package clawd

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// handleAuthCLI is the server side of the `clawdstacc tunnel` interactive
// login flow. The CLI starts a localhost HTTP listener, then opens the user's
// browser at
//
//	<dashboard>/auth/cli?cb=http://127.0.0.1:<port>/<path>
//
// The browser hits this dashboard endpoint. Whatever reverse-proxy auth sits
// in front (Cloudflare Access, oauth2-proxy, Authentik, Authelia, plain
// basic-auth, …) catches an unauthenticated browser, runs its own login
// flow, and after success forwards the request to this origin handler with
// the user's credentials attached — typically as cookies, sometimes as an
// Authorization header, sometimes as a `Cf-Access-Jwt-Assertion` header in
// CF Access's case.
//
// We snapshot all three of those credential surfaces and ship them back to
// the localhost callback as an auto-submitting form POST. The CLI parses
// the form, persists the bundle, and replays the headers on every
// subsequent connection to the dashboard. This works for any reverse-proxy
// auth that accepts the same headers/cookies on the next request — i.e.
// virtually all of them.
//
// Security:
//
//   - `cb` MUST resolve to a loopback address (127.0.0.0/8 or ::1). If it
//     doesn't, we 400 — otherwise a malicious link could ship a logged-in
//     user's session to an attacker's server.
//   - The handler itself sits behind whatever auth the dashboard already
//     uses, so the request can only reach here after the user authenticated.
//   - The auto-submit page does not display the secrets — they're values of
//     hidden form fields posted once and never echoed back.
func (s *Server) handleAuthCLI(w http.ResponseWriter, r *http.Request) {
	cb := r.URL.Query().Get("cb")
	if !isLoopbackHTTP(cb) {
		http.Error(w, "cb must be a http://127.0.0.1:<port>/... or http://[::1]:<port>/... URL", http.StatusBadRequest)
		return
	}

	cookie := r.Header.Get("Cookie")
	authorization := r.Header.Get("Authorization")
	// CF Access strips the CF_Authorization cookie at the edge and exposes
	// the JWT only via this header. Reconstruct it as a cookie value so the
	// CLI can replay it on subsequent requests — the edge will accept the
	// cookie form just as well.
	if cfJWT := r.Header.Get("Cf-Access-Jwt-Assertion"); cfJWT != "" {
		cfCookie := "CF_Authorization=" + cfJWT
		if cookie == "" {
			cookie = cfCookie
		} else {
			cookie = cookie + "; " + cfCookie
		}
	}

	// expires_at is best-effort: if any of the credentials we shipped is a
	// JWT (CF_Authorization is, oauth2-proxy session cookies often are), we
	// surface its `exp` so the CLI can re-trigger the login flow proactively
	// before the next dial fails. If nothing parses, leave empty and the CLI
	// will rely on server rejection to know it's time to re-auth.
	expiresAt := earliestJWTExpiry(cookie, authorization)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := authCLITmpl.Execute(w, authCLIData{
		Callback:      cb,
		Cookie:        cookie,
		Authorization: authorization,
		ExpiresAt:     expiresAt,
	}); err != nil {
		// Best-effort — connection is probably already being closed.
		fmt.Fprintln(w, "internal error rendering auth response")
	}
}

// isLoopbackHTTP only accepts http:// URLs pointing at 127.0.0.0/8 or ::1.
// This is the gate that stops a malicious `?cb=https://evil.com/...` link
// from exfiltrating an authenticated user's session to a third party.
func isLoopbackHTTP(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// earliestJWTExpiry scans the provided credential strings, decodes any JWTs
// it finds (Cookie values shaped like `name=eyJ...` and Authorization headers
// shaped like `Bearer eyJ...`), and returns the soonest `exp` claim it can
// parse. Returns the zero-value time when nothing is decodable, which the
// CLI treats as "no advance warning, just retry on failure".
func earliestJWTExpiry(cookieHdr, authHdr string) string {
	var earliest string
	check := func(jwt string) {
		t := jwtExpiry(jwt)
		if t.IsZero() {
			return
		}
		ts := t.UTC().Format("2006-01-02T15:04:05Z")
		if earliest == "" || ts < earliest {
			earliest = ts
		}
	}
	for _, pair := range strings.Split(cookieHdr, ";") {
		pair = strings.TrimSpace(pair)
		if eq := strings.IndexByte(pair, '='); eq > 0 {
			check(pair[eq+1:])
		}
	}
	if strings.HasPrefix(strings.ToLower(authHdr), "bearer ") {
		check(strings.TrimSpace(authHdr[7:]))
	}
	return earliest
}

type authCLIData struct {
	Callback      string
	Cookie        string
	Authorization string
	ExpiresAt     string
}

var authCLITmpl = template.Must(template.New("authcli").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>clawdstacc — signing in CLI…</title>
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
<body>
  <form id="f" method="POST" action="{{.Callback}}" style="display:none">
    <input name="cookie" value="{{.Cookie}}">
    <input name="authorization" value="{{.Authorization}}">
    <input name="expires_at" value="{{.ExpiresAt}}">
  </form>
  <div class="card">
    <h1><span class="dot"></span>Signed in</h1>
    <p>Returning credentials to clawdstacc CLI…<br>You can close this tab in a moment.</p>
  </div>
  <script>document.getElementById('f').submit();</script>
</body></html>`))
