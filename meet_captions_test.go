package main

// Включение субтитров. Проверяется на поддельной странице, а не в браузере:
// Chrome тут не поднимается — ни в headless, ни тем более в headful.
//
// Чинится здесь ровно одно сообщение: «субтитры включить не удалось». На живом
// созвоне оно было ложью — субтитры работали, имя говорящего снялось верно, а
// в конце того же лога стояло «субтитры сняты, имена говорящих есть». Прежний
// код печатал приговор сразу после последнего нажатия, ни разу больше не
// заглянув на страницу.

import (
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

// fakePage — страница с рисованным временем. Ждать по-настоящему нельзя:
// пятнадцать секунд на тест превратили бы go test в чай.
type fakePage struct {
	now time.Duration
	// appearAfter — сколько площадка рисует область субтитров после того, как
	// их включили. На живом созвоне Meet взял три секунды.
	appearAfter time.Duration
	// ignorePresses — сколько первых нажатий площадка проглатывает.
	ignorePresses int
	unavailable   bool
	toggleErr     error
	// pollFails — сколько первых опросов не отвечают вовсе.
	pollFails int

	on      bool // включены ли субтитры по мнению площадки
	onSince time.Duration
	presses int
	polls   int
}

func (f *fakePage) page() captionPage {
	return captionPage{
		state: func() (bool, bool, bool) {
			f.polls++
			if f.polls <= f.pollFails {
				return false, false, false
			}
			return f.on && f.now >= f.onSince, f.unavailable, true
		},
		toggle: func() error {
			if f.toggleErr != nil {
				return f.toggleErr
			}
			f.presses++
			if f.presses <= f.ignorePresses {
				return nil
			}
			f.on = !f.on
			if f.on {
				f.onSince = f.now + f.appearAfter
			}
			return nil
		},
		wait: func(d time.Duration) { f.now += d },
	}
}

func captureLog() (*log.Logger, *strings.Builder) {
	var b strings.Builder
	return log.New(&b, "", 0), &b
}

// Главный случай с живого созвона: Meet рисует область субтитров через три
// секунды после нажатия. Прежний код ждал полторы, объявлял поломку и жал
// «c» ещё четыре раза — то есть с одинаковой вероятностью мог их и выключить.
func TestCaptionsAppearingLateAreNotCalledFailure(t *testing.T) {
	f := &fakePage{appearAfter: 3 * time.Second}
	lg, out := captureLog()
	if !enableCaptionsOn(f.page(), lg) {
		t.Fatalf("не заметили появившиеся субтитры; лог: %s", out)
	}
	if strings.Contains(out.String(), "включить не удалось") {
		t.Errorf("соврали про поломку: %s", out)
	}
	if f.presses != 1 {
		t.Errorf("нажатий %d — лишние нажатия выключают уже включённые субтитры", f.presses)
	}
}

// Проверять надо и после последнего нажатия: именно этого прежний код не
// делал. Площадка глотает все нажатия кроме последнего.
func TestLastToggleIsCheckedToo(t *testing.T) {
	f := &fakePage{appearAfter: time.Second, ignorePresses: captionToggleTries - 1}
	lg, out := captureLog()
	if !enableCaptionsOn(f.page(), lg) {
		t.Fatalf("итог последнего нажатия не проверили; лог: %s", out)
	}
	if strings.Contains(out.String(), "включить не удалось") {
		t.Errorf("соврали про поломку: %s", out)
	}
}

// Субтитры уже включены — трогать переключатель нельзя: он их выключит.
func TestAlreadyOnCaptionsAreNotToggled(t *testing.T) {
	f := &fakePage{}
	f.on = true
	lg, _ := captureLog()
	if !enableCaptionsOn(f.page(), lg) {
		t.Fatal("не увидели уже включённые субтитры")
	}
	if f.presses != 0 {
		t.Errorf("нажали %d раз на уже включённых субтитрах", f.presses)
	}
}

// А когда их правда не включить — сказать об этом надо. Честное сообщение о
// поломке дороже молчания: субтитры единственный источник имён.
func TestCaptionsThatNeverAppearAreReported(t *testing.T) {
	f := &fakePage{appearAfter: time.Hour}
	lg, out := captureLog()
	if enableCaptionsOn(f.page(), lg) {
		t.Fatal("доложили об успехе, которого не было")
	}
	if !strings.Contains(out.String(), "включить не удалось") {
		t.Errorf("промолчали о поломке: %s", out)
	}
	if f.presses != captionToggleTries {
		t.Errorf("нажатий %d, ожидали %d", f.presses, captionToggleTries)
	}
}

// Страница сама знает, что субтитров не будет, — жать нечего.
func TestUnavailableCaptionsAreNotToggled(t *testing.T) {
	f := &fakePage{unavailable: true, appearAfter: time.Hour}
	lg, out := captureLog()
	if enableCaptionsOn(f.page(), lg) {
		t.Fatal("доложили об успехе на площадке без субтитров")
	}
	if f.presses != 0 {
		t.Errorf("нажали %d раз там, где субтитров нет", f.presses)
	}
	if !strings.Contains(out.String(), "выключены") {
		t.Errorf("не объяснили, почему имён не будет: %s", out)
	}
}

func TestToggleErrorStopsImmediately(t *testing.T) {
	f := &fakePage{toggleErr: errors.New("страница закрылась")}
	lg, out := captureLog()
	if enableCaptionsOn(f.page(), lg) {
		t.Fatal("доложили об успехе после ошибки переключения")
	}
	if !strings.Contains(out.String(), "страница закрылась") {
		t.Errorf("ошибку не показали: %s", out)
	}
}

// Один неответивший опрос — не повод считать, что субтитров нет.
func TestSinglePollFailureIsNotFatal(t *testing.T) {
	f := &fakePage{appearAfter: time.Second, pollFails: 1}
	lg, out := captureLog()
	if !enableCaptionsOn(f.page(), lg) {
		t.Fatalf("сдались из-за одного неответившего опроса; лог: %s", out)
	}
}
