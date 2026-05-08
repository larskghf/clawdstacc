package clawd

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

type Server struct {
	cfg  Config
	tmpl *template.Template
}

func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg}
	funcs := template.FuncMap{
		"cardClass":     cardClass,
		"activeLabel":   activeLabel,
		"sessionLine":   sessionLine,
		"codeServerURL": s.codeServerURL,
		"summary":       summary,
		"modelShort":    modelShort,
		"tokenFmt":      tokenFmt,
		"costFmt":       costFmt,
		"totalTokens":   totalTokens,
	}
	s.tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(webFS, "web/*.html"))
	return s
}

// modelShort turns "claude-opus-4-7" into "Opus 4.7", "claude-sonnet-4-6" → "Sonnet 4.6".
func modelShort(m string) string {
	m = strings.TrimPrefix(m, "claude-")
	parts := strings.Split(m, "-")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	family := strings.ToUpper(parts[0][:1]) + parts[0][1:]
	if len(parts) >= 3 {
		return fmt.Sprintf("%s %s.%s", family, parts[1], parts[2])
	}
	if len(parts) == 2 {
		return fmt.Sprintf("%s %s", family, parts[1])
	}
	return family
}

func tokenFmt(n int64) string {
	switch {
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
}

func costFmt(c float64) string {
	if c < 0.01 {
		return "$0.00"
	}
	return fmt.Sprintf("$%.2f", c)
}

func totalTokens(s *SessionInfo) int64 {
	if s == nil {
		return 0
	}
	return s.InputTokens + s.OutputTokens + s.CacheReadTokens + s.CacheCreationTokens
}

type Summary struct {
	Total   int
	Ready   int // tmux + claude alive
	Idle    int // tmux alive but no claude
	Down    int // tmux dead
	Setup   int // dir exists but no launchd agent
	Running int // currently in a tool call
}

func summary(snap StatusSnapshot) Summary {
	var s Summary
	for _, p := range snap.Projects {
		s.Total++
		if !p.AgentLoaded {
			s.Setup++
		}
		switch {
		case !p.TmuxAlive:
			s.Down++
		case !p.ClaudeAlive:
			s.Idle++
		default:
			s.Ready++
		}
		if p.Session != nil && p.Session.OpenToolUse != "" {
			s.Running++
		}
	}
	return s
}

func cardClass(p ProjectStatus) string {
	switch {
	case !p.AgentLoaded:
		// No launchd agent yet — call-to-action state. The CSS gives this a
		// dashed blue border and tints the background; the actions row shows
		// the setup-cta button instead of code-server/restart.
		return "setup"
	case !p.TmuxAlive:
		return "dead"
	case p.Session != nil && p.Session.OpenToolUse != "":
		return "ok running"
	case p.ClaudeAlive:
		return "ok"
	default:
		return "warn"
	}
}

func activeLabel(p ProjectStatus) string {
	var ago int64
	switch {
	case p.Session != nil:
		ago = p.Session.ModifiedAgo
	case p.TmuxIdle != nil:
		ago = *p.TmuxIdle
	default:
		return ""
	}
	return "active " + fmtAgo(ago) + " ago"
}

func sessionLine(p ProjectStatus) string {
	if p.Session == nil {
		return "no session yet"
	}
	if p.Session.LastUserMsg != "" {
		return "> " + p.Session.LastUserMsg
	}
	if p.Session.LastToolUse != "" {
		return "last tool: " + p.Session.LastToolUse
	}
	return "session live"
}

func (s *Server) codeServerURL(_ any, projectPath string) string {
	// The redirect handler decides public-vs-LAN at request time, so the link
	// itself only needs to carry the folder.
	return "/cs-redirect?folder=" + url.QueryEscape(projectPath)
}

// cardsView wraps a StatusSnapshot with the per-request paste target so the
// `cards` template can mark the active card with the .paste-target class
// server-side. Doing this client-side caused a heartbeat flicker every 2s
// because htmx innerHTML-swaps the cards container on each SSE push and
// new (un-classed) cards painted for a frame before JS re-applied the class.
type cardsView struct {
	StatusSnapshot
	PasteTarget string
}

// pasteTargetCookie reads the user's chosen paste target out of the request.
// JS writes this cookie whenever the dropdown or a code-server button changes
// the target; server-side rendering reads it back so the .paste-target class
// is in the HTML before the browser ever paints it.
func pasteTargetCookie(r *http.Request) string {
	if r == nil {
		return ""
	}
	c, err := r.Cookie("clawdstacc-paste-target")
	if err != nil {
		return ""
	}
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return c.Value
	}
	return v
}

func (s *Server) renderCards(snap StatusSnapshot, target string) (string, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "cards", cardsView{snap, target}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) renderHeader(snap StatusSnapshot) (string, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "header-meta", snap); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleAPIStatus)
	mux.HandleFunc("/api/restart/", s.handleAPIRestart)
	mux.HandleFunc("/api/setup/", s.handleAPISetup)
	mux.HandleFunc("/api/remove/", s.handleAPIRemove)
	mux.HandleFunc("/api/paste/", s.handleAPIPaste)
	mux.HandleFunc("/sse/status", s.handleSSEStatus)
	mux.HandleFunc("/cs-redirect", s.handleCodeServerRedirect)
	mux.HandleFunc("/favicon.svg", s.handleIcon)
	mux.HandleFunc("/icon.svg", s.handleIcon)
	return mux
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	body, err := webFS.ReadFile("web/icon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(body)
}

var (
	setupPathRE  = regexp.MustCompile(`^/api/setup/([a-zA-Z0-9_-]+)$`)
	removePathRE = regexp.MustCompile(`^/api/remove/([a-zA-Z0-9_-]+)$`)
)

func (s *Server) handleAPIRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := removePathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	name := m[1]
	w.Header().Set("Content-Type", "application/json")
	if err := RemoveProject(s.cfg, name); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) handleAPISetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := setupPathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	name := m[1]
	// Find the project path matching this name.
	var found string
	for _, p := range ListProjects(s.cfg) {
		if ProjectName(p) == name {
			found = p
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if found == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no matching project dir"})
		return
	}
	if err := SetupProject(s.cfg, found); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	snap := CollectStatus(s.cfg)
	view := cardsView{StatusSnapshot: snap, PasteTarget: pasteTargetCookie(r)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index", view); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	snap := CollectStatus(s.cfg)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(snap)
}

var restartPathRE = regexp.MustCompile(`^/api/restart/([a-zA-Z0-9_-]+)$`)

func (s *Server) handleAPIRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := restartPathRE.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	name := m[1]
	w.Header().Set("Content-Type", "application/json")
	if err := RestartProject(name); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleCodeServerRedirect bounces to code-server. Two paths:
//   - public (request is HTTPS) AND CODESERVER_PUBLIC_URL is configured →
//     redirect there (e.g. https://code.example.com)
//   - else → LAN-style http://<request-host>:<bind-port>
//
// Detecting "public" via X-Forwarded-Proto rather than r.TLS, since CF Tunnel
// terminates TLS at the edge and proxies plaintext HTTP to us.
func (s *Server) handleCodeServerRedirect(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	isPublic := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	var target string
	if isPublic && s.cfg.CodeServerPublicURL != "" {
		base := strings.TrimSuffix(s.cfg.CodeServerPublicURL, "/")
		target = fmt.Sprintf("%s/?folder=%s", base, url.QueryEscape(folder))
	} else {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		port := s.cfg.CodeServerBind
		if i := strings.LastIndex(port, ":"); i >= 0 {
			port = port[i+1:]
		}
		target = fmt.Sprintf("http://%s:%s/?folder=%s", host, port, url.QueryEscape(folder))
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleSSEStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Cookies on an EventSource are fixed at connect time. JS forces a
	// reconnect when the user changes the paste target, so this captured
	// value is good for the lifetime of the connection.
	target := pasteTargetCookie(r)

	// Initial push so the client sees fresh data immediately.
	s.pushStatus(w, flusher, target)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.pushStatus(w, flusher, target)
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) pushStatus(w http.ResponseWriter, flusher http.Flusher, target string) {
	snap := CollectStatus(s.cfg)
	cards, err := s.renderCards(snap, target)
	if err != nil {
		log.Printf("render cards: %v", err)
		return
	}
	stats, err := s.renderStats(snap)
	if err != nil {
		log.Printf("render stats: %v", err)
		return
	}
	sendSSE(w, "cards", cards)
	sendSSE(w, "stats", stats)
	flusher.Flush()
}

func (s *Server) renderStats(snap StatusSnapshot) (string, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "stats", summary(snap)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// sendSSE writes a Server-Sent-Events message. Multi-line data is split into
// one `data:` line per source line (per the SSE spec).
func sendSSE(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprintf(w, "\n")
}
