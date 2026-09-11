package audio

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Два прохода по словарю — на записанных выходах whisper.
//
// В testdata/whisper лежат настоящие ответы whisper-cli (-oj -ojf) на две
// записи владельца: без подсказки, с подсказкой-фразой и с подсказкой-списком.
// Здесь их отдаёт поддельный whisper-cli, а всё остальное настоящее: адаптер
// adapters/whisper-cpp.sh с его jq, RunTranscriber, слияние. Так проверяется
// весь путь от токенов whisper до реплик, кроме самого распознавания — и
// ровно тот результат, что получен на живых прогонах: Ануар и Сапар починены,
// «релиз», «послезавтра» и «SDK» вернулись, тринадцать границ из тринадцати на
// месте, ни одно уверенное слово не тронуто.
//
// Живой прогон на тех же записях — twopass_live_test.go (тег live).

// fakeWhisper — конфиг с настоящим адаптером whisper-cpp.sh поверх поддельного
// whisper-cli, который вместо распознавания отдаёт записанный ответ: plain —
// на вызов без --prompt, hinted — с ним. Возвращает ещё путь к аудио (секунда
// тишины — адаптеру нужен настоящий файл для ffmpeg) и путь к журналу
// вызовов: по нему видно, сколько раз и с чем звали whisper.
func fakeWhisper(t *testing.T, plain, hinted string) (cfg *core.Config, wav, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("скрипты на bash — не для Windows")
	}
	for _, bin := range []string{"bash", "jq", "ffmpeg"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("для адаптера нужен %s", bin)
		}
	}
	adapter, err := filepath.Abs(filepath.Join("..", "..", "adapters", "whisper-cpp.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls.log")
	cli := filepath.Join(dir, "whisper-cli")
	mustWriteFile(t, cli, `#!/usr/bin/env bash
of=""; prompted=0; detect=0
args=("$@")
for ((i=0; i<${#args[@]}; i++)); do
  case "${args[$i]}" in
    -of) of="${args[$((i+1))]}" ;;
    --prompt) prompted=1 ;;
    -dl) detect=1 ;;
  esac
done
printf '%s\n' "$*" >> "$FAKE_WHISPER_LOG"
if [ "$detect" = 1 ]; then echo "auto-detected language: ru (p = 0.99)"; exit 0; fi
src="$FAKE_WHISPER_PLAIN"; [ "$prompted" = 1 ] && src="$FAKE_WHISPER_HINTED"
cp "$src" "$of.json"
`)
	if err := os.Chmod(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(dir, "ggml-fake.bin")
	mustWriteFile(t, model, "")
	fixture := func(name string) string {
		p, err := filepath.Abs(filepath.Join("testdata", "whisper", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Setenv("WHISPER_BIN", cli)
	t.Setenv("WHISPER_MODEL", model)
	t.Setenv("WHISPER_MODEL_DIR", dir) // без VAD-модели: адаптер только предупредит
	t.Setenv("FAKE_WHISPER_PLAIN", fixture(plain))
	t.Setenv("FAKE_WHISPER_HINTED", fixture(hinted))
	t.Setenv("FAKE_WHISPER_LOG", calls)

	wav = filepath.Join(dir, "a.wav")
	mustWriteFile(t, wav, string(SilentWAV(time.Second)))
	cfg = core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{adapter, "{{audio}}", "{{language}}"}
	cfg.Transcribe.Language = "ru"
	cfg.Transcribe.Nice = false
	cfg.Transcribe.Timeout = core.Duration(60 * time.Second)
	return cfg, wav, calls
}

// captureLog — что RunTranscriber написал в журнал.
func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	prev, flags := log.Writer(), log.Flags()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &out
}

// measuredVocab — словарь, с которым мерилось: два человека и один сервис.
// Ровно он даёт фразу, на которой получен записанный ответ acaf-phrase.
func measuredVocab() *Vocab {
	v := &Vocab{Lang: "ru"}
	v.source()
	v.addPeople("Ануар", "Рустем")
	v.addTerms("Сапар")
	return v
}

// measuredPrompt — фраза, на которой сделан записанный прогон с подсказкой.
// Байт в байт: форма подсказки измерена, и любая правка каркаса — это новый
// замер, а не косметика.
const measuredPrompt = "Созвон команды разработки. Участвуют Ануар и Рустем. Обсуждают Сапар."

func texts(segs []core.Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

func TestTwoPassRepairsRecordedCalls(t *testing.T) {
	for _, c := range []struct {
		name          string
		plain, hinted string
		want          []string
		taken         int
	}{{
		name: "acaf", plain: "acaf-plain", hinted: "acaf-phrase", taken: 3,
		want: []string{
			"Так, проверка, проверка.",
			"Да, да, тоже будем проверять.",
			"Сейчас проверка.",
			"Ануар, нужно закончить Сапар.", // было «Анвару нужно закончить сапар,»
			"разобраться, что с ним делать,",
			"что случилось,",
			"разобрать на синхронные запросы.",
			"Он должен написать и проверить по конкурсам,",
			"чтобы участники нормальные и пешные могли участвовать.", // «пешные» 0.84 — не трогать
			"Сегодня надо проверить МДС,",
			"корректно ли работает.",
			"И с V-Live надо тоже посмотреть,", // «VLive» 0.42: форму берём у прохода со словарём
			"созвон будет, что они скажут и как это все будет.",
		},
	}, {
		name: "b9df", plain: "b9df-plain", hinted: "b9df-phrase", taken: 7,
		want: []string{
			// было «… транскрипт так нужно выпустили завтра и после нужно сделать»
			"проверка проверка 1 2 3 1 2 3 Транскрипт. Так, нужно выпустить релиз завтра. и после завтра нужно сделать SDK.",
		},
	}} {
		t.Run(c.name, func(t *testing.T) {
			cfg, wav, calls := fakeWhisper(t, c.plain, c.hinted)
			logged := captureLog(t)
			plain, _, err := RunTranscriber(context.Background(), cfg, wav, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := RunTranscriber(context.Background(), cfg, wav, measuredVocab())
			if err != nil {
				t.Fatal(err)
			}

			// Дословно.
			if g, w := strings.Join(texts(got), "\n"), strings.Join(c.want, "\n"); g != w {
				t.Errorf("расшифровка после слияния:\n%s\nожидали:\n%s", g, w)
			}
			// Границы — все из прохода без словаря.
			if len(got) != len(plain) {
				t.Fatalf("реплик %d, а в проходе без словаря %d", len(got), len(plain))
			}
			for i := range got {
				if math.Abs(got[i].Start-plain[i].Start) > 0.001 || math.Abs(got[i].End-plain[i].End) > 0.001 {
					t.Errorf("реплика %d: границы %.2f..%.2f, а без словаря %.2f..%.2f",
						i, got[i].Start, got[i].End, plain[i].Start, plain[i].End)
				}
			}
			// Ни одно уверенное слово не тронуто: всё, в чём проход без словаря
			// уверен, стоит в той же реплике в том же порядке, буква в букву.
			for i := range plain {
				var sure []string
				for _, w := range plain[i].Words {
					if w.Conf != nil && *w.Conf >= mergeBelow {
						sure = append(sure, w.Word)
					}
				}
				k := 0
				for _, w := range got[i].Words {
					if k < len(sure) && w.Word == sure[k] {
						k++
					}
				}
				if k != len(sure) {
					t.Errorf("реплика %d: уверенное слово %q тронуто: %q", i, sure[k], got[i].Text)
				}
			}
			// Whisper звали дважды: с подсказкой-фразой и без.
			raw, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			// Первые вызовы — проход без словаря для plain выше.
			var withPrompt, without int
			for _, l := range lines {
				if strings.Contains(l, "--prompt "+measuredPrompt+" --carry-initial-prompt") {
					withPrompt++
				} else if !strings.Contains(l, "--prompt") {
					without++
				} else {
					t.Errorf("whisper позвали с другой подсказкой: %s", l)
				}
			}
			if withPrompt != 1 || without != 2 {
				t.Errorf("вызовов whisper с подсказкой %d, без %d (ожидали 1 и 2):\n%s", withPrompt, without, raw)
			}
			// Человек видит в журнале, за что заплатил.
			if !strings.Contains(logged.String(), "второй проход без словаря занял") ||
				!strings.Contains(logged.String(), "взято "+strconv.Itoa(c.taken)+" слов") {
				t.Errorf("в журнале нет строки о втором проходе и взятых словах:\n%s", logged.String())
			}
		})
	}
}

// Форма подсказки: фраза держит границы реплик, список — рушит. Те же три
// слова, две записанные расшифровки: с фразой начала десяти реплик из
// тринадцати стоят там же, где без подсказки (±0.3 с), со списком — трёх.
// И Vocab.Prompt обязан давать ровно ту фразу, на которой это измерено.
func TestPromptPhraseKeepsBoundaries(t *testing.T) {
	starts := func(name string) []float64 {
		raw, err := os.ReadFile(filepath.Join("testdata", "whisper", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Transcription []struct {
				Offsets struct{ From, To float64 }
			}
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		var out []float64
		for _, s := range d.Transcription {
			out = append(out, s.Offsets.From/1000)
		}
		return out
	}
	kept := func(a, x []float64) int {
		n := 0
		for _, s := range a {
			for _, y := range x {
				if math.Abs(s-y) <= 0.3 {
					n++
					break
				}
			}
		}
		return n
	}
	plain := starts("acaf-plain")
	if len(plain) != 13 {
		t.Fatalf("в записи без подсказки %d реплик, ожидали 13", len(plain))
	}
	phrase, list := kept(plain, starts("acaf-phrase")), kept(plain, starts("acaf-list"))
	if phrase < 7 || list > 3 || phrase <= list {
		t.Errorf("границ на месте: с фразой %d/13, со списком %d/13 — замер был 7 и 3", phrase, list)
	}
	if got := measuredVocab().Prompt(promptBudget); got != measuredPrompt {
		t.Errorf("подсказка не та, на которой мерили границы:\n     %q\nнужно %q", got, measuredPrompt)
	}
}

// Заменить или удалить слова первого прохода можно, только если ВСЕ затронутые
// слова неуверенные. Без этого правила первая версия слияния удалила две
// верные реплики: whisper со словарём переписал их иначе, а одно слово в них
// было ниже порога и утянуло уверенных соседей.
func TestMergeNeedsEveryTouchedWordUnsure(t *testing.T) {
	conf := func(c float64) *float64 { return &c }
	mk := func(words ...core.Word) pass {
		return pass{segs: []core.Segment{{Start: 1, End: 3, Text: joinWords(words), Words: words}},
			endConf: []*float64{conf(0.9)}}
	}
	hinted := mk(core.Word{Word: "нужно"}, core.Word{Word: "выпустить"}, core.Word{Word: "релиз"})

	// «надо» уверенное — замена всего куска запрещена, реплика остаётся как была.
	plain := mk(core.Word{Word: "надо", Conf: conf(0.95)}, core.Word{Word: "выпустили", Conf: conf(0.05)})
	got, taken := mergePasses(plain, hinted)
	if got[0].Text != "надо выпустили" || taken != 0 {
		t.Errorf("уверенное слово утянуто за неуверенным соседом: %q (взято %d)", got[0].Text, taken)
	}
	// Оба неуверенные — та же замена разрешена.
	plain = mk(core.Word{Word: "надо", Conf: conf(0.3)}, core.Word{Word: "выпустили", Conf: conf(0.05)})
	got, taken = mergePasses(plain, hinted)
	if got[0].Text != "нужно выпустить релиз" || taken != 3 {
		t.Errorf("два неуверенных слова не заменены: %q (взято %d)", got[0].Text, taken)
	}
	// Без уверенности вовсе — трогать нельзя: «не сказал» не значит «не уверен».
	plain = mk(core.Word{Word: "надо"}, core.Word{Word: "выпустили"})
	if got, _ := mergePasses(plain, hinted); got[0].Text != "надо выпустили" {
		t.Errorf("слова без уверенности заменены: %q", got[0].Text)
	}
}

// Порог слияния — отдельная ручка от порога пометки, и стоит выше него:
// обрывок «после» из «послезавтра» имеет 0.524. При 0.5 «завтра» не вернулось
// бы.
func TestMergeThresholdSitsAboveUncertainMark(t *testing.T) {
	if mergeBelow <= core.UncertainBelow {
		t.Fatalf("порог слияния %.2f не выше порога пометки %.2f", mergeBelow, core.UncertainBelow)
	}
	conf := func(c float64) *float64 { return &c }
	plain := pass{segs: []core.Segment{{Start: 16, End: 23, Text: "и после нужно", Words: []core.Word{
		{Word: "и", Conf: conf(0.798)}, {Word: "после", Conf: conf(0.524)}, {Word: "нужно", Conf: conf(0.501)},
	}}}, endConf: []*float64{conf(0.9)}}
	hinted := pass{segs: []core.Segment{{Start: 16, End: 23, Text: "И после завтра нужно", Words: []core.Word{
		{Word: "И"}, {Word: "после"}, {Word: "завтра"}, {Word: "нужно"},
	}}}}
	got, _ := mergePasses(plain, hinted)
	if got[0].Text != "и после завтра нужно" {
		t.Errorf("вставка рядом со словом на 0.524 не прошла: %q", got[0].Text)
	}
	// «и» на 0.798 остаётся строчным: его форму менять нельзя.
	if got[0].Words[0].Word != "и" {
		t.Errorf("уверенное «и» перезаписано: %q", got[0].Words[0].Word)
	}
}

// Реплика, оборванная не договорив: последнее слово уверенное, соседей справа
// нет — и всё же вставить после него можно, если движок не уверен, что реплика
// кончилась. Так вернулось «SDK» (закрывающий токен 0.034).
func TestMergeTrustsUnsureSegmentEnd(t *testing.T) {
	conf := func(c float64) *float64 { return &c }
	mk := func(end float64) pass {
		return pass{segs: []core.Segment{{Start: 20, End: 23, Text: "нужно сделать", Words: []core.Word{
			{Word: "нужно", Conf: conf(0.9)}, {Word: "сделать", Conf: conf(0.887)},
		}}}, endConf: []*float64{conf(end)}}
	}
	hinted := pass{segs: []core.Segment{{Start: 20, End: 25, Text: "нужно сделать SDK.", Words: []core.Word{
		{Word: "нужно"}, {Word: "сделать"}, {Word: "SDK."},
	}}}}
	if got, _ := mergePasses(mk(0.034), hinted); got[0].Text != "нужно сделать SDK." {
		t.Errorf("хвост оборванной реплики не дописан: %q", got[0].Text)
	}
	// Обычный неуверенный конец (0.3 — таких одиннадцать из тринадцати) —
	// не повод: иначе вставки шли бы почти в каждую реплику.
	if got, _ := mergePasses(mk(0.3), hinted); got[0].Text != "нужно сделать" {
		t.Errorf("вставка после обычного конца реплики: %q", got[0].Text)
	}
	// Движок не сказал — тоже не повод.
	unknown := mk(0.5)
	unknown.endConf[0] = nil
	if got, _ := mergePasses(unknown, hinted); got[0].Text != "нужно сделать" {
		t.Errorf("вставка без уверенности в конце реплики: %q", got[0].Text)
	}
}

// Нужен ли второй проход, говорит адаптер: "vocabulary":"keyterms" — словарь
// ушёл отдельным списком, разбивка цела, второй проход только удвоил бы счёт.
// Адаптер, который ничего не сказал, получает два прохода.
func TestAdapterDecidesSecondPass(t *testing.T) {
	quiet(t)
	run := func(t *testing.T, body string, vocab *Vocab) []string {
		t.Helper()
		dir := t.TempDir()
		script := filepath.Join(dir, "adapter.sh")
		seen := filepath.Join(dir, "seen")
		mustWriteFile(t, script, "#!/bin/sh\n"+
			"printf 'prompt=%s terms=%s\\n' \"${STENO_PROMPT-нет}\" \"${STENO_TERMS-нет}\" >> "+seen+"\n"+
			"cat <<'JSON'\n"+body+"\nJSON\n")
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := core.DefaultConfig()
		cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
		cfg.Transcribe.Nice = false
		if _, _, err := RunTranscriber(context.Background(), cfg, "a.ogg", vocab); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(seen)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
	const segs = `"segments":[{"start":0,"end":2,"text":"Анвару нужно","words":[{"word":"Анвару","confidence":0.3},{"word":"нужно","confidence":0.9}]}]`
	v := measuredVocab()
	prompt := v.Prompt(promptBudget)

	if calls := run(t, `{"vocabulary":"keyterms",`+segs+`}`, v); len(calls) != 1 ||
		!strings.HasPrefix(calls[0], "prompt="+prompt+" terms=Ануар, Рустем, Сапар") {
		t.Errorf("адаптер с keyterms: ожидали один вызов со словарём, получили %q", calls)
	}
	if calls := run(t, `{`+segs+`}`, v); len(calls) != 2 ||
		!strings.HasPrefix(calls[0], "prompt="+prompt) || calls[1] != "prompt=нет terms=нет" {
		t.Errorf("адаптер без признака: ожидали проход со словарём и проход без, получили %q", calls)
	}
	if calls := run(t, `{"vocabulary":"prompt",`+segs+`}`, v); len(calls) != 2 {
		t.Errorf("адаптер с prompt: ожидали два вызова, получили %q", calls)
	}
	if calls := run(t, `{`+segs+`}`, nil); len(calls) != 1 || calls[0] != "prompt=нет terms=нет" {
		t.Errorf("без словаря: ожидали один вызов без переменных, получили %q", calls)
	}
}

// Движок без уверенности по словам: слить проходы не по чему, и остаётся то,
// что было до второго прохода, — расшифровка со словарём. Молча — нельзя.
func TestTwoPassKeepsHintedWithoutWordConfidence(t *testing.T) {
	logged := captureLog(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "adapter.sh")
	mustWriteFile(t, script, "#!/bin/sh\n"+
		"if [ -n \"${STENO_PROMPT:-}\" ]; then echo '{\"segments\":[{\"start\":0,\"end\":2,\"text\":\"Ануар нужно\"}]}';"+
		" else echo '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"Анвару\"},{\"start\":1,\"end\":2,\"text\":\"нужно\"}]}'; fi\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
	cfg.Transcribe.Nice = false
	got, _, err := RunTranscriber(context.Background(), cfg, "a.ogg", measuredVocab())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "Ануар нужно" {
		t.Errorf("без уверенности по словам ожидали расшифровку со словарём, получили %+v", got)
	}
	if !strings.Contains(logged.String(), "не даёт уверенности по словам") {
		t.Errorf("о невозможности слияния промолчали:\n%s", logged.String())
	}
}
