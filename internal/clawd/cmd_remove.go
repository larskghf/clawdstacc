package clawd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdRemove is the CLI counterpart of the dashboard's `/api/remove/<name>`:
// stops the launchd agent for a project and removes generated files. Leaves
// the project directory and Claude history alone — those are user data.
func cmdRemove(args []string) {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	confPath := fs.String("conf", defaultConfPath(), "path to clawdstacc.conf")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: clawdstacc remove <project-name>")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Stops the launchd agent for a project, deletes the plist, kills the")
		fmt.Fprintln(fs.Output(), "tmux session, and removes generated .vscode/{tasks,settings}.json files.")
		fmt.Fprintln(fs.Output(), "The project directory and Claude conversation history are left intact.")
		fmt.Fprintln(fs.Output())
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := fs.Arg(0)

	cfg, err := LoadConfig(*confPath)
	if err != nil {
		die("load config %s: %v", *confPath, err)
	}
	cfg.RepoDir = filepath.Dir(*confPath)

	if err := RemoveProject(cfg, name); err != nil {
		die("remove %s: %v", name, err)
	}
	fmt.Printf("%s Removed clawdstacc agent for %q\n", green("✓"), name)
	fmt.Println(gray("  Project files and Claude history were not touched."))
}
