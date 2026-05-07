package clawd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PasteImage saves an uploaded image to a tmp file and types its path into
// the named tmux session, where Claude reads it as a file argument. We don't
// press Enter — the user reviews and submits.
//
// On macOS Claude Code reads images from the system clipboard via osascript;
// we deliberately bypass that route because it doesn't survive the round trip
// through code-server's xterm.js. Path injection is the robust path.
func PasteImage(name string, data []byte, ext string) (string, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return "", fmt.Errorf("invalid project name")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty image")
	}

	// Sanity-check the session exists before writing anything to disk.
	// `sh()` flattens success and failure to the same empty string when there's
	// no stdout, so we use exec.Command directly to read the actual exit code.
	if err := exec.Command("tmux", append(tmuxArgs(), "has-session", "-t", name)...).Run(); err != nil {
		return "", fmt.Errorf("tmux session not running: %s", name)
	}

	dir := "/tmp/clawdstacc"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	cleanOldPastes(dir, time.Hour)

	if ext == "" {
		ext = "png"
	}
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if !regexp.MustCompile(`^[a-z0-9]{2,5}$`).MatchString(ext) {
		ext = "png"
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.%s", time.Now().UnixMilli(), ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	// Inject `<space><path>` into the session. -l = literal, no key
	// interpretation. The leading space separates back-to-back pastes; an empty
	// prompt with a leading space is harmless (Claude trims it).
	args := append(tmuxArgs(), "send-keys", "-l", "-t", name, " "+path)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux send-keys: %s: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// cleanOldPastes deletes images older than maxAge from dir. Best-effort.
func cleanOldPastes(dir string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

var pastePathRE = regexp.MustCompile(`^/api/paste/([a-zA-Z0-9_-]+)$`)

// 25 MiB is plenty for screenshots; bigger uploads are almost certainly mistakes.
const maxPasteBytes = 25 << 20

func (s *Server) handleAPIPaste(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := pastePathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	name := m[1]

	r.Body = http.MaxBytesReader(w, r.Body, maxPasteBytes)
	if err := r.ParseMultipartForm(maxPasteBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing 'image' field")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read upload: "+err.Error())
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		// Fall back from MIME type, e.g. image/png → png
		if ct := header.Header.Get("Content-Type"); strings.HasPrefix(ct, "image/") {
			ext = strings.TrimPrefix(ct, "image/")
		}
	}

	path, err := PasteImage(name, data, ext)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"path":    path,
		"bytes":   len(data),
		"project": name,
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
