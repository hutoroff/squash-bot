package featureflags

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestDefaultsAndPrecedence(t *testing.T) {
	yes, no := true, false
	for _, d := range Definitions() {
		if d.Default {
			t.Errorf("flag %s must default disabled", d.Key)
		}
		for _, tt := range []struct {
			global, group *bool
			enabled       bool
			source        string
		}{
			{nil, nil, false, "default"}, {&yes, nil, true, "global"}, {&no, nil, false, "global"},
			{&no, &yes, true, "group"}, {&yes, &no, false, "group"}, {nil, &no, false, "group"},
		} {
			s := Resolve(d, tt.global, tt.group)
			if s.Enabled != tt.enabled || s.Source != tt.source {
				t.Errorf("bad resolution: %+v", s)
			}
		}
	}
	if _, err := Lookup("typo"); err != ErrUnknown {
		t.Fatal("unknown key accepted")
	}
}

// This intentionally checks a small stable catalog, not prose. Semantic changes
// still require the same-task documentation review mandated by AGENTS.md.
func TestDocumentationMatchesRegistry(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	text, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../docs/feature-toggles.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| (disabled|enabled) \\| ([^|]+) \\| `([^`]+)` \\|$").FindAllStringSubmatch(string(text), -1)
	defs := Definitions()
	if len(rows) != len(defs) {
		t.Fatalf("documented %d flags, registry has %d", len(rows), len(defs))
	}
	seen := map[Key]bool{}
	for _, row := range rows {
		key := Key(row[1])
		d, err := Lookup(key)
		if err != nil || seen[key] {
			t.Fatalf("stale/duplicate documented flag %q", key)
		}
		seen[key] = true
		scopes := "global"
		if d.GroupScoped {
			scopes += ", group"
		}
		if row[2] != "disabled" || d.Default || row[3] != scopes || row[4] != d.Service {
			t.Errorf("catalog metadata differs for %s", key)
		}
	}
}
