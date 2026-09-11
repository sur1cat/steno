package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Настройки этой части — раздел "agent" в steno.json.
//
// Форма раздела живёт в core.AgentSettings: так его пишет мастер установки
// вместе с остальными разделами, а `steno agent on|off` правит тот же файл.
// Здесь — чтение. Оно нарочно своё, а не через core.LoadConfig: раздел
// перечитывается из файла на каждое обращение (SettingsFromEnv), и потому
// выключатель срабатывает без перезапуска сервиса — человек написал
// "enabled": true, нажал кнопку в панели, и она уже работает.
//
// Чего в этих настройках НЕТ и не будет: пути к исполняемому файлу агента —
// см. core.AgentSettings.
type Settings = core.AgentSettings

const (
	defaultPrefix  = "steno/"
	defaultTimeout = time.Hour
)

func prefixOf(s Settings) string {
	if p := strings.TrimSpace(s.BranchPrefix); p != "" {
		return p
	}
	return defaultPrefix
}

func timeoutOf(s Settings) time.Duration {
	if d := s.Timeout.D(); d > 0 {
		return d
	}
	return defaultTimeout
}

func worktreeDirOf(s Settings, dataDir string) string {
	if d := strings.TrimSpace(s.WorktreeDir); d != "" {
		return d
	}
	return filepath.Join(dataDir, "agent")
}

// SettingsFromEnv — раздел "agent" из того же конфига, по которому поднялся
// сервис. Нужен панели: она держит разобранный core.Config, а пути к файлу у
// неё нет, и добавлять ей поле ради одного раздела значило бы править чужой
// тип.
func SettingsFromEnv() Settings {
	return LoadSettings(core.ResolveConfigPath(
		core.EnvOr("STENO_CONFIG", core.DefaultConfigPath)))
}

// SettingsFor — раздел "agent" для того конфига, с которым работает команда
// или сервис. Перечитывается из файла на каждое обращение: выключатель должен
// срабатывать без перезапуска. Конфиг без файла (умолчания) — то, что в нём
// и есть, а без него вовсе — по окружению, как SettingsFromEnv.
func SettingsFor(cfg *core.Config) Settings {
	switch {
	case cfg == nil:
		return SettingsFromEnv()
	case cfg.Path != "":
		return LoadSettings(cfg.Path)
	}
	if set := SettingsFromEnv(); set.Enabled || set.AutoSpec {
		return set
	}
	return cfg.Agent
}

// LoadSettings читает раздел "agent". Нет файла или нет раздела — всё
// выключено, и это правильный ответ, а не ошибка.
func LoadSettings(configPath string) Settings {
	if strings.TrimSpace(configPath) == "" {
		return Settings{}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return Settings{}
	}
	var file struct {
		Agent Settings `json:"agent"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return Settings{}
	}
	// Путь к рабочим копиям — относительно конфига, как и всё остальное в
	// steno: конфиг в /etc не должен заводить каталоги в текущем.
	if d := file.Agent.WorktreeDir; d != "" && !filepath.IsAbs(d) {
		file.Agent.WorktreeDir = filepath.Join(filepath.Dir(configPath), d)
	}
	return file.Agent
}
