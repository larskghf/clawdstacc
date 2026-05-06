package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// sh runs a shell command and returns stdout (trimmed), or fallback on any failure.
func sh(cmd string, fallback string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", cmd).Output()
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(out))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// fmtAgo renders seconds as compact human time (s/m/h/d).
func fmtAgo(s int64) string {
	switch {
	case s < 0:
		return "?"
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dd", s/86400)
	}
}
