//go:build live

package audio

// Живой прогон двух проходов на настоящих записях владельца. Обычные прогоны
// его не видят: тег live, и нужен whisper-cli с моделью large-v3 в
// ~/.cache/whisper.
//
//	WHISPER_THREADS=2 go test -tags live -run TestLiveTwoPass -v -timeout 30m ./internal/audio/
//
// STENO_LIVE_RECORDINGS — каталог с записями (по умолчанию
// ~/steno/data/recordings). Записи только читаются; база и сервис не
// трогаются: словарь здесь собирается из конфига в памяти, а не из базы.
//
// Ожидания — дословно те, что получены при замере (merge.py на 63 прогонах):
//
//	acaf: «Анвару нужно закончить сапар,»            → «Ануар, нужно закончить Сапар.»
//	      «чтобы участники нормальные и пешные …»     → без изменений
//	b9df: «… нужно выпустили завтра и после нужно сделать»
//	      → «… нужно выпустить релиз завтра. и после завтра нужно сделать SDK.»
//
// и 13 из 13 границ acaf на месте, ни одно слово с уверенностью ≥ 0.6 не
// тронуто.

import (
	"context"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

func TestLiveTwoPass(t *testing.T) {
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		t.Skip("нет whisper-cli")
	}
	home, _ := os.UserHomeDir()
	dir := os.Getenv("STENO_LIVE_RECORDINGS")
	if dir == "" {
		dir = filepath.Join(home, "steno", "data", "recordings")
	}
	adapter, err := filepath.Abs(filepath.Join("..", "..", "adapters", "whisper-cpp.sh"))
	if err != nil {
		t.Fatal(err)
	}
	log.SetFlags(log.Ltime)

	// Конфиг — как у владельца: язык автоопределением, два потока, словарь
	// включён. Проект — с теми словами, на которых распознавание ошиблось.
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Transcribe.Cmd = []string{adapter, "{{audio}}", "{{language}}"}
	cfg.Transcribe.Language = ""
	cfg.Transcribe.Threads = 2
	cfg.Transcribe.Nice = true
	cfg.Transcribe.Timeout = core.Duration(20 * time.Minute)
	cfg.Projects = []core.Project{{
		Name:       "taxi-kolesa",
		Aliases:    []string{"такси"},
		People:     []string{"Ануар", "Рустем"},
		Vocabulary: []string{"Сапар", "Ануар", "Рустем"},
	}}

	for _, c := range []struct {
		id    string
		want  []string // дословно после слияния
		check func(t *testing.T, plain, got []core.Segment)
	}{{
		id: "2026-09-10-1027-acaf",
		want: []string{
			"Так, проверка, проверка.",
			"Да, да, тоже будем проверять.",
			"Сейчас проверка.",
			"Ануар, нужно закончить Сапар.",
			"разобраться, что с ним делать,",
			"что случилось,",
			"разобрать на синхронные запросы.",
			"Он должен написать и проверить по конкурсам,",
			"чтобы участники нормальные и пешные могли участвовать.",
			"Сегодня надо проверить МДС,",
			"корректно ли работает.",
			"И с V-Live надо тоже посмотреть,",
			"созвон будет, что они скажут и как это все будет.",
		},
	}, {
		id: "2026-09-09-1852-b9df",
		want: []string{
			"проверка проверка 1 2 3 1 2 3 Транскрипт. Так, нужно выпустить релиз завтра. и после завтра нужно сделать SDK.",
		},
	}} {
		t.Run(c.id, func(t *testing.T) {
			audio := filepath.Join(dir, c.id, "audio.ogg")
			if _, err := os.Stat(audio); err != nil {
				t.Skipf("нет записи %s", audio)
			}
			m := &core.Meeting{ID: c.id, AudioPath: audio, Participants: []string{"Rustem Turgeldin"}}
			vocab := MeetingVocabulary(context.Background(), cfg, nil, m)
			if vocab == nil {
				t.Fatal("словарь не собрался")
			}
			t.Logf("подсказка: %q", vocab.Prompt(promptBudget))

			t0 := time.Now()
			plain, _, err := RunTranscriber(context.Background(), cfg, audio, nil)
			if err != nil {
				t.Fatal(err)
			}
			onePass := time.Since(t0)
			t0 = time.Now()
			got, notes, err := RunTranscriber(context.Background(), cfg, audio, vocab)
			if err != nil {
				t.Fatal(err)
			}
			twoPass := time.Since(t0)
			t.Logf("один проход: %s; со словарём (два прохода): %s", onePass.Round(time.Second), twoPass.Round(time.Second))
			t.Logf("адаптер:\n%s", notes)

			t.Logf("ДО (без словаря):\n%s", strings.Join(texts(plain), "\n"))
			t.Logf("ПОСЛЕ (слияние):\n%s", strings.Join(texts(got), "\n"))
			if g, w := strings.Join(texts(got), "\n"), strings.Join(c.want, "\n"); g != w {
				t.Errorf("расшифровка после слияния:\n%s\nожидали:\n%s", g, w)
			}
			if len(got) != len(plain) {
				t.Fatalf("реплик %d, а без словаря %d", len(got), len(plain))
			}
			kept := 0
			for i := range got {
				if math.Abs(got[i].Start-plain[i].Start) <= 0.3 && math.Abs(got[i].End-plain[i].End) <= 0.3 {
					kept++
				}
			}
			t.Logf("границ на месте: %d из %d", kept, len(plain))
			if kept != len(plain) {
				t.Errorf("границы разъехались: %d из %d", kept, len(plain))
			}
			touched := 0
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
				touched += len(sure) - k
			}
			t.Logf("тронуто уверенных слов: %d", touched)
			if touched != 0 {
				t.Errorf("тронуто уверенных слов: %d", touched)
			}
		})
	}
}
