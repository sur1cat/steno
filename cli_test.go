package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Свежая установка — это состояние, в котором команду видят первым делом, а не
// краевой случай. `steno projects` без аргументов — форма, напечатанная в
// собственной справке, — падал на ней паникой: аргумент читался раньше проверки,
// что он есть. Нашлось это только прогоном с нуля, потому что во всех прошлых
// проверках база уже была непустой.
func TestCommandsSurviveEmptyInstall(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "steno.json")
	cfg := `{"data_dir":` + quote(filepath.Join(dir, "data")) + `}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		run  func([]string) error
	}{
		{"projects", cmdProjects},
		{"list", cmdList},
		{"cost", cmdCost},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run([]string{"-c", cfgPath}); err != nil {
				t.Fatalf("на пустой установке: %v", err)
			}
		})
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
