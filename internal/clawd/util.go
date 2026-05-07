package clawd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// --- ANSI colour helpers used by CLI subcommands ---

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cGray   = "\033[90m"
)

func colored(code, msg string) string {
	if !isStdoutTTY() {
		return msg
	}
	return code + msg + cReset
}

func bold(s string) string   { return colored(cBold, s) }
func red(s string) string    { return colored(cRed, s) }
func green(s string) string  { return colored(cGreen, s) }
func yellow(s string) string { return colored(cYellow, s) }
func blue(s string) string   { return colored(cBlue, s) }
func gray(s string) string   { return colored(cGray, s) }

func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// promptYN reads from /dev/tty so it works under `curl | bash`. Returns true
// for y / yes (case-insensitive). When CLAWDSTACC_YES=1, auto-confirms.
func promptYN(question string) bool {
	if os.Getenv("CLAWDSTACC_YES") == "1" {
		fmt.Println(yellow("  [auto-yes] " + question))
		return true
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		// No /dev/tty (CI, daemon, …) — be conservative, refuse.
		return false
	}
	defer tty.Close()
	fmt.Printf("%s [y/N] ", question)
	sc := bufio.NewScanner(tty)
	if !sc.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(sc.Text()))
	return ans == "y" || ans == "yes"
}

func die(format string, a ...any) {
	fmt.Fprintln(os.Stderr, red("ERROR: ")+fmt.Sprintf(format, a...))
	os.Exit(1)
}

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
