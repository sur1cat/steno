package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/demo"
)

// Сквозная проверка `steno mcp`: собранный бинарник, разговор по stdio —
// initialize, tools/list, tools/call. Инструменты сами по себе проверяет
// internal/mcp через транспорт в памяти; здесь важно другое — что бинарник
// поднимается с настоящей настройкой, что в stdout не попадает ничего, кроме
// протокола (одна строка лога туда — и клиент рвёт соединение), и что
// закрытие пункта доезжает до базы на диске.
func TestMCPOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("сборка бинарника — не для -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("нет go в PATH — бинарник собрать нечем")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "steno")
	// Не больше двух потоков: машина общая с человеком, и на прогретом кэше
	// сборки это всё равно секунды.
	build := exec.Command("go", "build", "-p", "2", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOMAXPROCS=2")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка: %v\n%s", err, out)
	}

	dataDir := filepath.Join(dir, "data")
	st, err := core.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	projects, _, err := demo.Seed(st, "ru")
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	// Проекты — через конфиг, как у настоящей установки: open() перенесёт их
	// в базу при первом запуске, и по псевдониму «биллинг» найдутся Платежи.
	cfg, _ := json.Marshal(map[string]any{"data_dir": dataDir, "projects": projects})
	cfgPath := filepath.Join(dir, "steno.json")
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.Command(bin, "mcp", "-c", cfgPath)
	// Лог сервера — в буфер: при падении он объясняет, что случилось, а при
	// успехе никому не нужен. Свой HOME, чтобы не читать чужой указатель на
	// настройку; впрочем, с явным -c он и не читается.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "HOME="+dir, "STENO_LANG=ru")
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "steno-test", Version: "0"}, nil).
		Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("подключение: %v\nstderr:\n%s", err, stderr.String())
	}
	defer cs.Close()

	if got := cs.InitializeResult().ServerInfo.Name; got != "steno" {
		t.Errorf("сервер представился как %q", got)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v\nstderr:\n%s", err, stderr.String())
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"list_meetings", "get_followup", "get_transcript", "search", "projects", "open_items", "close_item"} {
		if !contains(names, want) {
			t.Errorf("нет инструмента %s среди %v", want, names)
		}
	}

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_followup",
		Arguments: map[string]any{"id": "2026-09-08-1100-a1b2"}})
	if err != nil {
		t.Fatalf("tools/call: %v\nstderr:\n%s", err, stderr.String())
	}
	if res.IsError {
		t.Fatalf("get_followup: %s", text(res))
	}
	if !strings.Contains(text(res), "Релиз 2.4 сдвинули на пятницу") {
		t.Errorf("follow-up не тот: %s", text(res))
	}

	// Псевдоним проекта из конфига доехал через open() до базы.
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "open_items",
		Arguments: map[string]any{"project": "биллинг", "kind": "task"}})
	if err != nil || res.IsError {
		t.Fatalf("open_items: %v %s", err, text(res))
	}
	var open struct {
		Project string `json:"project"`
		Items   []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(text(res)), &open); err != nil {
		t.Fatal(err)
	}
	if open.Project != "Платежи" || len(open.Items) != 1 {
		t.Fatalf("open_items по биллингу: %s", text(res))
	}

	// Единственная запись: закрытие через MCP видно тем, кто откроет базу
	// следующим — панели, `steno projects`, следующему созвону.
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "close_item",
		Arguments: map[string]any{"id": open.Items[0].ID, "reason": "проверено сквозным тестом"}})
	if err != nil || res.IsError {
		t.Fatalf("close_item: %v %s", err, text(res))
	}
	cs.Close()
	st, err = core.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	it, err := st.Item(open.Items[0].ID)
	if err != nil || it.Status != "done" || it.Note != "проверено сквозным тестом" {
		t.Errorf("в базе после закрытия: %+v, %v", it, err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
