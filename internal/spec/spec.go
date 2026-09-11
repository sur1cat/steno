// Package spec превращает задачу с созвона в техническое задание по коду
// проекта — и, по отдельной команде человека, отдаёт это задание агенту.
//
// Зачем это отдельно от follow-up. Follow-up отвечает на вопрос «что было
// сказано»: он обязан ничего не потерять и ничего не додумать. ТЗ отвечает на
// другой вопрос — «что с этим делать в коде», — и у него другой источник
// правды: репозиторий. Одна фраза с созвона несёт примерно десятую часть того,
// что нужно исполнителю, и оставшиеся девять десятых берутся не из воздуха, а
// из кода: где лежит модуль, как называется сервис, чем проект проверяют.
//
// Отсюда главное свойство здешнего ТЗ, ради которого всё и написано: **оно
// обязано называть свои дыры**. «Ануару нужно закончить Сапар» — это не
// задача. Полное ТЗ из такой фразы можно только выдумать, а выдуманное ТЗ хуже
// отсутствующего: по нему что-то сделают. Поэтому раздел «чего не хватает»
// здесь не украшение и не пожелание промпту — за его наличием следит код
// (см. Gate), и ТЗ без него нельзя отдать агенту.
package spec

import (
	"fmt"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/i18n"
)

// Status — что с ТЗ происходит. Отказ тоже хранится: человек должен видеть,
// что задачу посмотрели и почему не взяли, иначе он нажмёт ту же кнопку ещё
// раз и заплатит второй раз за тот же ответ.
const (
	StatusDraft    = "draft"    // ТЗ готово, читать глазами
	StatusRejected = "rejected" // задача не про код — ТЗ не делали
	StatusRunning  = "running"  // по ТЗ идёт агент
	StatusDone     = "done"     // агент отработал, есть ветка
	StatusFailed   = "failed"   // агент не отработал
)

// Spec — само техническое задание.
//
// Порядок полей — порядок чтения. Сначала что и зачем, потом где в коде, потом
// шаги, и только в конце — предположения и дыры. Дыры внизу не потому, что они
// менее важны, а потому, что до них надо дочитать: раздел «чего не хватает» в
// начале документа читается как отписка, а в конце — как список вопросов,
// которые человек сейчас пойдёт задавать.
type Spec struct {
	ID      string
	ItemID  string // задача из разбора, по которой это ТЗ
	Project string
	Repo    string // каталог репозитория, по которому ТЗ собрано

	Title    string       `json:"title"`
	Summary  []string     `json:"summary"`  // 2–4 строки: что сделать и зачем
	Known    []string     `json:"known"`    // что известно точно, из цитаты и кода
	Places   []Place      `json:"places"`   // где в коде это живёт
	Steps    []string     `json:"steps"`    // что делать по порядку
	Checks   []string     `json:"checks"`   // чем проверить, что сделано
	Unknowns []Unknown    `json:"unknowns"` // чего не хватает — обязательный раздел
	Guesses  []Assumption `json:"guesses"`  // на чём ТЗ держится, если не спросить
	NotHere  []string     `json:"not_here"` // чего в этой задаче делать не надо

	// Blocked — сама модель считает, что без ответов начинать нельзя. Это её
	// суждение, а не наша проверка; наши проверки живут в Gate.
	Blocked bool   `json:"blocked"`
	Why     string `json:"why"` // одной строкой: почему blocked

	Status    string
	Reject    string // почему задачу не взяли (StatusRejected)
	CreatedAt time.Time
	Model     string
	USD       float64

	// Ход исполнения. Пусто, пока никто не нажимал.
	Branch    string
	Worktree  string
	RunLog    string
	RunError  string
	RunBy     string
	StartedAt time.Time
}

// Place — место в коде. Found проставляем не мы и не модель: путь проверяется
// os.Stat по репозиторию уже после ответа. Модель, назвавшая несуществующий
// файл, — это ровно тот случай, ради которого затевалась вся честность, и
// молчать о нём нельзя.
type Place struct {
	Path  string `json:"path"`
	Why   string `json:"why"`
	Found bool   `json:"-"`
}

// Unknown — дыра в задании. Ask — у кого спрашивать: список вопросов без
// адресата человек прочитает и отложит, потому что непонятно, кому их нести.
type Unknown struct {
	Question string `json:"question"`
	Why      string `json:"why"`
	Ask      string `json:"ask"`
}

// Assumption — то, что ТЗ додумало за неимением ответа. IfWrong обязателен:
// предположение без цены ошибки неотличимо от факта, а вся разница между
// полезным ТЗ и выдуманным именно в этом.
type Assumption struct {
	What    string `json:"what"`
	IfWrong string `json:"if_wrong"`
}

// Gate — почему по этому ТЗ нельзя запускать агента. Пустой список означает
// «можно».
//
// Это не советы и не предупреждения: пока список непуст, Run отказывается
// работать. Проверки нарочно тупые и считаются по самому документу, а не по
// уверенности модели, — потому что уверенность модели и есть то, что здесь
// проверяется. Модель, которая выдумала ТЗ целиком, будет уверена в нём
// сильнее всего.
func (s *Spec) Gate() []string {
	var out []string
	if len(s.Unknowns) == 0 {
		// Фраза с созвона не содержит всего, что нужно исполнителю. ТЗ, у
		// которого не нашлось ни одного вопроса, не разобралось в задаче, а
		// сочинило её.
		out = append(out, i18n.Tr("в ТЗ нет ни одного открытого вопроса — по фразе с созвона так не бывает"))
	}
	if len(s.Places) == 0 {
		out = append(out, i18n.Tr("ТЗ не называет ни одного места в коде"))
	} else if s.FoundPlaces() == 0 {
		out = append(out, i18n.Tr("ни один путь из ТЗ не нашёлся в репозитории"))
	}
	if len(s.Steps) == 0 {
		out = append(out, i18n.Tr("в ТЗ нет ни одного шага работы"))
	}
	if s.Blocked {
		why := strings.TrimSpace(s.Why)
		if why == "" {
			why = i18n.Tr("без ответов на вопросы начинать нельзя")
		}
		out = append(out, why)
	}
	return out
}

// FoundPlaces — сколько путей из ТЗ и правда есть в репозитории.
func (s *Spec) FoundPlaces() int {
	n := 0
	for _, p := range s.Places {
		if p.Found {
			n++
		}
	}
	return n
}

// Render — ТЗ так, как его читают глазами: в чате, в панели, в файле рядом с
// веткой. Markdown, потому что именно им steno публикует всё остальное.
func (s *Spec) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", firstNonEmpty(s.Title, i18n.Tr("Задание без названия")))
	fmt.Fprintf(&b, i18n.Tr("**Проект:** %s  \n"), s.Project)
	if s.ItemID != "" {
		fmt.Fprintf(&b, i18n.Tr("**Задача:** %s  \n"), s.ItemID)
	}
	if s.Repo != "" {
		fmt.Fprintf(&b, i18n.Tr("**Репозиторий:** %s\n"), s.Repo)
	}
	b.WriteString("\n")

	if len(s.Summary) > 0 {
		b.WriteString(strings.Join(s.Summary, "\n"))
		b.WriteString("\n\n")
	}

	// Сначала — можно ли по этому работать. Человек, который открыл ТЗ, должен
	// узнать про его негодность в первой же строке, а не дочитав до конца.
	if reasons := s.Gate(); len(reasons) > 0 {
		b.WriteString(i18n.Tr("> **По этому ТЗ нельзя запускать агента.**\n"))
		for _, r := range reasons {
			fmt.Fprintf(&b, "> - %s\n", r)
		}
		b.WriteString("\n")
	}

	section(&b, i18n.Tr("## Что известно"), s.Known)

	if len(s.Places) > 0 {
		b.WriteString(i18n.Tr("\n## Где это в коде\n\n"))
		for _, p := range s.Places {
			mark := ""
			if !p.Found {
				// Не прячем и не выкидываем: выдуманный путь — это сведение о
				// качестве ТЗ, и человеку оно нужнее, чем аккуратный список.
				mark = i18n.Tr("  ← **такого пути в репозитории нет**")
			}
			fmt.Fprintf(&b, "- `%s`%s\n", p.Path, mark)
			if strings.TrimSpace(p.Why) != "" {
				fmt.Fprintf(&b, "  %s\n", p.Why)
			}
		}
	}

	numbered(&b, i18n.Tr("## Что сделать"), s.Steps)
	section(&b, i18n.Tr("## Чем проверить"), s.Checks)
	section(&b, i18n.Tr("## Чего в этой задаче делать не надо"), s.NotHere)

	if len(s.Guesses) > 0 {
		b.WriteString(i18n.Tr("\n## На чём это держится\n\n"))
		b.WriteString(i18n.Tr("Ответов не было, поэтому ТЗ предполагает вот что. Если предположение неверно — переделывать придётся отсюда.\n\n"))
		for _, g := range s.Guesses {
			fmt.Fprintf(&b, "- %s\n", g.What)
			if strings.TrimSpace(g.IfWrong) != "" {
				fmt.Fprintf(&b, i18n.Tr("  Если не так: %s\n"), g.IfWrong)
			}
		}
	}

	// Раздел, ради которого всё писалось. Он последний и он же единственный
	// обязательный: ТЗ без него не проходит Gate.
	b.WriteString(i18n.Tr("\n## Чего не хватает, чтобы это сделать\n\n"))
	if len(s.Unknowns) == 0 {
		b.WriteString(i18n.Tr("Ни одного вопроса не названо — это само по себе повод не доверять этому ТЗ.\n"))
	}
	for _, u := range s.Unknowns {
		fmt.Fprintf(&b, "- **%s**\n", u.Question)
		if strings.TrimSpace(u.Why) != "" {
			fmt.Fprintf(&b, "  %s\n", u.Why)
		}
		if strings.TrimSpace(u.Ask) != "" {
			fmt.Fprintf(&b, i18n.Tr("  Спросить: %s\n"), u.Ask)
		}
	}
	return b.String()
}

func section(b *strings.Builder, head string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n\n", head)
	for _, l := range lines {
		fmt.Fprintf(b, "- %s\n", l)
	}
}

func numbered(b *strings.Builder, head string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s\n\n", head)
	for i, l := range lines {
		fmt.Fprintf(b, "%d. %s\n", i+1, l)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
