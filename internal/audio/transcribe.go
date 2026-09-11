package audio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Транскрипция вынесена за границу процесса: адаптер — это любая команда,
// которая берёт путь к аудио и печатает в stdout JSON вида
//
//	{"segments":[{"start":1.2,"end":3.4,"text":"..."}]}
//
// Так движок распознавания меняется правкой конфига, а не пересборкой:
// whisper.cpp, faster-whisper, что угодно ещё.
//
// Три поля сверх этого — необязательные, и появились они потому, что договор
// без них оказался уже, чем движки, которые в него подключают.
//
//	speaker      — кто говорит. У движка с диаризацией это своё знание о звуке,
//	               а не наша догадка по DOM площадки. Что с ним делает
//	               AlignSpeakers — см. там, это самое тонкое место.
//	confidence   — уверенность движка в реплике, 0..1.
//	words        — слова с уверенностью каждого. Ради них всё и затевалось:
//	               ошибка распознавания — это одно слово посреди верной фразы, и
//	               на уровне реплики её не видно (confidence.go).
//
// Отсутствие поля означает «движок не сказал», а не ноль. Поэтому уверенность
// здесь — указатель: адаптер, написанный до всей этой истории, продолжает
// работать без единой правки и получает nil, то есть «не знаю», а не 0, то есть
// «мусор».
//
// Ещё два необязательных поля появились вместе со вторым проходом по словарю
// (merge.go):
//
//	end_confidence — у реплики: уверенность движка, что реплика на этом
//	               кончилась. У whisper это вероятность закрывающего токена
//	               таймкода; низкая означает «оборвал, не договорив» — так на
//	               записи пропало «SDK» в конце фразы, и только по этому числу
//	               его можно вернуть из прохода со словарём.
//	vocabulary   — у всего ответа: как адаптер применил словарь созвона.
//	               "prompt" — подсказкой, как текст, который движок продолжает
//	               (whisper и всё на его основе): это чинит слова, но ломает
//	               разбивку на реплики, и steno делает второй проход без
//	               словаря, чтобы взять разбивку из него. "keyterms" — отдельным
//	               списком слов, не трогая разбивку (AssemblyAI): чинить нечего,
//	               второго прохода не будет. Пусто — считается "prompt".
type transcriptOut struct {
	Vocabulary string `json:"vocabulary"`
	Segments   []struct {
		Start   float64  `json:"start"`
		End     float64  `json:"end"`
		Text    string   `json:"text"`
		Speaker string   `json:"speaker"`
		Conf    *float64 `json:"confidence"`
		EndConf *float64 `json:"end_confidence"`
		Words   []struct {
			Word  string   `json:"word"`
			Start float64  `json:"start"`
			End   float64  `json:"end"`
			Conf  *float64 `json:"confidence"`
		} `json:"words"`
	} `json:"segments"`
}

// vocabularyAsKeyterms — ответ адаптера, после которого второй проход не нужен.
const vocabularyAsKeyterms = "keyterms"

// sane приводит уверенность к тому, чему можно верить.
//
// Движки меряют по-разному: whisper даёт вероятность 0..1, а кто-то отдаёт
// логарифм правдоподобия, то есть число вроде -0.7. Принять -0.7 за уверенность
// значит объявить всю расшифровку сомнительной; принять 100 за уверенность —
// наоборот, промолчать обо всём. Число вне 0..1 — это не «плохо расслышано», а
// «мы не поняли, что нам сказали», и честный ответ на него — nil.
func sane(c *float64) *float64 {
	if c == nil || *c < 0 || *c > 1 {
		return nil
	}
	return c
}

// SegmentsFromCaptions делает расшифровку прямо из субтитров Meet. Текст
// заметно хуже, чем у whisper, — Meet теряет окончания и путает термины, — но
// имена говорящих в нём уже проставлены, и работает это без единой установки.
// Способ довести конвейер до конца в первый же день и увидеть настоящий
// follow-up, а не заглушку.
func SegmentsFromCaptions(captionsPath string) ([]core.Segment, error) {
	utts, err := ReadUtterances(captionsPath)
	if err != nil {
		return nil, fmt.Errorf(i18n.Tr("субтитры %s: %w"), captionsPath, err)
	}
	if len(utts) == 0 {
		return nil, fmt.Errorf(i18n.Tr("субтитры пусты: в звонке они не включились, ")+
			i18n.Tr("и брать текст неоткуда (%s)"), captionsPath)
	}
	segs := make([]core.Segment, 0, len(utts))
	for _, u := range utts {
		if txt := strings.TrimSpace(u.Text); txt != "" {
			segs = append(segs, core.Segment{Start: u.Start, End: u.End, Speaker: u.Speaker, Text: txt})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, nil
}

// Расшифровки идут по очереди. На large-v3 одна занимает машину на минуты, и
// четыре созвона, кончившиеся в одну минуту, положили бы её целиком. Ожидание
// ничего не стоит: аудио уже на диске.
var transcribeQueue = make(chan struct{}, 1)

func init() { transcribeQueue <- struct{}{} }

// ResizeTranscribeQueue настраивает, сколько расшифровок идёт одновременно.
func ResizeTranscribeQueue(n int) {
	if n < 1 {
		n = 1
	}
	transcribeQueue = make(chan struct{}, n)
	for i := 0; i < n; i++ {
		transcribeQueue <- struct{}{}
	}
}

// RunTranscriber возвращает ещё и то, что адаптер написал в stderr: там он
// сообщает, какой моделью работал и какой язык определил. Это ровно те два
// факта, по которым потом понимаешь, почему расшифровка вышла такой.
//
// Четвёртый параметр — словарь созвона (vocab.go); nil — подсказывать нечего.
// Форма команды расшифровки от него не зависит — она записана в конфигах у
// людей как [адаптер, {{audio}}, {{language}}], и лишний аргумент сломал бы их
// все. Словарь уходит окружением, как уже уходит WHISPER_THREADS, и в двух
// видах сразу: STENO_PROMPT — фразой, для движков, которые продолжают её как
// текст (whisper), STENO_TERMS — списком через запятую, для движков с
// настоящим входом для словаря (AssemblyAI). Адаптер без словаря работает
// ровно как раньше.
//
// Со словарём адаптер зовётся дважды, и вот почему. Подсказка whisper чинит
// имена, но ломает разбивку на реплики — а от границ реплик зависит, кому
// достанется имя говорящего. Поэтому один проход идёт со словарём, другой без,
// и из прохода со словарём берутся только слова, в которых проход без словаря
// не уверен; всё остальное — границы, таймкоды — от прохода без словаря. Как
// именно, и что это дало на настоящих записях, — в merge.go.
//
// Два прохода — это вдвое дольше, и там, где второй ничего не даёт, его нет.
// Решает это сам адаптер, полем vocabulary в ответе: движок, который принял
// словарь отдельным списком слов (keyterms), разбивку не ломал, и чинить в
// ней нечего. Признак — у адаптера, а не в настройке и не в имени файла:
// как именно движок применяет словарь, знает только адаптер, и спрашивать об
// этом человека, который просто выбрал движок, было бы нечестно. Адаптер,
// написанный до этого поля, молчит — и получает два прохода: это безопасный
// исход, он стоит времени, а не слов.
//
// Порядок проходов от этого обратный тому, как о них думаешь: сначала со
// словарём, потому что только его ответ говорит, нужен ли второй.
func RunTranscriber(ctx context.Context, cfg *core.Config, audioPath string, vocab *Vocab) ([]core.Segment, string, error) {
	if len(cfg.Transcribe.Cmd) == 0 {
		return nil, "", errors.New(i18n.Tr("не настроен transcribe.cmd"))
	}
	// Ждём очереди до того, как заводить таймаут: иначе созвон, простоявший в
	// очереди час, сорвётся по таймауту, не начав расшифровываться.
	select {
	case <-transcribeQueue:
		defer func() { transcribeQueue <- struct{}{} }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}

	// Словарь созвона. Пустой не передаём вовсе: адаптер по отсутствию
	// переменной отличает «словаря нет» от «словарь пустой» и в первом случае
	// зовёт whisper теми же ключами, что и до всей этой истории.
	prompt := ""
	if vocab != nil {
		prompt = vocab.Prompt(promptBudget)
	}
	if prompt == "" {
		p, err := runPass(ctx, cfg, audioPath, nil)
		if err != nil {
			return nil, "", err
		}
		reportUncertain(p.segs)
		return p.segs, p.notes, nil
	}

	log.Printf(i18n.Tr("словарь для распознавания: %s"), i18n.Cut(prompt, 200))
	hinted, err := runPass(ctx, cfg, audioPath, []string{
		"STENO_PROMPT=" + prompt,
		"STENO_TERMS=" + strings.Join(vocab.Terms(), ", "),
	})
	if err != nil {
		return nil, "", err
	}
	if hinted.vocabulary == vocabularyAsKeyterms {
		// Словарь ушёл отдельным списком, разбивка цела — второй проход
		// ничего бы не исправил, а стоил бы столько же, сколько первый.
		reportUncertain(hinted.segs)
		return hinted.segs, hinted.notes, nil
	}

	started := time.Now()
	plain, err := runPass(ctx, cfg, audioPath, nil)
	if err != nil {
		return nil, "", err
	}
	if !hasWordConfidence(plain.segs) {
		// Без уверенности по словам сливать не по чему: взять можно только
		// то, в чём проход без словаря не уверен, а он не сказал ничего.
		// Остаётся то, что было до второго прохода, — расшифровка со словарём.
		log.Printf(i18n.Tr("второй проход без словаря занял %s, но движок не даёт уверенности по словам — слить проходы нельзя, взята расшифровка со словарём"),
			time.Since(started).Round(time.Second))
		reportUncertain(hinted.segs)
		return hinted.segs, plain.notes + "\n" + hinted.notes, nil
	}
	segs, taken := mergePasses(plain, hinted)
	// Одна строка о том, за что заплачено временем: сколько слов дал словарь и
	// что разбивка на реплики осталась от прохода без него.
	log.Printf(i18n.Tr("второй проход без словаря занял %s: из прохода со словарём взято %s, разбивка на реплики (%d) — из прохода без него"),
		time.Since(started).Round(time.Second),
		i18n.Plural(taken, i18n.Tr("слово"), i18n.Tr("слова"), i18n.Tr("слов")), len(segs))
	reportUncertain(segs)
	return segs, plain.notes + "\n" + hinted.notes, nil
}

// hasWordConfidence — сказал ли движок хоть об одном слове, насколько он в нём
// уверен. Без этого слияние проходов невозможно.
func hasWordConfidence(segs []core.Segment) bool {
	for _, s := range segs {
		for _, w := range s.Words {
			if w.Conf != nil {
				return true
			}
		}
	}
	return false
}

// reportUncertain — строка в лог о плохо расслышанных словах. Молча
// посчитанная уверенность ничем не отличается от несчитанной: движок без неё
// даёт 0 из 0, и это единственный способ узнать, что механизм на этой
// установке вообще работает.
func reportUncertain(segs []core.Segment) {
	if marked, total := core.CountUncertain(segs); total > 0 {
		log.Printf(i18n.Tr("плохо расслышано слов: %d из %d (уверенность ниже %.2f)"),
			marked, total, core.UncertainBelow)
	}
}

// runPass — один вызов адаптера. extra — переменные окружения сверх обычных:
// словарь созвона в обоих видах или ничего.
func runPass(ctx context.Context, cfg *core.Config, audioPath string, extra []string) (pass, error) {
	args := make([]string, len(cfg.Transcribe.Cmd))
	for i, a := range cfg.Transcribe.Cmd {
		a = strings.ReplaceAll(a, "{{audio}}", audioPath)
		a = strings.ReplaceAll(a, "{{language}}", cfg.Transcribe.Language)
		args[i] = a
	}
	args[0] = core.AdapterPath(args[0])

	// Потолок — на один вызов адаптера: он сторожит зависший процесс, а не
	// сумму работы. Со словарём вызовов два, и это сказано в журнале.
	if d := cfg.Transcribe.Timeout.D(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	// nice: расшифровка не срочная и должна уступать интерактивной работе.
	if cfg.Transcribe.Nice {
		if _, err := exec.LookPath("nice"); err == nil {
			args = append([]string{"nice", "-n", "10"}, args...)
		}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Адаптер печатает диагностику человеку и потому говорит на языке steno.
	// Передаём всегда: язык мог прийти из конфига, а не из окружения, и сам
	// по себе до адаптера тогда не доедет.
	env := []string{"STENO_LANG=" + i18n.UILang}
	if n := cfg.Transcribe.Threads; n > 0 {
		// Адаптеры читают это и не занимают машину целиком.
		env = append(env, fmt.Sprintf("WHISPER_THREADS=%d", n))
	}
	env = append(env, extra...)
	cmd.Env = append(os.Environ(), env...)
	// По таймауту гасим всю группу процессов мягко, чтобы обёртка успела
	// убрать временный WAV, а whisper не остался сиротой.
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }
	cmd.WaitDelay = 15 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return pass{}, fmt.Errorf(i18n.Tr("адаптер расшифровки %v: %w\n%s"), args, err, i18n.Tail(stderr.String(), 800))
	}

	var out transcriptOut
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		return pass{}, fmt.Errorf(i18n.Tr("адаптер вернул не тот JSON: %w\n%s"), err, i18n.Tail(stdout.String(), 400))
	}
	p := pass{vocabulary: strings.TrimSpace(out.Vocabulary), notes: strings.TrimSpace(stderr.String())}
	for _, s := range out.Segments {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		seg := core.Segment{Start: s.Start, End: s.End, Text: txt,
			Speaker: strings.TrimSpace(s.Speaker), Conf: sane(s.Conf)}
		for _, w := range s.Words {
			word := strings.TrimSpace(w.Word)
			if word == "" {
				continue
			}
			seg.Words = append(seg.Words, core.Word{
				Word: word, Start: w.Start, End: w.End, Conf: sane(w.Conf)})
		}
		p.segs = append(p.segs, seg)
		p.endConf = append(p.endConf, sane(s.EndConf))
	}
	// Сортировка — парой: уверенность конца реплики едет вместе с репликой.
	idx := make([]int, len(p.segs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return p.segs[idx[i]].Start < p.segs[idx[j]].Start })
	segs := make([]core.Segment, len(idx))
	ends := make([]*float64, len(idx))
	for i, k := range idx {
		segs[i], ends[i] = p.segs[k], p.endConf[k]
	}
	p.segs, p.endConf = segs, ends
	return p, nil
}

func ReadUtterances(path string) ([]Utterance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Utterance
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var u Utterance
		if err := json.Unmarshal([]byte(line), &u); err != nil {
			continue // одна битая строка не повод терять весь файл
		}
		out = append(out, u)
	}
	return out, sc.Err()
}

// captionLag — субтитры Meet появляются позже самой речи: распознаванию нужно
// время. Сдвигаем их назад, иначе имена уезжают на следующего говорящего.
//
// У подсветки говорящего задержка своя и другая — она загорается почти сразу и
// гаснет с опозданием. Поэтому два источника нельзя сложить в одну кучу и
// сдвинуть общим числом: их приводят к времени речи по отдельности, здесь и в
// normalizeSpans, и только потом сравнивают.
const captionLag = 1.5

// nearestWindow — насколько далеко от сегмента ещё допустимо брать имя, если
// прямого пересечения нет. Дальше лучше без имени, чем с чужим.
const nearestWindow = 3.0

// minSolidOverlap — пересечение короче этого считаем касанием краёв, а не
// попаданием. Нужно ровно в одном месте: реплика субтитров, задевшая сегмент
// на пять сотых секунды краем, не должна перебивать подсветку, накрывшую тот же
// сегмент целиком.
const minSolidOverlap = 0.25

// namedSpan — отрезок с именем. Сюда одинаково ложатся и реплика субтитров, и
// подсветка говорящего: дальше их различает только то, кто в каком списке.
type namedSpan struct {
	speaker    string
	start, end float64
}

// spanFinder — окно, едущее по отсортированной ленте. Обе последовательности
// идут по времени, поэтому смотреть каждый отрезок для каждого сегмента
// незачем. На часовом созвоне это тысячи сегментов против тысяч отрезков.
type spanFinder struct {
	spans []namedSpan
	lo    int
}

// match — что лента дала для одного сегмента whisper. overlap > 0 означает
// настоящее пересечение, dist > 0 — что имя взято от ближайшего по времени
// отрезка, а не от накрывающего.
type match struct {
	speaker string
	overlap float64
	dist    float64
	ok      bool
}

func (f *spanFinder) find(s core.Segment) match {
	// Отрезки, кончившиеся до окна допуска, больше не понадобятся никогда:
	// сегменты дальше только позже.
	for f.lo < len(f.spans) && f.spans[f.lo].end < s.Start-nearestWindow {
		f.lo++
	}
	best, bestOverlap := -1, 0.0
	nearest, dist := -1, nearestWindow
	for j := f.lo; j < len(f.spans) && f.spans[j].start <= s.End+nearestWindow; j++ {
		u := f.spans[j]
		if ov := overlap(s.Start, s.End, u.start, u.end); ov > bestOverlap {
			best, bestOverlap = j, ov
		}
		if d := gap(s.Start, s.End, u.start, u.end); d < dist {
			nearest, dist = j, d
		}
	}
	if best >= 0 {
		return match{speaker: f.spans[best].speaker, overlap: bestOverlap, ok: true}
	}
	// Пересечения нет — берём ближайший по времени отрезок, но только если он
	// рядом: иначе лучше без имени, чем с чужим.
	if nearest >= 0 {
		return match{speaker: f.spans[nearest].speaker, dist: dist, ok: true}
	}
	return match{}
}

// solidOverlap — сколько пересечения довольно, чтобы считать попадание
// настоящим. У очень коротких сегментов порогом становится их половина: иначе
// реплика в четверть секунды не смогла бы попасть никуда.
func solidOverlap(s core.Segment) float64 {
	if half := (s.End - s.Start) / 2; half < minSolidOverlap {
		return half
	}
	return minSolidOverlap
}

// AlignSpeakers ставит имена на сегменты whisper: текст берём у whisper (он
// точнее), имя — у площадки.
//
// Источников имени два, и они складываются, а не заменяют друг друга.
//
// Субтитры точнее: строка субтитров появляется потому, что площадка распознала
// речь и привязала её к конкретному участнику, — это имя привязано к фразе.
// Поэтому субтитры остаются основным источником.
//
// Подсветка говорящего слабее: она реагирует на громкость, а не на слова.
// Её ловит кашель и стук по клавиатуре, при перекрёстных репликах она скачет,
// а границы у неё грубее — опрос идёт раз в полсекунды. Зато она не зависит от
// языка распознавания и работает там, где субтитров нет вовсе (публичный
// Jitsi). Поэтому подсветка — запасной источник там, где субтитры молчат.
//
// При разногласии на одном отрезке выигрывают субтитры. Единственное
// исключение — когда субтитры лишь задели сегмент краем (меньше
// solidOverlap), а подсветка накрыла его по-настоящему: тогда речь идёт не о
// разногласии двух мнений об одном отрезке, а о том, что реплика субтитров
// относится к соседнему куску разговора.
//
// Расхождения не заминаются, а считаются: если субтитры систематически
// разъезжаются с подсветкой, это видно строкой в логе, а не по чужим фамилиям
// в follow-up.
//
// Третий источник — сам движок расшифровки, если он умеет диаризацию. Он
// сильнее обоих наших: он слушает звук, а не разглядывает DOM, и границу между
// голосами ставит по голосу. Но он отвечает не на тот вопрос: диаризация даёт
// «A», «B», «speaker 1» — она различает говорящих, а не называет их. Имена
// есть только у площадки.
//
// Поэтому имя от движка и имя от площадки не спорят, а складываются, и
// складываются по-разному, смотря что именно движок сказал:
//
//   - Движок вернул имя (не похожее на метку диаризации) — оно и остаётся.
//     Затирать его догадкой по субтитрам нельзя ни при каких условиях: он знает
//     то, чего мы знать не можем.
//   - Движок вернул метку — метка становится ключом, а не именем. Все реплики
//     одной метки — это один голос; голоса за имя собираются по всем её
//     репликам сразу, и побеждает общее большинство. Так реплика, которую
//     субтитры задели чужим именем из-за задержки, получает имя своих
//     соседей по голосу, а не по времени. Это и есть весь выигрыш от
//     диаризации.
//   - Голоса внутри метки разошлись примерно поровну — значит движок слил двух
//     людей в один голос, и наследовать имя большинства нельзя: половина реплик
//     получит чужое. Такая метка разбирается по-старому, по каждой реплике
//     отдельно, то есть ровно так, как всё работало до диаризации. Хуже не
//     становится, и об этом говорится в логе.
//   - Метка есть, а голосов нет вовсе (субтитров нет, подсветки нет) — метка
//     остаётся именем. «A: …» в follow-up хуже, чем «Ануар: …», но заметно
//     лучше, чем все шесть задач на одного человека.
//
// Третий параметр вариативный не для красоты: main.go зовёт AlignSpeakers
// двумя аргументами, и над ним сейчас работают, — так подсветка включается
// правкой одной строки, а сборка не ломается ни на минуту.
func AlignSpeakers(segs []core.Segment, utts []Utterance, tiles ...SpeakerSpan) []core.Segment {
	caps := make([]namedSpan, 0, len(utts))
	for _, u := range utts {
		// Субтитры отстают от речи — сдвигаем их назад к тому времени, когда
		// слова были сказаны.
		caps = append(caps, namedSpan{u.Speaker, u.Start - captionLag, u.End - captionLag})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].start < caps[j].start })

	// Лента подсветки к времени речи приводится своим способом — в
	// normalizeSpans. Общим сдвигом их путать нельзя: у субтитров задержка
	// одна, у подсветки другая, и смешать их значит получить имена, съехавшие
	// ровно на эту разницу.
	norm := normalizeSpans(tiles)
	tls := make([]namedSpan, 0, len(norm))
	for _, s := range norm {
		tls = append(tls, namedSpan{s.Speaker, s.Start, s.End})
	}

	if len(caps) == 0 && len(tls) == 0 {
		// Имена взять негде. Всё, что было у реплик от движка, остаётся при
		// них: даже «A» и «B» разводят реплики по двум людям, а пустое имя не
		// разводит ни по чему.
		return segs
	}

	capFinder := &spanFinder{spans: caps}
	tileFinder := &spanFinder{spans: tls}
	var mix speakerMix

	// Первым проходом решаем про каждую реплику отдельно — это то самое
	// поведение, что было всегда. Вторым проходом метки диаризации, если они
	// есть, наследуют имя большинства своих реплик. Считаем только во втором:
	// иначе реплика, у которой имя в итоге взяли не отсюда, попадала бы в отчёт
	// дважды, и сумма по строке перестала бы сходиться с числом реплик.
	guess := make([]match, len(segs))
	fromTiles := make([]bool, len(segs))
	disagree := make([]bool, len(segs))
	for i, s := range segs {
		c := capFinder.find(s)
		t := tileFinder.find(s)
		solid := solidOverlap(s)
		capSolid := c.ok && c.overlap > 0 && c.overlap >= solid
		tileSolid := t.ok && t.overlap > 0 && t.overlap >= solid
		// Считается это во втором проходе, и только там, где имя в итоге взяли
		// отсюда: на реплике, названной движком, спор субтитров с подсветкой
		// ничего не решал, а строка отчёта обещает, что там взято имя из
		// субтитров.
		disagree[i] = capSolid && tileSolid && c.speaker != t.speaker
		switch {
		case capSolid:
			guess[i] = match{speaker: c.speaker, overlap: c.overlap, ok: true}
		case t.ok && t.overlap > 0:
			// Субтитры сюда не попали или только задели краем, а подсветка
			// накрыла — это её случай.
			guess[i] = match{speaker: t.speaker, overlap: t.overlap, ok: true}
			fromTiles[i] = true
		case c.ok:
			guess[i] = match{speaker: c.speaker, dist: c.dist, ok: true}
		case t.ok:
			guess[i] = match{speaker: t.speaker, dist: t.dist, ok: true}
			fromTiles[i] = true
		}
	}

	named := clusterNames(segs, guess, &mix)
	out := make([]core.Segment, len(segs))
	for i, s := range segs {
		out[i] = s
		switch {
		case s.Speaker != "" && !looksLikeSpeakerID(s.Speaker):
			// Движок вернул имя, а не метку. Он знает лучше — не трогаем.
			mix.FromEngine++
		case s.Speaker != "" && named[s.Speaker] != "":
			out[i].Speaker = named[s.Speaker]
			mix.FromClusters++
		case guess[i].ok:
			out[i].Speaker = guess[i].speaker
			if fromTiles[i] {
				mix.FromTiles++
			} else {
				mix.FromCaptions++
			}
			if disagree[i] {
				mix.Conflicts++
			}
		case s.Speaker != "":
			// Метка есть, имени для неё не нашлось — метка и остаётся именем.
			mix.FromEngine++
		default:
			mix.Unnamed++
		}
	}
	if len(tls) > 0 || mix.FromEngine > 0 || mix.FromClusters > 0 || mix.SplitClusters > 0 {
		log.Printf("%s", mix.Report())
	}
	return out
}

// speakerIDRe — метка диаризации, а не имя. Движок отвечает «A», «B», «0»,
// «speaker 1», «SPEAKER_00», «spk_2»; человека же зовут словом.
//
// Граница проведена узко нарочно. Всё, что не подошло под этот образец,
// считается именем и остаётся нетронутым: ошибиться в сторону «это имя» —
// значит один раз не воспользоваться диаризацией, ошибиться в другую —
// значит затереть настоящее имя, которое движок узнал, а мы нет. Поэтому
// голая буква считается меткой (людей с именем «A» не бывает), а «Ян» и
// «Bob» — именами, хотя они и короткие.
var speakerIDRe = regexp.MustCompile(`^(?i)(speaker|spk|s)?[ _-]?([a-z]|\d{1,3})$`)

func looksLikeSpeakerID(s string) bool {
	return speakerIDRe.MatchString(strings.TrimSpace(s))
}

// clusterNames — как зовут каждую метку диаризации.
//
// Голоса собираются по всем репликам метки сразу и взвешиваются пересечением:
// реплика, накрытая субтитрами на четыре секунды, весит вчетверо больше той,
// что задета на секунду. Реплики, для которых имени не нашлось вовсе, не
// голосуют — молчание не довод ни за кого.
//
// clusterMajority — во сколько раз победитель должен превзойти второго. Ровно
// вдвое: метка, где имена разошлись почти поровну, — это два человека, слитые
// диаризацией в один голос, и любое имя для неё будет чужим для половины
// реплик. Такая метка остаётся без общего имени, и её реплики разбираются
// по-старому.
func clusterNames(segs []core.Segment, guess []match, mix *speakerMix) map[string]string {
	const clusterMajority = 2.0
	votes := map[string]map[string]float64{}
	for i, s := range segs {
		if s.Speaker == "" || !looksLikeSpeakerID(s.Speaker) || !guess[i].ok {
			continue
		}
		w := guess[i].overlap
		if w <= 0 {
			// Имя взято у ближайшего по времени отрезка, а не у накрывающего.
			// Такой голос считается, но весит мало: чем дальше отрезок, тем
			// меньше это похоже на знание.
			w = 0.01 * (nearestWindow - guess[i].dist)
			if w <= 0 {
				continue
			}
		}
		if votes[s.Speaker] == nil {
			votes[s.Speaker] = map[string]float64{}
		}
		votes[s.Speaker][guess[i].speaker] += w
	}
	out := map[string]string{}
	for id, byName := range votes {
		best, bestW, secondW := "", 0.0, 0.0
		for name, w := range byName {
			switch {
			case w > bestW:
				best, bestW, secondW = name, w, bestW
			case w > secondW:
				secondW = w
			}
		}
		if bestW > 0 && bestW >= clusterMajority*secondW {
			out[id] = best
			continue
		}
		mix.SplitClusters++
	}
	return out
}

// speakerMix — откуда взялись имена. Нужен, чтобы человек видел, работает ли
// подсветка вообще и не разъезжается ли она с субтитрами: молча выбранный
// источник — это чужие фамилии в follow-up без единого следа в логе.
type speakerMix struct {
	FromCaptions int
	FromTiles    int
	Unnamed      int
	Conflicts    int

	// FromEngine — реплики, где имя пришло от самого движка расшифровки: он
	// вернул имя, а не метку, либо вернул метку, которую некому было назвать.
	// FromClusters — реплики, где метка движка получила имя от площадки.
	FromEngine   int
	FromClusters int
	// SplitClusters — метки, внутри которых имена разошлись почти поровну.
	// Каждая такая — это, скорее всего, два человека, слитые в один голос.
	SplitClusters int
}

func (m speakerMix) Report() string {
	msg := fmt.Sprintf(i18n.Tr("имена: %d от субтитров, %d от подсветки говорящего, %d без имени"),
		m.FromCaptions, m.FromTiles, m.Unnamed)
	if m.FromClusters > 0 || m.FromEngine > 0 {
		msg += fmt.Sprintf(i18n.Tr("; %d по диаризации движка, %d прямо от движка"),
			m.FromClusters, m.FromEngine)
	}
	if m.SplitClusters > 0 {
		msg += fmt.Sprintf(i18n.Tr("; у %d голосов имя не сошлось — там разбирали по каждой реплике"),
			m.SplitClusters)
	}
	if m.Conflicts > 0 {
		msg += fmt.Sprintf(i18n.Tr("; разошлись на %d сегментах — там взято имя из субтитров"), m.Conflicts)
	}
	return msg
}

// speakerTimelinePath — лента подсветки лежит рядом с субтитрами и пишется тем
// же ботом. Отдельным файлом, а не строчками в captions.jsonl: реплика
// субтитров и отрезок подсветки — разные вещи с разными задержками, и
// смешивать их в одном файле значит потом гадать, что было чем. Заодно
// «реплик в субтитрах» остаётся честным числом.
func speakerTimelinePath(captionsPath string) string {
	return filepath.Join(filepath.Dir(captionsPath), SpeakersFileName)
}

// SpeakersFileName — имя ленты. Одно на двоих: бот его пишет (meet_bot.go),
// расшифровка читает. Разъехавшиеся строковые литералы здесь означали бы, что
// лента пишется и молча никем не читается.
const SpeakersFileName = "speakers.jsonl"

// ReadSpeakerSpans читает ленту подсветки. Ошибку не возвращает намеренно:
// её отсутствие — обычное дело (старая запись, площадка без подсветки), и
// расшифровка от этого не должна падать.
func ReadSpeakerSpans(captionsPath string) []SpeakerSpan {
	f, err := os.Open(speakerTimelinePath(captionsPath))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []SpeakerSpan
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s SpeakerSpan
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue // одна битая строка не повод терять всю ленту
		}
		out = append(out, s)
	}
	return out
}

func overlap(a1, a2, b1, b2 float64) float64 {
	lo, hi := max(a1, b1), min(a2, b2)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func gap(a1, a2, b1, b2 float64) float64 {
	if b1 > a2 {
		return b1 - a2
	}
	if a1 > b2 {
		return a1 - b2
	}
	return 0
}

// Пометки, которыми движки расшифровки обозначают отсутствие речи. Они не
// текст, и принимать их за расшифровку нельзя.
var noSpeechMarkers = regexp.MustCompile(`(?i)^[\[\(](blank_?audio|silence|music|музыка|тишина|inaudible|неразборчиво|звук[^\]\)]*)[\]\)]$`)

// CheckTranscript ловит вырожденную расшифровку до того, как за неё заплатят.
//
// Настоящий случай: whisper определяет язык по первым тридцати секундам, а
// запись созвона начинается с тишины — бот заходит раньше людей. Определился
// не тот язык, и весь разговор вышел пятнадцатью строками [BLANK_AUDIO].
// Формально это валидный JSON и нулевой код возврата: без проверки сервис
// отдал бы это Claude и выдал пустой follow-up как настоящий.
func CheckTranscript(segs []core.Segment) error {
	const minChars = 80
	chars, real := 0, 0
	for _, s := range segs {
		t := strings.TrimSpace(s.Text)
		if t == "" || noSpeechMarkers.MatchString(t) {
			continue
		}
		real++
		chars += len([]rune(t))
	}
	if real == 0 {
		return fmt.Errorf(i18n.Tr("в расшифровке нет речи — %d строк, и все пустые или «нет звука». ")+
			i18n.Tr("Обычно это значит, что определился не тот язык или запись почти вся тишина"), len(segs))
	}
	if chars < minChars {
		return fmt.Errorf(i18n.Tr("расшифровка почти пустая: %d значимых строк, %d символов. ")+
			i18n.Tr("Проверь запись и язык распознавания"), real, chars)
	}
	return nil
}
