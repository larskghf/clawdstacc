package clawd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// TunnelPort declares a single port on the dashboard host that should be
// reachable on a client's localhost when `clawdstacc tunnel <url>` is running.
type TunnelPort struct {
	Port    int    `json:"port"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
}

// TunnelConfig is the persisted port set, edited via the dashboard UI and
// fetched by tunnel clients. Lives at ~/.config/clawdstacc/tunnel.json.
type TunnelConfig struct {
	Ports []TunnelPort `json:"ports"`
}

// TunnelStore is the thread-safe in-memory + on-disk handle a Server holds
// onto. The dashboard mutates it via HTTP, the WebSocket data plane reads it
// to gate incoming streams, the client subcommand pulls it via the JSON API.
type TunnelStore struct {
	mu   sync.RWMutex
	path string
	cfg  TunnelConfig
}

func tunnelConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clawdstacc", "tunnel.json")
}

// LoadTunnelStore opens (or creates) the on-disk config. A missing file is
// not an error — empty config is the natural starting state.
func LoadTunnelStore() (*TunnelStore, error) {
	s := &TunnelStore{path: tunnelConfigPath()}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return s, nil
}

// Get returns a defensive copy of the current config.
func (s *TunnelStore) Get() TunnelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := TunnelConfig{Ports: make([]TunnelPort, len(s.cfg.Ports))}
	copy(out.Ports, s.cfg.Ports)
	return out
}

// PortEnabled is the fast path the WebSocket handler uses to validate that a
// requested port is in the whitelist.
func (s *TunnelStore) PortEnabled(port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.cfg.Ports {
		if p.Port == port {
			return p.Enabled
		}
	}
	return false
}

// Replace overwrites the full port list and persists. Sorting by port keeps
// the dashboard's rendering stable.
func (s *TunnelStore) Replace(ports []TunnelPort) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deduplicate by port (last write wins). Drops anything <= 0 or > 65535.
	byPort := make(map[int]TunnelPort, len(ports))
	for _, p := range ports {
		if p.Port <= 0 || p.Port > 65535 {
			continue
		}
		byPort[p.Port] = p
	}
	s.cfg.Ports = s.cfg.Ports[:0]
	for _, p := range byPort {
		s.cfg.Ports = append(s.cfg.Ports, p)
	}
	sort.Slice(s.cfg.Ports, func(i, j int) bool { return s.cfg.Ports[i].Port < s.cfg.Ports[j].Port })
	return s.persist()
}

// Upsert adds or updates a single port without touching the rest.
func (s *TunnelStore) Upsert(p TunnelPort) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("port out of range: %d", p.Port)
	}
	for i := range s.cfg.Ports {
		if s.cfg.Ports[i].Port == p.Port {
			s.cfg.Ports[i] = p
			return s.persist()
		}
	}
	s.cfg.Ports = append(s.cfg.Ports, p)
	sort.Slice(s.cfg.Ports, func(i, j int) bool { return s.cfg.Ports[i].Port < s.cfg.Ports[j].Port })
	return s.persist()
}

// Delete removes a port from the list.
func (s *TunnelStore) Delete(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Ports {
		if s.cfg.Ports[i].Port == port {
			s.cfg.Ports = append(s.cfg.Ports[:i], s.cfg.Ports[i+1:]...)
			return s.persist()
		}
	}
	return nil
}

// persist writes the JSON atomically via tmpfile + rename. Caller holds the
// write lock.
func (s *TunnelStore) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tunnel.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create tmpfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.cfg); err != nil {
		tmp.Close()
		return fmt.Errorf("encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmpfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmp.Name(), s.path, err)
	}
	return nil
}

// NotifyCh and Subscribe let the WebSocket handler push config changes to
// connected clients without polling. Each subscriber owns a 1-slot buffered
// channel; if the slot is full we drop (client will pick up the next change).
type tunnelSub struct {
	ch chan TunnelConfig
}

// subscribers list is intentionally minimal — order doesn't matter, churn is
// rare (a handful of long-lived tunnel clients per dashboard).
var (
	subsMu sync.Mutex
	subs   = make(map[*tunnelSub]struct{})
)

// Subscribe registers for config-change notifications. Caller must call the
// returned unsubscribe func when done. Buffered to 1; if a notification is
// already pending the new one is coalesced (we only care about latest state).
func (s *TunnelStore) Subscribe() (<-chan TunnelConfig, func()) {
	sub := &tunnelSub{ch: make(chan TunnelConfig, 1)}
	subsMu.Lock()
	subs[sub] = struct{}{}
	subsMu.Unlock()
	unsub := func() {
		subsMu.Lock()
		delete(subs, sub)
		subsMu.Unlock()
		close(sub.ch)
	}
	return sub.ch, unsub
}

// Notify broadcasts the current config to all subscribers. Cheap; called
// from the API handlers after every mutation.
func (s *TunnelStore) Notify() {
	cfg := s.Get()
	subsMu.Lock()
	defer subsMu.Unlock()
	for sub := range subs {
		select {
		case sub.ch <- cfg:
		default:
			// Slot full — the subscriber already has an unread update;
			// it'll pick up the latest state when it drains.
		}
	}
}
