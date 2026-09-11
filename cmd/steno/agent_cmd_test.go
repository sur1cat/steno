package main

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/spec"
)

// `steno agent on|off` — тот же выключатель, что и в файле: команда правит
// steno.json, и следующий же `steno spec run` его видит. Без перезапуска чего
// бы то ни было.
func TestAgentSwitchWritesTheConfig(t *testing.T) {
	cfgPath, _ := specCLI(t, "")

	out := captureStdout(t, func() {
		if err := cmdAgent([]string{"-c", cfgPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "выключено") || !strings.Contains(out, "steno agent on") {
		t.Fatalf("состояние по умолчанию описано не так:\n%s", out)
	}

	out = captureStdout(t, func() {
		if err := cmdAgent([]string{"on", "-c", cfgPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !spec.LoadSettings(cfgPath).Enabled {
		t.Fatal("steno agent on не включил исполнение в файле")
	}
	if !strings.Contains(out, "включено") {
		t.Fatalf("после включения не сказано, что включено:\n%s", out)
	}

	captureStdout(t, func() {
		if err := cmdAgent([]string{"auto", "on", "-c", cfgPath}); err != nil {
			t.Fatal(err)
		}
	})
	set := spec.LoadSettings(cfgPath)
	if !set.AutoSpec || !set.Enabled {
		t.Fatalf("auto on сбил другой выключатель: %+v", set)
	}

	captureStdout(t, func() {
		if err := cmdAgent([]string{"off", "-c", cfgPath}); err != nil {
			t.Fatal(err)
		}
	})
	set = spec.LoadSettings(cfgPath)
	if set.Enabled || !set.AutoSpec {
		t.Fatalf("off выключил не то: %+v", set)
	}

	if err := cmdAgent([]string{"maybe", "-c", cfgPath}); err == nil || !strings.Contains(err.Error(), "steno agent on") {
		t.Fatalf("непонятное слово должно объяснять, что можно: %v", err)
	}
}
