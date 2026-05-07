package clawd

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates
var templatesFS embed.FS

// renderEmbeddedTemplate is the embed-FS counterpart of renderTemplate (which
// reads from disk). Both use the same `__KEY__` placeholder convention as the
// bash render_template helper, so templates remain editable in either world.
func renderEmbeddedTemplate(name string, vars map[string]string) (string, error) {
	raw, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		return "", fmt.Errorf("embedded template %q: %w", name, err)
	}
	content := string(raw)
	for k, v := range vars {
		content = strings.ReplaceAll(content, "__"+k+"__", v)
	}
	return content, nil
}
