package clawd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// tokenStoreEntry is what we persist per dashboard host. JWT is opaque from
// our perspective — we only parse the `exp` claim to know when to re-auth.
type tokenStoreEntry struct {
	JWT       string    `json:"jwt"`
	ExpiresAt time.Time `json:"expires_at"`
}

// tokenStore mediates concurrent access to ~/.config/clawdstacc/tokens.json.
// Used only on the client side (cmd_tunnel.go); not part of the server.
type tokenStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]tokenStoreEntry
}

func tokenStorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clawdstacc", "tokens.json")
}

func loadTokenStore() *tokenStore {
	s := &tokenStore{path: tokenStorePath(), entries: map[string]tokenStoreEntry{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s // missing/unreadable file → empty store
	}
	_ = json.Unmarshal(data, &s.entries)
	return s
}

// Get returns the cached token for a host, ok=false if missing or expired.
// A 60s safety margin against clock skew means "almost-expired" counts as
// expired — we'd rather re-login a minute early than fail mid-session.
func (s *tokenStore) Get(host string) (tokenStoreEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[host]
	if !ok {
		return tokenStoreEntry{}, false
	}
	if !e.ExpiresAt.IsZero() && time.Now().Add(60*time.Second).After(e.ExpiresAt) {
		return tokenStoreEntry{}, false
	}
	return e, true
}

// Put stores or replaces the entry for a host and persists.
func (s *tokenStore) Put(host string, e tokenStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[host] = e
	return s.persist()
}

// Delete removes the entry for a host (called when the server rejects the
// token even though it looked valid locally — usually means it was revoked
// or the team's signing keys rotated).
func (s *tokenStore) Delete(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[host]; !ok {
		return nil
	}
	delete(s.entries, host)
	return s.persist()
}

func (s *tokenStore) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tokens.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create tmpfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		// Best-effort; on filesystems that don't support mode this is OK.
		_ = err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.entries); err != nil {
		tmp.Close()
		return fmt.Errorf("encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmpfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmp.Name(), s.path, err)
	}
	// Re-chmod after rename in case the source mode didn't propagate.
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// jwtExpiry decodes a JWT's payload and returns the `exp` claim as time.Time.
// We do NOT verify the signature — that's the auth provider's job. We just
// need to know when our cached copy stops being useful. Zero time means we
// couldn't determine an expiry (treat as "use it once, refresh next time").
func jwtExpiry(jwt string) time.Time {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some encoders pad with '=' — try the padded variant too.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	if claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0).UTC()
}

// errNoToken is returned by tokenStore.Get when the host has no cached entry.
// Exported only for parallelism with other clawd errors; callers can use the
// bool return instead.
var errNoToken = errors.New("no cached token for host")
