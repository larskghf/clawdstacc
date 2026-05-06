package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	defaultConf := os.Getenv("CLAWDSTACC_CONF")
	if defaultConf == "" {
		exe, err := os.Executable()
		if err == nil {
			// Repo layout: <repo>/bin/dashboard or <repo>/dashboard/dashboard.
			defaultConf = filepath.Join(filepath.Dir(exe), "..", "clawdstacc.conf")
		}
	}

	defaultPort := os.Getenv("CLAWDSTACC_PORT")
	if defaultPort == "" {
		defaultPort = "8390"
	}

	confPath := flag.String("conf", defaultConf, "path to clawdstacc.conf")
	addr := flag.String("addr", "0.0.0.0:"+defaultPort, "listen address")
	flag.Parse()

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
