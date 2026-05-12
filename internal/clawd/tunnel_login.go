package clawd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// dashboardLogin runs the interactive auth flow against a clawdstacc
// dashboard that sits behind some reverse-proxy auth (CF Access,
// oauth2-proxy, Authentik, anything). The mechanism is provider-agnostic:
//
//  1. We listen on a random localhost port and a random path-secret URL.
//  2. We open the user's browser at <dashboard>/auth/cli?cb=<localhost>.
//  3. The reverse-proxy auth in front of the dashboard catches the
//     unauthenticated request, runs whatever login UI it has, and on
//     success forwards the request to the origin (our dashboard) with the
//     user's credentials attached as cookies or as a Cf-Access-Jwt-Assertion
//     / Authorization header.
//  4. The dashboard's /auth/cli endpoint snapshots those credentials and
//     returns a self-submitting HTML form that POSTs them to our localhost
//     callback.
//  5. We parse the form, persist the credential bundle, and replay the same
//     headers on every subsequent WS dial.
//
// Bounded by ctx — Ctrl-C kills the flow cleanly without leaving a token
// file in a half-written state.
func dashboardLogin(ctx context.Context, base *url.URL) (tokenStoreEntry, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return tokenStoreEntry{}, fmt.Errorf("listen local callback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	pathSecret := randomHex(16)
	cbURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, pathSecret)

	loginURL := &url.URL{
		Scheme:   base.Scheme,
		Host:     base.Host,
		Path:     "/auth/cli",
		RawQuery: "cb=" + url.QueryEscape(cbURL),
	}

	type cbResult struct {
		entry tokenStoreEntry
		err   error
	}
	resultCh := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+pathSecret, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST expected", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
			resultCh <- cbResult{err: fmt.Errorf("callback parse: %w", err)}
			return
		}
		entry := tokenStoreEntry{
			Cookie:        strings.TrimSpace(r.PostForm.Get("cookie")),
			Authorization: strings.TrimSpace(r.PostForm.Get("authorization")),
		}
		if entry.Cookie == "" && entry.Authorization == "" {
			http.Error(w, "no credentials in callback", http.StatusBadRequest)
			resultCh <- cbResult{err: fmt.Errorf("server returned empty credentials — is the dashboard at %s actually behind an auth proxy?", base.Host)}
			return
		}
		if exp := strings.TrimSpace(r.PostForm.Get("expires_at")); exp != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z", exp); err == nil {
				entry.ExpiresAt = t
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(callbackDoneHTML))
		resultCh <- cbResult{entry: entry}
	})
	// Anything else looks like a misdirected request — don't leak the
	// existence of the path-secret.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	fmt.Printf("%s Opening browser at %s to log in…\n", blue("==>"), loginURL.String())
	if err := openBrowser(loginURL.String()); err != nil {
		fmt.Printf("%s could not open browser automatically: %v\n", yellow("·"), err)
		fmt.Printf("%s open this URL manually:\n   %s\n", yellow("·"), loginURL.String())
	}

	loginCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	select {
	case res := <-resultCh:
		if res.err != nil {
			return tokenStoreEntry{}, res.err
		}
		fmt.Printf("%s Login successful\n", green("✓"))
		return res.entry, nil
	case <-loginCtx.Done():
		if errors.Is(loginCtx.Err(), context.DeadlineExceeded) {
			return tokenStoreEntry{}, fmt.Errorf("login timed out after 5 minutes — re-run when you're ready")
		}
		return tokenStoreEntry{}, loginCtx.Err()
	}
}

const callbackDoneHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>clawdstacc — signed in</title>
<style>
  body { font: 14px -apple-system, system-ui, sans-serif;
         background: #0a0a0a; color: #e5e5e5;
         display: flex; align-items: center; justify-content: center;
         min-height: 100vh; margin: 0; }
  .card { background: #141414; border: 1px solid #2a2a2a; border-radius: 14px;
          padding: 2rem 2.5rem; max-width: 420px; text-align: center; }
  .dot { width: 8px; height: 8px; border-radius: 50%;
         background: #22c55e; display: inline-block; margin-right: 8px;
         box-shadow: 0 0 8px #22c55e; }
  h1 { font-size: 1.1rem; margin: 0 0 .6rem; font-weight: 600; }
  p  { color: #a3a3a3; margin: 0; line-height: 1.55; }
</style></head>
<body><div class="card">
  <h1><span class="dot"></span>Signed in</h1>
  <p>clawdstacc captured your auth credentials.<br>You can close this tab.</p>
</div></body></html>`

// openBrowser uses the platform's default URL opener. xdg-open / open /
// rundll32 are all "fire and forget" — we don't wait for them, we wait for
// the callback hit.
func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
