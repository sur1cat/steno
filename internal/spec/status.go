package spec

import (
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Состояние этой части одним значением — для doctor, панели, строки меню и
// `steno agent`. Четыре места показывают одно и то же, и считать это в каждом
// по-своему значило бы получить четыре разных ответа на вопрос «а агент у
// меня вообще работает?».

type Readiness struct {
	// Разрешено ли исполнение (agent.enabled).
	Enabled bool `json:"enabled"`
	// Собирается ли ТЗ само после разбора (agent.auto_spec).
	AutoSpec bool `json:"autoSpec"`
	// Кто исполняет: «Claude Code» или «Codex». Пусто — выбрать не вышло, см. Why.
	Executor string `json:"executor"`
	// Имя команды исполнителя: claude | codex.
	Bin string `json:"bin"`
	// Исполнитель найден в PATH и вошёл. Считается только при probe: это
	// запуск подпроцесса, а не чтение настройки.
	Ready bool `json:"ready"`
	// Что не так — словами, которые можно показать как есть. Пусто — всё
	// в порядке (или не проверяли: см. Probed).
	Why string `json:"why"`
	// Проверяли ли исполнителя живьём. Панель без проверки говорит только
	// то, что написано в настройке, и не обещает, что claude установлен.
	Probed bool `json:"probed"`
	// Приставка веток, по которой работу машины видно в списке веток.
	BranchPrefix string `json:"branchPrefix"`
}

// Check собирает состояние. probe — сходить к исполнителю (`claude auth
// status`, `codex login status`): это секунда и подпроцесс, doctor'у в самый
// раз, а на каждый запрос панели — нет.
func Check(cfg *core.Config, set Settings, probe bool) Readiness {
	r := Readiness{
		Enabled: set.Enabled, AutoSpec: set.AutoSpec,
		BranchPrefix: prefixOf(set), Probed: probe,
	}
	want, err := wantAgent(cfg, set)
	if err != nil {
		r.Why = err.Error()
		return r
	}
	r.Bin = want
	r.Executor = map[string]string{"claude": "Claude Code", "codex": "Codex"}[want]
	if !probe {
		return r
	}
	var ok bool
	var why string
	if want == "claude" {
		ok, why = brain.ClaudeCLIAvailable()
	} else {
		ok, why = brain.CodexCLIAvailable()
	}
	r.Ready = ok
	if !ok {
		r.Why = i18n.Trf("исполнять некому: %s", why)
	}
	return r
}
