package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Followup — то, ради чего всё затевалось. Структура выбрана под то, как это
// читают: сначала три строки, дальше решения и задачи с владельцем и сроком.
// Пункт без владельца и срока не читают вообще, поэтому оба поля обязательны в
// схеме, а модели запрещено их выдумывать.
type Followup struct {
	Title         string         `json:"title"`
	TLDR          []string       `json:"tldr"`
	Decisions     []Decision     `json:"decisions"`
	ActionItems   []ActionItem   `json:"action_items"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
	Risks         []string       `json:"risks"`
	Timeline      []TimelineItem `json:"timeline"`
	// Что случилось с тем, что уже висело по проектам. Без этого каждый созвон
	// заводил бы новые копии тех же задач, и живое состояние проекта тонуло бы
	// в дублях.
	Updates []ItemUpdate `json:"updates"`
}

// ItemUpdate — судьба пункта, который висел открытым до этого созвона.
type ItemUpdate struct {
	ID     string `json:"id"`
	Status string `json:"status"` // done | dropped | still_open
	Note   string `json:"note"`
}

type Decision struct {
	What    string  `json:"what"`
	Why     string  `json:"why"`
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type ActionItem struct {
	Owner   string  `json:"owner"`
	What    string  `json:"what"`
	Due     string  `json:"due"`   // YYYY-MM-DD или "" если срок не назвали
	Quote   string  `json:"quote"` // дословно из транскрипта — чтобы можно было проверить
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type OpenQuestion struct {
	Question  string  `json:"question"`
	WaitingOn string  `json:"waiting_on"`
	At        float64 `json:"at"`
	Project   string  `json:"project"`
}

type TimelineItem struct {
	At      float64 `json:"at"`
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
}

const followupSystem = `Ты делаешь follow-up по расшифровке рабочего созвона.

Читать его будут те, кто на созвоне не был, и те, кто был, но забыл. Пиши так,
чтобы через месяц по этому тексту можно было восстановить, что решили и кто
что должен сделать.

## Что обязано попасть

Это главная часть работы. Пропустить прозвучавшее — дороже, чем сформулировать
неточно: ради этих списков follow-up и делается.

**Задача (action_items)** — всё, что кто-то после созвона должен сделать.
Считается задачей:
- прямое поручение, обычно с обращением по имени: «посчитай к завтрашнему»,
  «возьмёшь?»;
- взятое на себя: «Возьму, до среды сделаю», «Перепишу завтра», «Я посмотрю»;
- обещание доложить или проверить: «скажи в четверг вечером, как прошло»;
- то, что человек назвал своим следующим шагом, даже если его не просили.

Согласие — «возьму», «соберу», «договорились», «ок» — это не отдельная задача,
а подтверждение предыдущей: оно уточняет, на ком она и к какому сроку.

**Кому назначено — не всегда тот, кто говорит.** «Ануару нужно закончить», «это
на Вике», «пусть Борис посмотрит» — исполнитель здесь названный человек, а не
произносящий. И назначение действует до конца мысли: если дальше идёт «…
разобраться, что случилось, и разложить на части», это всё ещё его работа, даже
когда следующие куски расшифровки приписаны другому голосу. Расшифровка режет
речь на части механически, по паузам, и одно предложение легко оказывается
разорванным между двумя говорящими.

Не задача: рассуждение вслух о том, что хорошо бы когда-нибудь сделать, если
никто это на себя не взял.

**Решение (decisions)** — выбор, после которого будут действовать иначе:
что-то приняли, отвергли, поменяли план или срок. Отказ («субтитры не вариант»)
— такое же решение, как согласие.

**Открытый вопрос (open_questions)** — то, что назвали и не закрыли: ждут
данных, чужого ответа или отдельного разговора. Явно отложенное («давайте не
сейчас») — тоже открытый вопрос.

**Риск (risks)** — то, что может пойти не так, и последствие этого. Сюда же
то, что прямо не назвали риском, но из разговора видно.

## Кто есть кто

Участники в списке названы так, как подписан их аккаунт Google. Вслух к ним
обращаются иначе: по имени, сокращённо, иногда аккаунт вообще не похож на имя
(в списке короткий логин латиницей, а вслух человека зовут именем, которого в
этой подписи нет).

Разбирайся по разговору, кто есть кто:
- к кому обратились по имени, тот обычно отвечает следующей репликой;
- взявший задачу («возьму», «сделаю») — это тот, кто в этот момент говорит;
- по ходу созвона одного человека называют то полным именем, то сокращённым.

В owner пиши имя **из списка участников** — то есть подпись аккаунта, — когда
понял, о ком речь. Если сопоставить не вышло, пиши имя так, как оно прозвучало
вслух. Оставлять "не назначен" можно только тогда, когда из разговора и правда
не видно, на ком задача. Потерять задачу из-за того, что имя не совпало со
списком, нельзя: задача важнее аккуратности подписи.

У проектов бывают проставлены «люди» и «сервисы и сокращения». Это не
украшение справки, а разметка: люди — те, на кого задачу назначить можно,
сервисы и сокращения — те, на кого нельзя никогда. Слово из второго списка не
становится исполнителем, даже когда звучит как имя и стоит в предложении на
месте человека: «Сапар не успевает» — это про сервис. И наоборот, имя из
первого списка — человек, даже если в списке участников созвона его нет:
он мог не подключаться, но задачу на него повесили.

## Как заполнять

1. due — только если срок прозвучал. Относительные («к пятнице», «завтра»,
   «до среды») переводи в дату YYYY-MM-DD от даты созвона. Не прозвучал —
   пустая строка. Срок, названный в ответе («до среды сделаю»), — это срок
   задачи, о которой шла речь до этого.
2. quote — дословный фрагмент расшифровки, из которого видно задачу. Не
   пересказ, не длиннее двух предложений.
3. at — таймкод в секундах от начала записи, из ближайшей строки расшифровки.
   Он превращается в ссылку на момент в записи.
4. tldr — от двух до четырёх строк: не пересказ повестки, а что изменилось.
5. waiting_on в открытых вопросах — от кого ждут ответ: человек или сторона,
   а не название продукта. Ждут не от «VLive», а от того, с кем по нему созвон;
   если по расшифровке непонятно, от кого именно, — "не определено".
6. Пиши на том же языке, на котором говорили. Но если выше указан «Язык
   follow-up» — он важнее: пиши на нём целиком, и заголовки, и формулировки
   задач, даже если говорили на другом. Без вводных вроде «в этом созвоне
   обсуждалось» — сразу по делу.

## Чего не делать

7. Не выдумывай. Каждый пункт опирается на конкретное место в расшифровке.
   Ничего не решили — decisions пустой; но «ничего не решили» и «я не нашёл,
   как это назвать» — разные вещи, и второе не повод для пустого списка.
8. Не приписывай задачу тому, кто её не брал. Сомневаешься между двумя людьми —
   ставь того, чьи слова ближе, и это видно по quote.
9. Расшифровка машинная: в ней есть оговорки, обрывы и неверно распознанные
   слова. Восстанавливай смысл по контексту, но не додумывай факты.
   Имя «неизвестно» означает, что имя не удалось снять, а не что говорил
   кто-то другой: чаще всего это тот же человек, что и в соседних строках.
   Смена на «неизвестно» и обратно — не повод считать, что речь перешла к
   другому или что началась новая мысль.
   Отдельно про сроки: срок, расслышанный обрывком, пропадает совсем —
   «послезавтра» становится «и после», «к четвергу» становится «к чет».
   Увидев такой обрывок, не выбрасывай его: добавь в открытые вопросы, что
   срок назывался, но расслышан плохо, и приведи обрывок дословно. А срок,
   который рядом с ним прочитан уверенно, оставь в due как есть: обрывок
   по соседству — не повод стирать то, что расслышано. К срокам, названным
   расплывчато вслух («потом», «как-нибудь»), это не относится — там терять
   нечего.

## Проекты

10. У каждого решения, задачи и вопроса проставь project — название из списка
    проектов. Вслух проект часто называют иначе, чем он записан: ориентируйся
    на суть разговора, а не на совпадение слов. Даны справки — разбирайся по
    ним, там записано, какими словами команда говорит о каждом проекте. Не
    относится ни к одному или непонятно — "не определён". Лучше "не определён",
    чем приписать чужому проекту.
11. Если в списке «что уже висит открытым» есть пункт, судьба которого решилась
    на этом созвоне, добавь его в updates с его идентификатором: done —
    сделано, dropped — отменили или стало неактуально, still_open — обсуждали,
    но не закрыли (тогда note: что изменилось). Идентификаторы не выдумывай,
    только из списка.
12. Пункт из «уже висит открытым», о котором говорили, идёт в updates, а не
    заново в action_items. Но если на созвоне прозвучала новая задача, похожая
    на старую, — это новая задача. Сомневаешься, повтор это или новое, — заводи
    новое: потерянная задача хуже, чем задача, названная дважды.`

func followupSchema() map[string]any {
	str := map[string]any{"type": "string"}
	num := map[string]any{"type": "number"}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{
			"type": "object", "properties": props,
			"required": req, "additionalProperties": false,
		}
	}
	arr := func(items map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": items}
	}
	return obj(map[string]any{
		"title": str,
		"tldr":  arr(str),
		"decisions": arr(obj(map[string]any{"what": str, "why": str, "at": num, "project": str},
			"what", "why", "at", "project")),
		"action_items": arr(obj(map[string]any{
			"owner": str, "what": str, "due": str, "quote": str, "at": num, "project": str},
			"owner", "what", "due", "quote", "at", "project")),
		"open_questions": arr(obj(map[string]any{
			"question": str, "waiting_on": str, "at": num, "project": str},
			"question", "waiting_on", "at", "project")),
		"risks": arr(str),
		"timeline": arr(obj(map[string]any{"at": num, "title": str, "summary": str},
			"at", "title", "summary")),
		"updates": arr(obj(map[string]any{
			"id":     str,
			"status": map[string]any{"type": "string", "enum": []string{"done", "dropped", "still_open"}},
			"note":   str,
		}, "id", "status", "note")),
	}, "title", "tldr", "decisions", "action_items", "open_questions", "risks", "timeline", "updates")
}

// renderTranscript готовит расшифровку в виде, который модель читает лучше
// всего: таймкод, имя, реплика. Соседние реплики одного человека склеиваются.
func renderTranscript(segs []Segment) string {
	var b strings.Builder
	last := ""
	for _, s := range segs {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		sp := s.Speaker
		if sp == "" {
			sp = "неизвестно"
		}
		if sp == last {
			b.WriteString(" ")
			b.WriteString(txt)
			continue
		}
		if last != "" {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s: %s", clock(s.Start), sp, txt)
		last = sp
	}
	b.WriteString("\n")
	return b.String()
}

func clock(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
}

func makeFollowup(ctx context.Context, cfg *Config, m *Meeting, segs []Segment, projects []Project, primers, openItems string) (*Followup, Spend, error) {
	head := followupHead(cfg, m, projects, primers, openItems)

	maxTokens := int64(cfg.Claude.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	out, spend, err := askLLM(ctx, cfg, followupSystem,
		head+renderTranscript(segs), followupSchema(), maxTokens)
	if err != nil {
		return nil, Spend{}, err
	}

	var f Followup
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		return nil, Spend{}, fmt.Errorf(tr("разбор follow-up: %w\nответ: %.400s"), err, out)
	}
	// Названия проектов приводим к тем, что записаны в конфиге: модель может
	// вернуть «биллинг» там, где проект называется «Платежи».
	for i := range f.ActionItems {
		f.ActionItems[i].Project = matchProject(projects, f.ActionItems[i].Project)
	}
	for i := range f.Decisions {
		f.Decisions[i].Project = matchProject(projects, f.Decisions[i].Project)
	}
	for i := range f.OpenQuestions {
		f.OpenQuestions[i].Project = matchProject(projects, f.OpenQuestions[i].Project)
	}

	return &f, spend, nil
}

// followupHead — всё, что модель читает до расшифровки: кто встречался, какие
// у команды проекты, что о них известно и что по ним висит.
//
// Вынесено из makeFollowup отдельно, чтобы это можно было прочитать глазами и
// проверить тестом. Промпт — единственная часть steno, которая не падает, когда
// ломается: словарь, не доехавший до модели, выглядит как обычный follow-up, в
// котором задача просто уехала не тому человеку.
// Подписи здесь — и в renderTranscript — намеренно не идут через tr(). Промпт
// читает Claude, а не человек, и он должен совпадать с followupSystem, который
// написан по-русски и на котором мерили качество разбора. Язык ответа задаётся
// отдельно и дважды: строкой «Язык follow-up» из настроек и правилом 6 «пиши на
// том же языке, на котором говорили», — от языка интерфейса он не зависит.
//
// Цена перевода каркаса не в стиле, а в правилах, которые ссылаются на его
// слова. Правило 9 говорит про имя «неизвестно» дословно; переведись оно в
// «unknown» — правило перестало бы срабатывать, и реплика без имени молча
// приписалась бы соседнему говорящему.
func followupHead(cfg *Config, m *Meeting, projects []Project, primers, openItems string) string {
	var head strings.Builder
	fmt.Fprintf(&head, "Название встречи: %s\n", orDash(m.Title))
	fmt.Fprintf(&head, "Дата: %s\n", m.StartedAt.Format("2006-01-02 15:04 MST"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&head, "Участники: %s\n", strings.Join(m.Participants, ", "))
	}
	if len(m.Invitees) > 0 {
		fmt.Fprintf(&head, "Приглашены в календаре: %s\n", strings.Join(m.Invitees, ", "))
	}
	if cfg.Claude.OutputLanguage != "" {
		fmt.Fprintf(&head, "Язык follow-up: %s\n", cfg.Claude.OutputLanguage)
	}

	if len(projects) > 0 {
		head.WriteString("\nПроекты команды:\n")
		for _, p := range projects {
			fmt.Fprintf(&head, "  %s", p.Name)
			if len(p.Aliases) > 0 {
				fmt.Fprintf(&head, " (вслух: %s)", strings.Join(p.Aliases, ", "))
			}
			if p.About != "" {
				fmt.Fprintf(&head, " — %s", p.About)
			}
			head.WriteString("\n")
			// Словарь проекта — двумя строками, а не одной. Плоский список слов
			// оставляет модель гадать по звучанию, кто из них человек, а
			// ошибка в эту сторону и есть та самая задача, уехавшая не туда:
			// владельцем становится сервис или сосед по алфавиту.
			if len(p.People) > 0 {
				fmt.Fprintf(&head, "    люди: %s\n", strings.Join(p.People, ", "))
			}
			if other := p.otherWords(); len(other) > 0 {
				fmt.Fprintf(&head, "    сервисы и сокращения (не люди): %s\n",
					strings.Join(other, ", "))
			}
		}
		fmt.Fprintf(&head, "  %s — если непонятно, к чему относится\n", unassignedProject)
	}
	if primers != "" {
		head.WriteString("\n")
		head.WriteString(primers)
	}
	if openItems != "" {
		head.WriteString("\n")
		head.WriteString(openItems)
	}
	head.WriteString("\nРасшифровка:\n\n")
	return head.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
