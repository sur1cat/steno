package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Секрет Google ложится в .env рядом с токеном Telegram — и токен обязан
// пережить это невредимым. Повторный ввод меняет строку, а не плодит вторую.
func TestUpsertEnvKeepsNeighboursAndReplacesInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("# секреты\nTELEGRAM_BOT_TOKEN=tg\nGOOGLE_CLIENT_SECRET=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnv(p, "GOOGLE_CLIENT_SECRET", "new"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	got := string(raw)
	if !strings.Contains(got, "TELEGRAM_BOT_TOKEN=tg\n") {
		t.Errorf("токен Telegram пропал: %q", got)
	}
	if strings.Count(got, "GOOGLE_CLIENT_SECRET=") != 1 || !strings.Contains(got, "GOOGLE_CLIENT_SECRET=new\n") {
		t.Errorf("секрет должен замениться на месте один раз: %q", got)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("права .env %o, ждали 0600", st.Mode().Perm())
	}
	// Файла не было — появляется с одной строкой.
	p2 := filepath.Join(t.TempDir(), ".env")
	if err := upsertEnv(p2, "K", "v"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(p2)
	if string(raw) != "K=v\n" {
		t.Errorf("новый .env: %q", raw)
	}
}

// Правится ровно раздел google; остальное в конфиге, включая ключи, которых
// эта версия не знает, остаётся как было.
func TestSetConfigClientIDTouchesOnlyGoogle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "steno.json")
	src := `{"lang":"ru","telegram":{"chat_id":"1"},"google":{"client_id":"","client_secret_env":"GOOGLE_CLIENT_SECRET","future_key":true},"unknown_top":42}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setConfigClientID(p, "abc.apps.googleusercontent.com", "GOOGLE_CLIENT_SECRET"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("конфиг после правки не читается: %v\n%s", err, raw)
	}
	g := doc["google"].(map[string]any)
	if g["client_id"] != "abc.apps.googleusercontent.com" {
		t.Errorf("client_id не записан: %v", g)
	}
	if g["future_key"] != true {
		t.Errorf("чужой ключ в разделе google потерян: %v", g)
	}
	if doc["unknown_top"] != float64(42) || doc["telegram"].(map[string]any)["chat_id"] != "1" {
		t.Errorf("задело соседние разделы: %v", doc)
	}
}

// В трубе (без терминала) обе строки читаются из stdin — так проверяется и
// сама запись, и то, что команда не виснет без TTY.
func TestGoogleSetFromPipe(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "steno.json")
	if err := os.WriteFile(p, []byte(`{"google":{"client_secret_env":"GOOGLE_CLIENT_SECRET"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	in := bufio.NewReader(strings.NewReader("my-id\nmy-secret\n"))
	if err := googleSet(p, in); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "GOOGLE_CLIENT_SECRET=my-secret\n") {
		t.Errorf("секрет не в .env: %q", env)
	}
	cfg, _ := os.ReadFile(p)
	if !strings.Contains(string(cfg), `"client_id": "my-id"`) {
		t.Errorf("client_id не в конфиге: %s", cfg)
	}
}

// Код из вставленного адреса: только при совпавшем state — иначе можно
// подсунуть чужой код. Голая строка без адреса кодом не считается.
func TestCodeFromPastedRequiresMatchingState(t *testing.T) {
	const st = "abc123"
	if got := codeFromPasted("http://127.0.0.1:5555/callback?state=abc123&code=4/xyz", st); got != "4/xyz" {
		t.Errorf("код не вынут: %q", got)
	}
	if got := codeFromPasted("http://127.0.0.1:5555/callback?state=other&code=4/xyz", st); got != "" {
		t.Errorf("чужой state принят: %q", got)
	}
	if got := codeFromPasted("4/xyz", st); got != "" {
		t.Errorf("голая строка принята как код: %q", got)
	}
}
