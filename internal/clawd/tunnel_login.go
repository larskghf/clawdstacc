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

// cfAccessLogin runs the Cloudflare Access browser flow:
//
//  1. Generate a random path component on a localhost listener (one-shot
//     callback, never reused).
//  2. Open the user's browser at <host>/cdn-cgi/access/login/<host> with
//     redirect_url pointing at that listener.
//  3. CF Access shows whatever login UI the user has configured (E-mail PIN,
//     SSO, etc.). After auth, CF redirects the browser to the protected app
//     which then redirects to the localhost callback with the JWT in the
//     "cf_access_token" URL parameter.
//  4. Our listener captures the JWT, serves the user a small "you can close
//     this tab now" page, and shuts down.
//
// The whole thing is bounded by the supplied context so Ctrl-C kills it
// cleanly. Returns the captured JWT or an error explaining why the flow
// failed (browser couldn't open, callback never hit, etc.).
func cfAccessLogin(ctx context.Context, base *url.URL) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen local callback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// Random path so a stray request to 127.0.0.1:<port> can't pose as the
	// callback. Not security-critical (the listener only lives a few minutes)
	// but cleaner.
	pathSecret := randomHex(16)
	cbURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, pathSecret)

	loginURL := &url.URL{
		Scheme:   base.Scheme,
		Host:     base.Host,
		Path:     "/cdn-cgi/access/login/" + base.Host,
		RawQuery: "redirect_url=" + url.QueryEscape(cbURL),
	}

	type cbResult struct {
		jwt string
		err error
	}
	resultCh := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+pathSecret, func(w http.ResponseWriter, r *http.Request) {
		// CF Access encodes the JWT in different params depending on the
		// account/setup. Check the documented ones in order.
		jwt := firstQueryParam(r.URL, "cf_access_token", "jwt", "token")
		if jwt == "" {
			http.Error(w, "no token in callback", http.StatusBadRequest)
			resultCh <- cbResult{err: fmt.Errorf("callback hit without a token (params: %v)", r.URL.Query())}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(callbackSuccessHTML))
		resultCh <- cbResult{jwt: jwt}
	})
	// Anything that doesn't match the secret path is a misdirected request —
	// don't leak that the path exists by returning anything useful.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	fmt.Printf("%s Opening browser for %s login at %s\n", blue("==>"), base.Host, loginURL.String())
	if err := openBrowser(loginURL.String()); err != nil {
		// Browser open failed (headless SSH, no display, etc.) — print the
		// URL so the user can copy it onto another device.
		fmt.Printf("%s could not open browser automatically: %v\n", yellow("·"), err)
		fmt.Printf("%s open this URL manually:\n   %s\n", yellow("·"), loginURL.String())
	}

	// Bound the wait so a user who closes the tab without logging in doesn't
	// leave us hanging forever.
	loginCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", res.err
		}
		fmt.Printf("%s Login successful\n", green("✓"))
		return res.jwt, nil
	case <-loginCtx.Done():
		if errors.Is(loginCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("login timed out after 5 minutes — re-run when you're ready")
		}
		return "", loginCtx.Err()
	}
}

const callbackSuccessHTML = `<!DOCTYPE html>
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
  <p>clawdstacc captured your auth token.<br>You can close this tab.</p>
</div></body></html>`

// openBrowser uses the platform's "open this URL" tool. The browser handles
// the actual rendering and redirect chain.
func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default: // linux + bsds
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a non-cryptographic-but-still-fine value rather than
		// abort the whole login flow.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func firstQueryParam(u *url.URL, names ...string) string {
	q := u.Query()
	for _, n := range names {
		if v := strings.TrimSpace(q.Get(n)); v != "" {
			return v
		}
	}
	return ""
}
