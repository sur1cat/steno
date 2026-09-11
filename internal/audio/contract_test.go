package audio

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// fakeAdapter — адаптер расшифровки, который печатает заданный JSON. Договор с
// адаптером — это stdout, и проверять его надо через настоящий процесс: разбор
// строки в тесте доказал бы только то, что encoding/json работает.
func fakeAdapter(t *testing.T, out string) *core.Config {
	t.Helper()
	script := filepath.Join(t.TempDir(), "adapter.sh")
	mustWriteFile(t, script, "#!/bin/sh\ncat <<'JSON'\n"+out+"\nJSON\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
	cfg.Transcribe.Nice = false
	return cfg
}

func run(t *testing.T, out string) []core.Segment {
	t.Helper()
	quiet(t)
	segs, _, err := RunTranscriber(context.Background(), fakeAdapter(t, out), "x.ogg", nil)
	if err != nil {
		t.Fatal(err)
	}
	return segs
}

// Адаптер, написанный до появления уверенности, обязан работать без единой
// правки — и получать «не знаю», а не ноль. Ноль означал бы, что вся его
// расшифровка сомнительная, то есть договор сломал бы ровно тех, кто ничего не
// менял.
func TestOldContractStillWorks(t *testing.T) {
	segs := run(t, `{"segments":[{"start":1.2,"end":3.4,"text":"давайте начнём с релиза"}]}`)
	if len(segs) != 1 {
		t.Fatalf("реплик %d", len(segs))
	}
	s := segs[0]
	if s.Start != 1.2 || s.End != 3.4 || s.Text != "давайте начнём с релиза" {
		t.Errorf("старые поля разъехались: %+v", s)
	}
	if s.Speaker != "" || s.Conf != nil || s.Words != nil {
		t.Errorf("из ничего родились новые поля: %+v", s)
	}
	if core.Uncertain(s.Conf) {
		t.Error("молчание адаптера принято за низкую уверенность")
	}
}

// Полный договор: говорящий, уверенность реплики и уверенность каждого слова.
func TestNewContractIsRead(t *testing.T) {
	segs := run(t, `{"segments":[{
	  "start":21.76,"end":23.66,"text":"Анвару нужно закончить сапар",
	  "speaker":"A","confidence":0.615,
	  "words":[
	    {"word":"Анвару","start":21.76,"end":22.1,"confidence":0.334},
	    {"word":"нужно","start":22.1,"end":22.4,"confidence":0.863},
	    {"word":"закончить","start":22.4,"end":23.0,"confidence":0.989},
	    {"word":"сапар","start":23.1,"end":23.66,"confidence":0.276}]}]}`)
	if len(segs) != 1 {
		t.Fatalf("реплик %d", len(segs))
	}
	s := segs[0]
	if s.Speaker != "A" {
		t.Errorf("говорящий: %q", s.Speaker)
	}
	if s.Conf == nil || *s.Conf != 0.615 {
		t.Errorf("уверенность реплики: %v", s.Conf)
	}
	if len(s.Words) != 4 {
		t.Fatalf("слов %d, ожидали 4", len(s.Words))
	}
	if s.Words[0].Word != "Анвару" || s.Words[0].Conf == nil || *s.Words[0].Conf != 0.334 {
		t.Errorf("первое слово: %+v", s.Words[0])
	}
	if s.Words[0].Start != 21.76 || s.Words[0].End != 22.1 {
		t.Errorf("времена первого слова: %+v", s.Words[0])
	}
	// То, ради чего всё: ошибка распознавания доезжает до модели помеченной.
	want := "Анвару" + core.UncertainMark + " нужно закончить сапар" + core.UncertainMark
	if got := core.MarkUncertain(s); got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
}

// Движки меряют уверенность по-разному, и кто-то отдаёт логарифм правдоподобия:
// -0.7 вместо 0.5. Принять его за уверенность значит объявить всю расшифровку
// мусором; принять 42 — наоборот, промолчать обо всём. Число вне 0..1 — это «мы
// не поняли, что нам сказали», и честный ответ на него nil.
func TestImpossibleConfidenceBecomesUnknown(t *testing.T) {
	segs := run(t, `{"segments":[{"start":0,"end":2,"text":"раз два три","confidence":-0.7,
	  "words":[{"word":"раз","confidence":-0.7},{"word":"два","confidence":42},
	           {"word":"три","confidence":0.9}]}]}`)
	s := segs[0]
	if s.Conf != nil {
		t.Errorf("логарифм принят за уверенность: %v", *s.Conf)
	}
	if s.Words[0].Conf != nil || s.Words[1].Conf != nil {
		t.Errorf("невозможная уверенность у слов: %+v", s.Words)
	}
	if s.Words[2].Conf == nil || *s.Words[2].Conf != 0.9 {
		t.Errorf("нормальную уверенность потеряли вместе с мусорной: %+v", s.Words[2])
	}
	if got := core.MarkUncertain(s); got != "раз два три" {
		t.Errorf("на мусорной уверенности расставили пометки: %q", got)
	}
}

// Слова без текста в расшифровку не идут: пустое слово ничего не помечает, а в
// счётчике «плохо расслышано N из M» делает M неверным.
func TestEmptyWordsAreDropped(t *testing.T) {
	segs := run(t, `{"segments":[{"start":0,"end":2,"text":"тест",
	  "words":[{"word":"  ","confidence":0.1},{"word":"тест","confidence":0.9}]}]}`)
	if len(segs[0].Words) != 1 || segs[0].Words[0].Word != "тест" {
		t.Fatalf("слова: %+v", segs[0].Words)
	}
}

// Уверенность обязана быть видна человеку строкой в логе. Молча посчитанная,
// она ничем не отличается от несчитанной: движок без уверенности даёт 0 из 0, и
// это единственный способ узнать, что весь этот механизм на его установке
// вообще не работает.
func TestUncertainWordsAreCounted(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	if _, _, err := RunTranscriber(context.Background(), fakeAdapter(t,
		`{"segments":[{"start":0,"end":2,"text":"Анвару нужно закончить сапар",
		 "words":[{"word":"Анвару","confidence":0.334},{"word":"нужно","confidence":0.863},
		          {"word":"закончить","confidence":0.989},{"word":"сапар","confidence":0.276}]}]}`),
		"x.ogg", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "плохо расслышано слов: 2 из 4") {
		t.Fatalf("о плохо расслышанных словах промолчали: %q", out.String())
	}

	out.Reset()
	if _, _, err := RunTranscriber(context.Background(), fakeAdapter(t,
		`{"segments":[{"start":0,"end":2,"text":"давайте начнём"}]}`), "x.ogg", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "плохо расслышано") {
		t.Errorf("движок без уверенности, а мы о ней рассуждаем: %q", out.String())
	}
}
