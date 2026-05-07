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

// defaultConfPath returns the conventional location of clawdstacc.conf
// (sibling to the bin/ directory the binary was launched from), or
// $CLAWDSTACC_CONF if set.
func defaultConfPath() string {
	if c := os.Getenv("CLAWDSTACC_CONF"); c != "" {
		return c
	}
	exe, err := os.Executable()
	if err != nil {
		return "clawdstacc.conf"
	}
	// Repo layout: <repo>/bin/clawdstacc → conf at <repo>/clawdstacc.conf
	return filepath.Join(filepath.Dir(exe), "..", "clawdstacc.conf")
}
