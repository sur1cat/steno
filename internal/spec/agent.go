package spec

import (
	"fmt"
	"strings"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Кто будет исполнять ТЗ.
//
// Здесь единственное место, где steno вообще знает имя агента, и оно нарочно
// устроено как продолжение выбора модели, а не отдельная настройка. Человек,
// который перевёл разбор на codex, не должен обнаружить, что задачи всё равно
// уезжают в Claude: он выбирал не «чем делать follow-up», он выбирал, чем и за
// чей счёт steno разговаривает с моделью.
//
// Но правит файлы не всякая модель. Ключ по HTTP и скрипт-адаптер отвечают
// текстом на текст: они не умеют ни открыть файл, ни выполнить команду. Агентов
// среди наших путей ровно два — `claude -p` и `codex exec`, — и когда выбранный
// провайдер не из них, честный ответ один: сказать об этом. Тихо подставить
// Claude было бы удобнее и стоило бы человеку денег на счёте, которого он не
// открывал.

type agent struct {
	Bin   string // имя команды: ищется в PATH, путём из настроек не задаётся
	Title string // как называть человеку
}

// wantAgent — кого выбрали, ещё не спрашивая, установлен ли он.
//
// Отдельно от проверки готовности нарочно: выбор — это правило, и его надо
// уметь проверить тестом на машине, где не стоит ни claude, ни codex. Слепив
// выбор с exec.LookPath, мы получили бы проверку, которая на чужой машине
// проходит по совсем другой причине.
func wantAgent(cfg *core.Config, set Settings) (string, error) {
	want := strings.ToLower(strings.TrimSpace(set.Provider))
	switch want {
	case "claude", "codex":
		return want, nil
	case "", "auto":
	default:
		return "", fmt.Errorf(i18n.Tr("agent.provider=%q — допустимы auto, claude, codex"), set.Provider)
	}
	switch cfg.BrainProvider() {
	case core.ProviderCodex:
		return "codex", nil
	case core.ProviderOpenAI, core.ProviderCommand:
		return "", fmt.Errorf(
			i18n.Tr("разбор идёт через %s, а этот путь умеет только текст: файлы он не правит.\n")+
				i18n.Tr("  → поставь agent.provider = \"claude\" или \"codex\" — это те два, что умеют работать в репозитории"),
			brain.ProviderTitle(cfg))
	}
	return "claude", nil
}

func resolveAgent(cfg *core.Config, set Settings) (agent, error) {
	want, err := wantAgent(cfg, set)
	if err != nil {
		return agent{}, err
	}
	switch want {
	case "claude":
		if ok, why := brain.ClaudeCLIAvailable(); !ok {
			return agent{}, fmt.Errorf(i18n.Tr("исполнять некому: %s"), why)
		}
		return agent{Bin: "claude", Title: "Claude Code"}, nil
	default:
		if ok, why := brain.CodexCLIAvailable(); !ok {
			return agent{}, fmt.Errorf(i18n.Tr("исполнять некому: %s"), why)
		}
		return agent{Bin: "codex", Title: "Codex"}, nil
	}
}

// denyRules — то, что агенту запрещено, даже если он решит, что так удобнее.
//
// Это второй слой поверх рабочей копии: копия защищает чужую работу, а эти
// правила — историю. Коммит делает система и только в свою ветку; push не
// делает никто, кроме человека, руками и глазами.
//
// Файл пишем сами, из констант. Ни одна строка отсюда не приходит из настроек и
// тем более из панели: правила доступа, задаваемые через веб-форму, — это не
// правила доступа.
var denyRules = []string{
	"Bash(git push:*)",
	"Bash(git commit:*)",
	"Bash(git checkout:*)",
	"Bash(git switch:*)",
	"Bash(git merge:*)",
	"Bash(git rebase:*)",
	"Bash(git reset:*)",
	"Bash(git worktree:*)",
	"Bash(git branch:*)",
	"Bash(sudo:*)",
	"Bash(rm -rf /*)",
	"Bash(curl:*)",
	"Bash(wget:*)",
	"WebFetch",
}
