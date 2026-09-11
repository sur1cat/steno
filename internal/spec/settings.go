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
// Читаются своим разбором того же файла, а не полем в core.Config. Причина
// простая: раздел появился последним, а конфиг правят несколько человек сразу,
// и лишний повод разойтись в одном общем файле никому не нужен. Если раздел
// когда-нибудь переедет в core.Config, здесь достаточно будет заменить тело
// LoadSettings — наружу торчит один тип.
//
// Чего в этих настройках НЕТ и не будет: пути к исполняемому файлу агента.
// Такое поле, выставляемое через веб-форму панели, — это выполнение
// произвольного кода от имени того, кто запустил steno. Агент выбирается
// именем (claude | codex), а имя ищется в PATH.
type Settings struct {
	// Выключено. Это не осторожность на всякий случай: включённая по умолчанию
	// настройка означала бы, что steno, поставленный из Homebrew ради заметок с
	// созвонов, получил право писать файлы и выполнять команды на чужой машине.
	// Включает это человек, руками, один раз, понимая зачем.
	Enabled bool `json:"enabled"`

	// Кем исполнять: auto | claude | codex. Пусто — auto, то есть по
	// brain.provider (см. resolveAgent).
	Provider string `json:"provider"`

	// От чего ответвляться. Пусто — от того коммита, на котором репозиторий
	// стоит сейчас. В main не коммитим никогда и никуда не пушим.
	BaseBranch string `json:"base_branch"`

	// Приставка к имени ветки. Она же — обещание человеку: всё, что начинается
	// с неё, сделано машиной, и это видно в `git branch` без пояснений.
	BranchPrefix string `json:"branch_prefix"`

	// Куда класть рабочие копии. Пусто — <data_dir>/agent.
	WorktreeDir string `json:"worktree_dir"`

	// Потолок расхода на один запуск, если исполнитель умеет его соблюдать.
	MaxUSD float64 `json:"max_usd"`

	// Сколько ждать агента. Ноль — час.
	Timeout core.Duration `json:"timeout"`
}

const (
	defaultPrefix  = "steno/"
	defaultTimeout = time.Hour
)

func (s Settings) prefix() string {
	if p := strings.TrimSpace(s.BranchPrefix); p != "" {
		return p
	}
	return defaultPrefix
}

func (s Settings) timeout() time.Duration {
	if d := s.Timeout.D(); d > 0 {
		return d
	}
	return defaultTimeout
}

func (s Settings) worktreeDir(dataDir string) string {
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
