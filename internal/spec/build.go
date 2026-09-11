package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Сборка ТЗ по задаче.
//
// Порядок здесь — от дешёвого к дорогому, и это не оптимизация, а отбор.
// Сначала проверки, которые не стоят ничего и ни у кого не спрашивают: та ли
// это задача, есть ли у проекта репозиторий, лежит ли он на этой машине. Потом
// короткий запрос «делается ли это кодом вообще». И только потом — большой
// запрос с материалом.
//
// Отказ на любом шаге — это тоже результат, и он сохраняется. Человек должен
// видеть, что задачу посмотрели и почему не взяли; иначе он нажмёт ту же кнопку
// ещё раз и заплатит второй раз за тот же ответ.

type Deps struct {
	Cfg *core.Config
	St  *core.Store
	Sp  *Store
}

// Build делает ТЗ по задаче из разбора.
//
// Возвращённый *Spec всегда осмыслен: либо это задание, либо запись об отказе с
// причиной. Ошибку возвращаем только тогда, когда не сработала механика —
// сломалась база, не ответила модель.
func Build(ctx context.Context, d Deps, itemID string) (*Spec, error) {
	it, err := FindItem(d.St, itemID)
	if err != nil {
		return nil, err
	}
	sp := &Spec{ID: newID(), ItemID: it.ID, Project: it.Project}

	// --- проверки, которые ничего не стоят ---

	if it.Kind != core.KindTask {
		return d.reject(sp, i18n.Tr("это не задача, а решение или вопрос — исполнять нечего"))
	}
	if it.Project == core.UnassignedProject || strings.TrimSpace(it.Project) == "" {
		// Без проекта нет и репозитория. Угадывать его по словам задачи мы не
		// станем: ошибка здесь означает ветку в чужом проекте.
		return d.reject(sp, i18n.Tr("у задачи не определён проект — непонятно, в каком репозитории работать"))
	}
	p, err := d.St.Project(it.Project)
	if err != nil {
		return d.reject(sp, i18n.Trf("проект %q не заведён", it.Project))
	}
	repo, err := RepoOf(d.Cfg.DataDir, p)
	if err != nil {
		return d.reject(sp, err.Error())
	}
	sp.Repo = repo

	// --- короткий запрос: делается ли это кодом ---

	ev := Collect(ctx, repo, it)
	verdict, why, spend, err := Triage(ctx, d.Cfg, it, p, ev)
	if err != nil {
		return nil, err
	}
	sp.USD, sp.Model = spend.USD, spend.Model
	if verdict != "code" {
		return d.reject(sp, why)
	}

	// --- большой запрос ---

	primer := ""
	if c, err := d.St.ProjectContext(p.Name); err == nil {
		primer = c.Primer
	}
	openItems := ""
	if items, err := d.St.OpenItems(p.Name); err == nil {
		var others []core.ProjectItem
		for _, o := range items {
			if o.ID != it.ID {
				others = append(others, o)
			}
		}
		openItems = core.RenderOpenItems(others)
	}
	var meeting *core.Meeting
	if it.OpenedIn != "" {
		meeting, _ = d.St.Meeting(it.OpenedIn)
	}

	maxTokens := int64(d.Cfg.Claude.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	out, spend2, err := brain.AskLLM(ctx, d.Cfg, specSystem,
		head(d.Cfg, it, p, primer, openItems, ev, meeting), specSchema(), maxTokens)
	if err != nil {
		return nil, err
	}
	sp.USD += spend2.USD
	if spend2.Model != "" {
		sp.Model = spend2.Model
	}
	// Разбираем в отдельное значение и переносим только содержательные поля.
	// Прямой Unmarshal в sp сложил бы ответ модели поверх идентификаторов —
	// достаточно ключа "repo" в ответе, чтобы ТЗ поехало проверять пути по
	// каталогу, который назвала модель.
	var got Spec
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return nil, fmt.Errorf(i18n.Tr("разбор ТЗ: %w\nответ: %.400s"), err, out)
	}
	sp.Title, sp.Summary, sp.Known = got.Title, got.Summary, got.Known
	sp.Places, sp.Steps, sp.Checks = got.Places, got.Steps, got.Checks
	sp.Unknowns, sp.Guesses, sp.NotHere = got.Unknowns, got.Guesses, got.NotHere
	sp.Blocked, sp.Why = got.Blocked, got.Why

	// Пути проверяем сами. Модель, назвавшая несуществующий файл, звучит ровно
	// так же уверенно, как назвавшая существующий, и отличить одно от другого
	// можно только сходив на диск.
	verifyPlaces(repo, sp.Places)

	sp.Status = StatusDraft
	if err := d.Sp.Save(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

func (d Deps) reject(sp *Spec, why string) (*Spec, error) {
	sp.Status, sp.Reject = StatusRejected, why
	if err := d.Sp.Save(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

// Triage — короткий запрос об одном: делается ли задача правкой кода.
//
// Отдельной функцией, потому что у него есть своё применение помимо Build:
// пройтись по всем открытым задачам проекта и показать, за какие вообще имеет
// смысл браться, не собирая по каждой полное ТЗ.
func Triage(ctx context.Context, cfg *core.Config, it core.ProjectItem,
	p core.Project, ev Evidence) (verdict, why string, spend core.Spend, err error) {

	out, spend, err := brain.AskLLM(ctx, cfg, triageSystem, triageHead(it, p, ev), triageSchema(), 2000)
	if err != nil {
		return "", "", spend, err
	}
	var res struct {
		Verdict string `json:"verdict"`
		Why     string `json:"why"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", "", spend, fmt.Errorf(i18n.Tr("разбор отбора: %w\nответ: %.200s"), err, out)
	}
	// Всё, что не «code», — отказ. В том числе вердикт, которого мы не знаем:
	// незнакомое слово от модели не повод заводить ветку.
	if res.Verdict != "code" {
		why := strings.TrimSpace(res.Why)
		if why == "" {
			why = i18n.Tr("по формулировке не видно, что это делается кодом")
		}
		return res.Verdict, why, spend, nil
	}
	return "code", strings.TrimSpace(res.Why), spend, nil
}

// verifyPlaces проставляет Found и приводит пути к виду «от корня репозитория».
//
// Модель возвращает путь как придётся: с ./ впереди, со строкой через
// двоеточие, иногда абсолютный. Всё это один и тот же файл, и объявлять его
// несуществующим из-за оформления было бы враньём в другую сторону.
func verifyPlaces(repo string, places []Place) {
	for i := range places {
		clean := normalizePath(repo, places[i].Path)
		if clean == "" {
			places[i].Found = false
			continue
		}
		places[i].Path = clean
		_, err := os.Stat(filepath.Join(repo, clean))
		places[i].Found = err == nil
	}
}

func normalizePath(repo, p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "`\"' ")
	if p == "" {
		return ""
	}
	// «файл.py:120» и «файл.py:120-140» — это про файл.
	if i := strings.IndexByte(p, ':'); i > 0 {
		p = p[:i]
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(repo, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return ""
		}
		p = rel
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	p = filepath.Clean(p)
	// Выход за пределы репозитория — это не путь, а попытка. Обрубаем молча:
	// в ТЗ он всё равно окажется помеченным как ненайденный.
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}
