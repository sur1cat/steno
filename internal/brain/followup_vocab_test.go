package brain

import (
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// --- промпт follow-up ---------------------------------------------------------

// Ради этого всё и затевалось: модель должна прочитать, кто человек, а кто
// сервис, а не гадать по звучанию.
func TestFollowupHeadCarriesVocabulary(t *testing.T) {
	cfg := core.DefaultConfig()
	m := &core.Meeting{Title: "Планёрка", StartedAt: time.Now()}
	head := followupHead(cfg, m, []core.Project{{
		Name:       "Платежи",
		Aliases:    []string{"биллинг"},
		About:      "приём денег",
		People:     []string{"Орынгали", "Рустем"},
		Vocabulary: []string{"Сапар", "Орынгали"},
	}}, "", "")

	if !strings.Contains(head, "люди: Орынгали, Рустем") {
		t.Errorf("в промпт не попали люди проекта:\n%s", head)
	}
	if !strings.Contains(head, "Сапар") {
		t.Errorf("в промпт не попал словарь проекта:\n%s", head)
	}
	// Имя не должно оказаться в строке «не люди»: ровно из этой строки модель и
	// узнаёт, кому задачу назначать нельзя.
	for _, line := range strings.Split(head, "\n") {
		if strings.Contains(line, "не люди") && strings.Contains(line, "Орынгали") {
			t.Errorf("имя человека уехало в список «не люди»: %q", line)
		}
	}
	// Проект без словаря не должен приносить в промпт пустых строк: модель
	// читает «люди:» с пустым списком как «людей нет».
	bare := followupHead(cfg, m, []core.Project{{Name: "Онбординг"}}, "", "")
	if strings.Contains(bare, "люди:") || strings.Contains(bare, "не люди") {
		t.Errorf("у проекта без словаря появились пустые строки:\n%s", bare)
	}
}

// Списка мало — модели надо сказать, что с ним делать. И сказать теми же
// словами, какими подписаны сами списки: правило про «людей» рядом с данными,
// подписанными «участники», — это правило про раздел, которого в промпте нет.
// Разъезжаются они молча, и ловится это только так.
func TestFollowupSystemExplainsVocabulary(t *testing.T) {
	head := followupHead(core.DefaultConfig(), &core.Meeting{StartedAt: time.Now()}, []core.Project{{
		Name: "Платежи", People: []string{"Орынгали"}, Vocabulary: []string{"Сапар"},
	}}, "", "")

	for _, label := range []string{"люди", "сервисы и сокращения"} {
		if !strings.Contains(head, label) {
			t.Errorf("подпись %q пропала из данных промпта:\n%s", label, head)
		}
		if !strings.Contains(followupSystem, label) {
			t.Errorf("правило в системном промпте не упоминает %q — оно про другой раздел", label)
		}
	}
	// И правило должно быть запретом, а не описанием: список, про который
	// сказано только «бывает и такое», исполнителем всё равно станет.
	//
	// Переносы строк схлопываем: промпт свёрстан под 80 колонок, и фраза
	// разорвана посередине — искать её как есть значит проверять вёрстку.
	rule := strings.Join(strings.Fields(followupSystem), " ")
	for _, want := range []string{"на кого нельзя никогда", "не становится исполнителем"} {
		if !strings.Contains(rule, want) {
			t.Errorf("в системном промпте нет запрета назначать задачу на сервис: %q", want)
		}
	}
}

// Промпт не переводится, и это не недосмотр.
//
// Его читает Claude, а не человек, и он обязан совпадать с followupSystem —
// русской константой, на которой мерили качество разбора. Язык ответа задаётся
// в самом промпте, отдельной строкой и правилом 6, а не языком интерфейса.
//
// Тест сторожит возврат i18n.Tr() на эти строки: правка выглядит безобидной
// («забыли перевести»), а ломает она правила, которые ссылаются на слова
// каркаса дословно.
func TestFollowupPromptStaysRussianInEnglishUI(t *testing.T) {
	old := i18n.UILang
	i18n.UILang = i18n.LangEN
	defer func() { i18n.UILang = old }()

	// Ловушка. Из каталога эти строки убраны, поэтому вернувшийся i18n.Tr() сам по
	// себе ничего не изменит — и тест бы его не заметил, а заметил бы человек
	// на созвоне, когда кто-нибудь дозаполнит i18n_en.go. Подкладываем перевод
	// сами: теперь любая обёртка i18n.Tr() видна сразу.
	//
	// Каталог общий на пакет, поэтому тест не параллельный и всё за собой
	// убирает.
	for _, k := range []string{
		"Название встречи: %s\n", "Дата: %s\n", "Участники: %s\n",
		"Приглашены в календаре: %s\n", "Язык follow-up: %s\n",
		"\nПроекты команды:\n", " (вслух: %s)", "    люди: %s\n",
		"    сервисы и сокращения (не люди): %s\n",
		"  %s — если непонятно, к чему относится\n", "\nРасшифровка:\n\n",
		"неизвестно",
	} {
		if _, busy := i18n.TrEN[k]; busy {
			t.Fatalf("строка %q снова в каталоге переводов — промпт переводиться не должен", k)
		}
		i18n.TrEN[k] = "!ПЕРЕВЕДЕНО!"
		defer delete(i18n.TrEN, k)
	}

	cfg := core.DefaultConfig()
	head := followupHead(cfg, &core.Meeting{
		Title: "Планёрка", StartedAt: time.Now(),
		Participants: []string{"TomXemmings"},
		Invitees:     []string{"kto-to@example.com"},
	}, []core.Project{{
		Name: "Платежи", Aliases: []string{"биллинг"}, About: "приём денег",
		People: []string{"Орынгали"}, Vocabulary: []string{"Сапар"},
	}}, "", "")

	for _, want := range []string{
		"Название встречи:", "Дата:", "Участники:", "Приглашены в календаре:",
		"Проекты команды:", "(вслух:", "люди:", "сервисы и сокращения (не люди):",
		"если непонятно, к чему относится", "Расшифровка:",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("подпись %q перевелась — правила followupSystem ссылаются на русские слова:\n%s",
				want, head)
		}
	}

	// А язык ответа при этом английский, и сказано об этом внутри промпта.
	if cfg.Claude.OutputLanguage != "English" {
		t.Fatalf("язык follow-up при английском интерфейсе: %q", cfg.Claude.OutputLanguage)
	}
	if !strings.Contains(head, "Язык follow-up: English") {
		t.Errorf("в промпте нет указания отвечать по-английски:\n%s", head)
	}

	// «неизвестно» — не подпись, а слово, на которое ссылается правило 9:
	// «Имя "неизвестно" означает, что имя не удалось снять». Переведись оно —
	// правило перестало бы срабатывать, и реплика без имени молча уехала бы
	// соседнему говорящему.
	line := RenderTranscript([]core.Segment{{Start: 1, Text: "возьму на себя"}})
	if !strings.Contains(line, "неизвестно") {
		t.Errorf("имя говорящего без подписи перевелось: %q", line)
	}
	if !strings.Contains(strings.Join(strings.Fields(followupSystem), " "), "Имя «неизвестно»") {
		t.Error("правило про «неизвестно» пропало из followupSystem — слово в расшифровке осталось без смысла")
	}
}
