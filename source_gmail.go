package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/gmail/v1"
)

// Самый незаметный способ: пригласить бота в звонок как человека.
//
// У бота есть свой Google-аккаунт — он всё равно нужен, чтобы заходить в Meet
// без «постучаться». В идущем звонке жмёшь «Добавить людей», вводишь
// steno@company.com, Google шлёт на этот адрес письмо со ссылкой. steno видит
// письмо и приходит. Приглашающему не нужно знать про steno вообще ничего —
// он зовёт его так же, как позвал бы коллегу.

type gmailSource struct {
	cfg *Config
	d   *Dispatcher
	log *log.Logger

	// Клиент переживает тики: иначе каждые 45 секунд перечитывался бы файл
	// ключа и заново обменивался OAuth-токен.
	mu  sync.Mutex
	svc *gmail.Service
}

func (s *gmailSource) Name() string { return "почта" }

func (s *gmailSource) Run(ctx context.Context) error {
	if s.cfg.Gmail.Account == "" {
		return fmt.Errorf("не указан gmail.account — почта аккаунта бота")
	}
	every := s.cfg.Gmail.PollEvery.D()
	if every <= 0 {
		every = 45 * time.Second
	}
	s.log.Printf("почта: слежу за приглашениями на %s, опрос раз в %s",
		s.cfg.Gmail.Account, every)

	t := time.NewTicker(every)
	defer t.Stop()
	s.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.poll(ctx)
		}
	}
}

func (s *gmailSource) service(ctx context.Context) (*gmail.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		return s.svc, nil
	}
	opt, err := googleClient(ctx, s.cfg,
		credentialsFile(s.cfg.Gmail.CredentialsFile, s.cfg.GoogleDocs.CredentialsFile),
		s.cfg.Gmail.Account, gmail.GmailReadonlyScope)
	if err != nil {
		return nil, err
	}
	svc, err := gmail.NewService(ctx, opt)
	if err != nil {
		return nil, err
	}
	s.svc = svc
	return svc, nil
}

func (s *gmailSource) poll(ctx context.Context) {
	srv, err := s.service(ctx)
	if err != nil {
		s.log.Printf("почта: %v", err)
		return
	}
	// Час назад — потолок: письмо о звонке, который начался давно, уже не
	// повод куда-то идти.
	list, err := srv.Users.Messages.List("me").
		Q("newer_than:1h").MaxResults(25).Context(ctx).Do()
	if err != nil {
		s.log.Printf("почта: %v", err)
		return
	}
	for _, ref := range list.Messages {
		key := "gmail:" + ref.Id
		if seen, err := s.d.st.EventSeen(key); err != nil || seen {
			continue
		}
		msg, err := srv.Users.Messages.Get("me", ref.Id).Format("full").Context(ctx).Do()
		if err != nil {
			s.log.Printf("почта: письмо %s: %v", ref.Id, err)
			continue
		}
		s.consider(ctx, key, msg)
	}
}

func (s *gmailSource) consider(ctx context.Context, key string, msg *gmail.Message) {
	from, subject := header(msg, "From"), header(msg, "Subject")
	if !s.senderAllowed(from) {
		// Письмо не из своего домена — не повод тащить бота в чужой звонок.
		s.skip(key)
		return
	}
	body := msg.Snippet + "\n" + messageText(msg.Payload)
	meetURL := findMeetURL(body)
	if meetURL == "" {
		// Письмо со ссылкой на неподдержанную площадку не должно уходить в
		// тишину: снаружи это выглядит как «бот проигнорировал приглашение»,
		// и разбираются с этим уже на созвоне, куда он не пришёл.
		if hint := linkHint(body); hint != "" {
			s.log.Printf("почта: «%s» — %s", orDash(subject), hint)
		}
		s.skip(key)
		return
	}
	m := &Meeting{
		ID:        newID(time.Now()),
		Title:     firstNonEmpty(subject, "Созвон по приглашению на почту"),
		MeetURL:   meetURL,
		StartedAt: time.Now(),
		Status:    "recording",
	}
	// Дедупликация двухуровневая, и оба уровня обязательны. Ключ по ссылке не
	// даёт двум источникам привести двух ботов. Ключ по письму не даёт этому
	// же письму сработать снова через 45 секунд: adHocKey округляет время до
	// получаса, поэтому на границе получаса он стал бы другим — и на идущий
	// созвон пришёл бы второй бот.
	//
	// Но письмо помечается разобранным, только если созвон действительно
	// пристроен. Отказ из-за нехватки слотов — временный: пометив письмо, мы
	// забанили бы приглашение навсегда, хотя место освободится через минуту.
	switch s.d.Start(ctx, adHocKey(meetURL, time.Now()), m, "приглашение от "+from) {
	case Started, Duplicate:
		s.skip(key)
	case NoCapacity:
		s.log.Printf("почта: «%s» подождёт свободного слота", orDash(subject))
	}
}

// skip помечает письмо разобранным, чтобы не тянуть его снова каждые 45 секунд.
func (s *gmailSource) skip(key string) {
	if _, err := s.d.st.MarkEventSeen(key, ""); err != nil {
		s.log.Printf("почта: не отметил письмо: %v", err)
	}
}

// senderAllowed пропускает только свой домен. Иначе любой, кто узнал адрес
// бота, сможет позвать его в свой звонок — и записать разговор чужими руками.
//
// Заголовок From разбирается по-настоящему, а не ищется подстрокой: подстрока
// "@company.com" находится и в "attacker@company.com.evil.net", и в имени
// отправителя «"steno@company.com" <attacker@evil.net>».
func (s *gmailSource) senderAllowed(from string) bool {
	domains := s.cfg.Gmail.AllowedDomains
	if len(domains) == 0 {
		// По умолчанию — домен самого бота.
		if i := strings.LastIndex(s.cfg.Gmail.Account, "@"); i >= 0 {
			domains = []string{s.cfg.Gmail.Account[i+1:]}
		}
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return false
	}
	i := strings.LastIndex(addr.Address, "@")
	if i < 0 {
		return false
	}
	host := strings.ToLower(addr.Address[i+1:])
	for _, d := range domains {
		if d != "" && host == strings.ToLower(d) {
			return true
		}
	}
	return false
}

func header(msg *gmail.Message, name string) string {
	if msg.Payload == nil {
		return ""
	}
	for _, h := range msg.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// messageText собирает текст письма из всех текстовых частей MIME-дерева.
func messageText(p *gmail.MessagePart) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.Body != nil && p.Body.Data != "" && strings.HasPrefix(p.MimeType, "text/") {
		// Gmail отдаёт base64url, и выравнивание в нём иногда отсутствует.
		raw, err := base64.URLEncoding.DecodeString(p.Body.Data)
		if err != nil {
			raw, err = base64.RawURLEncoding.DecodeString(p.Body.Data)
		}
		if err == nil {
			b.Write(raw)
			b.WriteString("\n")
		}
	}
	for _, part := range p.Parts {
		b.WriteString(messageText(part))
	}
	return b.String()
}
