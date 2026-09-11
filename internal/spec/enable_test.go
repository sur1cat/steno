package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Выключатель правит файл человека, а не переписывает его: разделы остаются
// в своём порядке, незнакомые ключи — на месте, а меняется ровно одно поле.
func TestSetEnabledKeepsTheFileAsItWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steno.json")
	write(t, path, `{
  "data_dir": "./data",
  "telegram": {"enabled": true, "chat_id": "-100500"},
  "agent": {
    "provider": "codex",
    "worktree_dir": "wt",
    "my_own_key": [1, 2, 3],
    "enabled": false
  },
  "panel": {"enabled": true}
}
`)
	if err := SetEnabled(path, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)

	// Порядок верхних разделов — тот же.
	for _, pair := range [][2]string{{`"data_dir"`, `"telegram"`}, {`"telegram"`, `"agent"`}, {`"agent"`, `"panel"`}} {
		if strings.Index(got, pair[0]) > strings.Index(got, pair[1]) {
			t.Errorf("разделы переставлены: %s оказался после %s\n%s", pair[0], pair[1], got)
		}
	}
	// Чужой ключ и чужой порядок внутри раздела целы.
	if !strings.Contains(got, `"my_own_key": [`) {
		t.Errorf("незнакомый ключ пропал:\n%s", got)
	}
	agent := got[strings.Index(got, `"agent"`):]
	if strings.Index(agent, `"provider"`) > strings.Index(agent, `"enabled": true`) {
		t.Errorf("порядок полей раздела поехал:\n%s", got)
	}
	// Нетронутые поля не дописываются: короткий раздел человека остался коротким.
	if strings.Contains(got, `"timeout"`) || strings.Contains(got, `"max_usd"`) {
		t.Errorf("в раздел дописались поля, которых человек не писал:\n%s", got)
	}
	// А само поле поменялось — и читается тем же LoadSettings, что и сервис.
	set := LoadSettings(path)
	if !set.Enabled || set.Provider != "codex" {
		t.Errorf("после включения прочиталось %+v", set)
	}
	if set.WorktreeDir != filepath.Join(filepath.Dir(path), "wt") {
		t.Errorf("относительный worktree_dir не разрешён от конфига: %q", set.WorktreeDir)
	}
	// Файл остался валидным JSON и читается целиком как конфиг.
	if _, err := core.LoadConfig(path); err != nil {
		t.Fatalf("конфиг после правки не читается: %v", err)
	}

	if err := SetEnabled(path, false); err != nil {
		t.Fatal(err)
	}
	if LoadSettings(path).Enabled {
		t.Error("выключить не вышло")
	}
}

// Раздела в файле не было — появляется целиком, с умолчаниями, а не одной
// строкой "enabled": человек, открывший файл, должен увидеть все ручки.
func TestSetEnabledAddsWholeSectionWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steno.json")
	write(t, path, `{"data_dir": "./data"}`)
	if err := SetAutoSpec(path, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("после правки не JSON: %v\n%s", err, raw)
	}
	var agent map[string]any
	if err := json.Unmarshal(file["agent"], &agent); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"enabled", "auto_spec", "provider", "branch_prefix", "max_usd", "timeout"} {
		if _, ok := agent[k]; !ok {
			t.Errorf("в новом разделе нет %q: %v", k, agent)
		}
	}
	set := LoadSettings(path)
	if !set.AutoSpec || set.Enabled {
		t.Errorf("прочиталось %+v", set)
	}
	if set.BranchPrefix != "steno/" {
		t.Errorf("умолчание приставки не записалось: %q", set.BranchPrefix)
	}
}

func TestSetEnabledNeedsAConfig(t *testing.T) {
	err := SetEnabled(filepath.Join(t.TempDir(), "нет.json"), true)
	if err == nil || !strings.Contains(err.Error(), "steno setup") {
		t.Fatalf("без файла должен отсылать к setup: %v", err)
	}
}

// Check без проверки живьём — это только настройка: кто выбран и включено ли.
// С проверкой — ещё и есть ли он на машине; на машине без claude это отказ
// словами, а не паника и не «готов».
func TestCheckDescribesState(t *testing.T) {
	cfg := core.DefaultConfig()
	set := Settings{Enabled: true, AutoSpec: true, Provider: "codex", BranchPrefix: "bot/"}
	r := Check(cfg, set, false)
	if !r.Enabled || !r.AutoSpec || r.Executor != "Codex" || r.Bin != "codex" || r.Probed {
		t.Errorf("Check без проверки: %+v", r)
	}
	if r.BranchPrefix != "bot/" {
		t.Errorf("приставка: %q", r.BranchPrefix)
	}
	cfg.Brain.Provider = core.ProviderOpenAI
	r = Check(cfg, Settings{Enabled: true}, false)
	if r.Executor != "" || r.Why == "" {
		t.Errorf("текстовый провайдер должен дать отказ словами: %+v", r)
	}
	lines := Describe(r, Settings{Enabled: true}, "/tmp/data")
	if len(lines) != 4 || !strings.Contains(strings.Join(lines, "\n"), "не выбран") {
		t.Errorf("Describe: %q", lines)
	}
}
