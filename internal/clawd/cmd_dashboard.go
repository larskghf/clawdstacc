package clawd

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func cmdDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	defaultPort := os.Getenv("CLAWDSTACC_PORT")
	if defaultPort == "" {
		defaultPort = "8390"
	}

	confPath := fs.String("conf", defaultConfPath(), "path to clawdstacc.conf")
	addr := fs.String("addr", "0.0.0.0:"+defaultPort, "listen address")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	cfg, err := LoadConfig(*confPath)
	if err != nil {
		log.Fatalf("load config %s: %v", *confPath, err)
	}
	cfg.RepoDir = filepath.Dir(*confPath)

	srv := NewServer(cfg)
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout — SSE connections are long-lived.
	}

	log.Printf("clawdstacc dashboard on %s  (conf: %s)", *addr, *confPath)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

// defaultConfPath looks for a clawdstacc.conf in the conventional locations,
// in priority order:
//
//  1. $CLAWDSTACC_CONF (explicit override — wins over everything)
//  2. ~/.config/clawdstacc/clawdstacc.conf (XDG, recommended for brew installs)
//  3. ~/clawdstacc.conf (user-home shortcut for the install.sh bootstrap)
//  4. <binary-dir>/../clawdstacc.conf (source-tree install, sibling to bin/)
//
// Returns the first path that exists. If none exists, returns the XDG path so
// `--help` and error messages point at the recommended location.
func defaultConfPath() string {
	if c := os.Getenv("CLAWDSTACC_CONF"); c != "" {
		return c
	}
	home, _ := os.UserHomeDir()
	xdg := filepath.Join(home, ".config", "clawdstacc", "clawdstacc.conf")

	candidates := []string{
		xdg,
		filepath.Join(home, "clawdstacc.conf"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "clawdstacc.conf"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return xdg
}
