package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	ProjectsGlob        string
	CodeServerBind      string
	CodeServerPublicURL string // optional; e.g. "https://code.kghf.io" for CF Tunnel setups
	LogDir              string
	BrewPrefix          string
	ClaudeContinue      string
	ClaudeExtraFlags    string
	ExplicitProjects    []string

	// Derived (not in clawdstacc.conf): repo directory, used to locate templates.
	RepoDir string
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		ProjectsGlob:     filepath.Join(home, "_*"),
		CodeServerBind:   "0.0.0.0:8443",
		LogDir:           filepath.Join(home, "Library/Logs/clawdstacc"),
		BrewPrefix:       "/opt/homebrew",
		ClaudeContinue:   "true",
		ClaudeExtraFlags: "",
	}
}

var (
	scalarRE = regexp.MustCompile(`^([A-Z_]+)=(.*)$`)
	blockRE  = regexp.MustCompile(`(?s)^[ \t]*EXPLICIT_PROJECTS=\(\s*\n(.*?)\n[ \t]*\)\s*$`)
)

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	home, _ := os.UserHomeDir()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	text := string(raw)

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := scalarRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		if strings.HasPrefix(val, "(") {
			continue
		}
		val = strings.Trim(val, `"'`)
		val = strings.ReplaceAll(val, "$HOME", home)

		switch key {
		case "CODESERVER_PUBLIC_URL":
			cfg.CodeServerPublicURL = val
		case "PROJECTS_GLOB":
			cfg.ProjectsGlob = val
		case "CODESERVER_BIND":
			cfg.CodeServerBind = val
		case "LOG_DIR":
			cfg.LogDir = val
		case "BREW_PREFIX":
			cfg.BrewPrefix = val
		case "CLAUDE_CONTINUE":
			cfg.ClaudeContinue = val
		case "CLAUDE_EXTRA_FLAGS":
			cfg.ClaudeExtraFlags = val
		}
	}

	// Look for an active EXPLICIT_PROJECTS=( ... ) block (first line of which
	// must NOT be commented out).
	for _, m := range blockRE.FindAllStringSubmatchIndex(text, -1) {
		blockStart := m[0]
		// Find the start of the line that contains EXPLICIT_PROJECTS=
		lineStart := strings.LastIndex(text[:blockStart], "\n") + 1
		lineEnd := strings.Index(text[blockStart:], "\n")
		if lineEnd == -1 {
			lineEnd = len(text) - blockStart
		}
		firstLine := text[lineStart : blockStart+lineEnd]
		if strings.HasPrefix(strings.TrimSpace(firstLine), "#") {
			continue
		}
		body := text[m[2]:m[3]]
		var items []string
		for _, raw := range strings.Split(body, "\n") {
			raw = strings.TrimSpace(raw)
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			raw = strings.Trim(raw, `"'`)
			raw = strings.ReplaceAll(raw, "$HOME", home)
			items = append(items, raw)
		}
		if len(items) > 0 {
			cfg.ExplicitProjects = items
			break
		}
	}

	return cfg, nil
}
