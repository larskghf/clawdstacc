package clawd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_DefaultsWhenMissing(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.conf"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	home, _ := os.UserHomeDir()
	if cfg.ProjectsGlob != filepath.Join(home, "_*") {
		t.Errorf("default ProjectsGlob = %q, want %q", cfg.ProjectsGlob, filepath.Join(home, "_*"))
	}
	if cfg.CodeServerBind != "0.0.0.0:8443" {
		t.Errorf("default CodeServerBind = %q", cfg.CodeServerBind)
	}
	if cfg.BrewPrefix != "/opt/homebrew" {
		t.Errorf("default BrewPrefix = %q", cfg.BrewPrefix)
	}
	if cfg.ClaudeContinue != "true" {
		t.Errorf("default ClaudeContinue = %q", cfg.ClaudeContinue)
	}
}

func TestLoadConfig_ParsesScalars(t *testing.T) {
	conf := strings.Join([]string{
		`# clawdstacc.conf`,
		`PROJECTS_GLOB="$HOME/work/_*"`,
		`CODESERVER_BIND="127.0.0.1:9000"`,
		`CODESERVER_PUBLIC_URL="https://code.example.com"`,
		`LOG_DIR="$HOME/Library/Logs/clawdstacc"`,
		`BREW_PREFIX="/usr/local"`,
		`CLAUDE_CONTINUE="false"`,
		`CLAUDE_EXTRA_FLAGS="--dangerously-skip-permissions"`,
		`# trailing comment`,
		``,
	}, "\n")
	path := filepath.Join(t.TempDir(), "clawdstacc.conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	home, _ := os.UserHomeDir()
	want := map[string]string{
		"ProjectsGlob":        filepath.Join(home, "work/_*"),
		"CodeServerBind":      "127.0.0.1:9000",
		"CodeServerPublicURL": "https://code.example.com",
		"LogDir":              filepath.Join(home, "Library/Logs/clawdstacc"),
		"BrewPrefix":          "/usr/local",
		"ClaudeContinue":      "false",
		"ClaudeExtraFlags":    "--dangerously-skip-permissions",
	}
	got := map[string]string{
		"ProjectsGlob":        cfg.ProjectsGlob,
		"CodeServerBind":      cfg.CodeServerBind,
		"CodeServerPublicURL": cfg.CodeServerPublicURL,
		"LogDir":              cfg.LogDir,
		"BrewPrefix":          cfg.BrewPrefix,
		"ClaudeContinue":      cfg.ClaudeContinue,
		"ClaudeExtraFlags":    cfg.ClaudeExtraFlags,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestLoadConfig_ExplicitProjectsBlock(t *testing.T) {
	conf := strings.Join([]string{
		`PROJECTS_GLOB="$HOME/_*"`,
		`# A commented-out block — should be ignored:`,
		`# EXPLICIT_PROJECTS=(`,
		`#   "$HOME/_old"`,
		`# )`,
		`EXPLICIT_PROJECTS=(`,
		`  "$HOME/_real-one"`,
		`  "$HOME/_real-two"`,
		`  # inline comment skipped`,
		`)`,
	}, "\n")
	path := filepath.Join(t.TempDir(), "clawdstacc.conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := []string{
		filepath.Join(home, "_real-one"),
		filepath.Join(home, "_real-two"),
	}
	if len(cfg.ExplicitProjects) != len(want) {
		t.Fatalf("ExplicitProjects len = %d, want %d (%v)", len(cfg.ExplicitProjects), len(want), cfg.ExplicitProjects)
	}
	for i, p := range want {
		if cfg.ExplicitProjects[i] != p {
			t.Errorf("ExplicitProjects[%d] = %q, want %q", i, cfg.ExplicitProjects[i], p)
		}
	}
}
