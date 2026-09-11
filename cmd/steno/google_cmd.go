package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"github.com/sur1cat/steno/internal/i18n"
)

// steno google — подключение Google отдельно от мастера.
//
// Мастер спрашивает Client ID и секрет на шестом шаге из десяти, и добавить
// их в уже настроенный steno значило ответить заново на всё остальное — и в
// конце согласиться перезаписать конфиг. Человек, получивший две строки из
// консоли Google, хочет вставить их и всё; ради этого команда и существует.
func cmdGoogle(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("google", flag.ContinueOnError)
	cfgPath := setupFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := core.ResolveConfigPath(*cfgPath)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf(i18n.Tr("конфига %s нет — сначала steno setup"), path)
	}
	switch fs.Arg(0) {
	case "", "status":
		return googleStatus(path)
	case "set":
		return googleSet(path, bufio.NewReader(os.Stdin))
	}
	return fmt.Errorf(i18n.Tr("не понял «%s»: steno google | steno google set"), fs.Arg(0))
}

// googleStatus — три вещи, которые нужны, чтобы кнопка в панели заработала,
// и что из них уже есть.
func googleStatus(path string) error {
	cfg, err := core.LoadConfig(path)
	if err != nil {
		return err
	}
	envPath := filepath.Join(filepath.Dir(path), ".env")
	_ = loadDotEnv(envPath)
	secretEnv := cfg.Google.ClientSecretEnv
	if secretEnv == "" {
		secretEnv = "GOOGLE_CLIENT_SECRET"
	}
	fmt.Println(i18n.Tr("Google · вход по кнопке"))
	mark := func(ok bool, yes, no string) {
		if ok {
			fmt.Println("  ✓ " + yes)
		} else {
			fmt.Println("  ✗ " + no)
		}
	}
	mark(cfg.Google.ClientID != "", i18n.Tr("Client ID задан"), i18n.Tr("Client ID не задан"))
	mark(os.Getenv(secretEnv) != "", i18n.Tr("секрет в .env есть"), i18n.Tr("секрета в .env нет"))
	_, tokErr := google.LoadGoogleToken(cfg)
	mark(tokErr == nil, i18n.Tr("согласие получено — кнопка нажата"), i18n.Tr("согласия ещё нет"))
	fmt.Println()
	switch {
	case cfg.Google.ClientID == "" || os.Getenv(secretEnv) == "":
		fmt.Println(dim(i18n.Tr("  вставить Client ID и секрет:  steno google set")))
	case tokErr != nil:
		fmt.Println(dim(i18n.Tr("  осталось нажать «Подключить Google» в настройках панели")))
	}
	return nil
}

// googleSet спрашивает две строки из консоли Google и кладёт их куда надо:
// Client ID — в конфиг (он не секрет, у настольных приложений Google сам так
// говорит), секрет — в .env рядом, не трогая остальных строк в нём.
func googleSet(path string, in *bufio.Reader) error {
	cfg, err := core.LoadConfig(path)
	if err != nil {
		return err
	}
	fmt.Println(dim(i18n.Tr("  Открой https://console.cloud.google.com/apis/credentials")))
	fmt.Println(dim(i18n.Tr("  → Create credentials → OAuth client ID → тип Desktop app.")))
	fmt.Println(dim(i18n.Tr("  Google покажет две строки — скопируй их сюда.")))
	fmt.Println()

	id := askLine(in, i18n.Tr("Client ID"), cfg.Google.ClientID)
	if id == "" {
		return errors.New(i18n.Tr("без Client ID кнопка в панели не появится"))
	}
	secret := askHidden(in, i18n.Tr("Client secret"))
	if secret == "" {
		return errors.New(i18n.Tr("без секрета Google не выдаст согласие"))
	}

	secretEnv := cfg.Google.ClientSecretEnv
	if secretEnv == "" {
		secretEnv = "GOOGLE_CLIENT_SECRET"
	}
	envPath := filepath.Join(filepath.Dir(path), ".env")
	if err := upsertEnv(envPath, secretEnv, secret); err != nil {
		return err
	}
	if err := setConfigClientID(path, id, secretEnv); err != nil {
		return err
	}
	fmt.Println(ok(i18n.Tr("записано: Client ID в ") + filepath.Base(path) + i18n.Tr(", секрет в .env")))
	fmt.Println(dim(i18n.Tr("  сервис читает .env на старте:  steno stop && steno start")))
	fmt.Println(dim(i18n.Tr("  потом «Подключить Google» в настройках панели")))
	return nil
}

// setConfigClientID правит две строки в JSON, а не перезаписывает файл из
// структуры: конфиг человек мог править руками, и порядок с комментариями
// ему дорог; неизвестные нам ключи тоже должны пережить правку.
func setConfigClientID(path, id, secretEnv string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf(i18n.Tr("конфиг %s не читается: %w"), path, err)
	}
	var g map[string]any
	if cur, ok := doc["google"]; ok && len(cur) > 0 {
		if err := json.Unmarshal(cur, &g); err != nil {
			return fmt.Errorf(i18n.Tr("раздел google в %s не читается: %w"), path, err)
		}
	}
	if g == nil {
		g = map[string]any{}
	}
	g["client_id"] = id
	g["client_secret_env"] = secretEnv
	gb, err := json.Marshal(g)
	if err != nil {
		return err
	}
	doc["google"] = gb
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// upsertEnv заменяет строку KEY=… в .env или дописывает её, остальные строки
// оставляя как есть — там лежит токен Telegram и прочее, что терять нельзя.
// Права 0600: файл с секретами.
func upsertEnv(envPath, key, value string) error {
	var lines []string
	if raw, err := os.ReadFile(envPath); err == nil {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), key+"=") {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if !found {
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		lines = append(lines, key+"="+value)
	}
	return os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func askLine(in *bufio.Reader, question, def string) string {
	if def != "" {
		fmt.Printf("  %s [%s]: ", question, dim(def))
	} else {
		fmt.Printf("  %s: ", question)
	}
	line, _ := in.ReadString('\n')
	if v := strings.TrimSpace(line); v != "" {
		return v
	}
	return def
}

// askHidden не показывает ввод, когда есть терминал; в трубе читает строку.
func askHidden(in *bufio.Reader, question string) string {
	fmt.Printf("  %s: ", question)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, _ := in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
