package main

import (
	"strings"
	"testing"
	"time"
)

// Напоминание должно говорить не только «через сколько», но и придёт ли бот.
// «Не придёт, потому что нет ссылки» — это ещё можно успеть поправить,
// а молчание разбирать потом уже поздно.
func TestRemindText(t *testing.T) {
	now := time.Date(2026, 9, 9, 14, 50, 0, 0, time.Local)
	base := ScheduleEntry{
		Title:     "Планёрка по релизу",
		StartsAt:  now.Add(10 * time.Minute),
		Attendees: []string{"Участник А", "Участник Б"},
		MeetURL:   "https://meet.google.com/abc-defg-hij",
	}

	got := remindText(base, now)
	for _, want := range []string{"Через 10 мин", "Планёрка по релизу", "15:00",
		"Участник А, Участник Б", "Бот придёт", "meet.google.com/abc-defg-hij"} {
		if !strings.Contains(got, want) {
			t.Errorf("в напоминании нет %q:\n%s", want, got)
		}
	}

	// Причина неявки должна быть названа словами.
	noLink := base
	noLink.MeetURL = ""
	noLink.Skip = "нет ссылки на Meet"
	if got := remindText(noLink, now); !strings.Contains(got, "Бот не придёт: нет ссылки на Meet") {
		t.Errorf("причина неявки не названа:\n%s", got)
	}

	// Отменённое руками — тоже отдельной причиной, иначе непонятно, почему.
	skipped := base
	skipped.Override = "skip"
	if got := remindText(skipped, now); !strings.Contains(got, "отменили в панели") {
		t.Errorf("ручная отмена не объяснена:\n%s", got)
	}

	// Ручное «пойти» перебивает автоматическую причину.
	forced := base
	forced.Skip = "участников 1, нужно хотя бы 2"
	forced.Override = "attend"
	if got := remindText(forced, now); !strings.Contains(got, "Бот придёт") {
		t.Errorf("ручное «пойти» не сработало:\n%s", got)
	}

	// Формулировка времени у самого начала.
	soon := base
	soon.StartsAt = now
	if got := remindText(soon, now); !strings.Contains(got, "Сейчас начинается") {
		t.Errorf("формулировка у начала: %s", got)
	}
}

// Напоминать надо один раз. Опрос календаря перезаписывает строку расписания
// целиком, поэтому отметка живёт отдельно — иначе одно и то же приходило бы
// каждую минуту.
func TestRemindOnlyOnce(t *testing.T) {
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	key := "https://meet.google.com/abc-defg-hij@2026-09-09T12:00Z"
	first, err := st.MarkReminded(key)
	if err != nil || !first {
		t.Fatalf("первая отметка: %v %v", first, err)
	}
	again, err := st.MarkReminded(key)
	if err != nil || again {
		t.Fatalf("напомнили повторно: %v %v", again, err)
	}

	// Перезапись расписания отметку не трогает.
	if err := st.SaveScheduled(ScheduleEntry{Key: key, Title: "Планёрка",
		MeetURL: "https://meet.google.com/abc-defg-hij", StartsAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if third, _ := st.MarkReminded(key); third {
		t.Error("отметка о напоминании не пережила обновление расписания")
	}
}
