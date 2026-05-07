package clawd

import (
	"path/filepath"
	"sort"
	"strings"
)

func ListProjects(cfg Config) []string {
	var paths []string
	if len(cfg.ExplicitProjects) > 0 {
		paths = cfg.ExplicitProjects
	} else {
		matches, _ := filepath.Glob(cfg.ProjectsGlob)
		paths = matches
	}
	var dirs []string
	for _, p := range paths {
		if isDir(p) {
			dirs = append(dirs, p)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func ProjectName(path string) string {
	return strings.TrimPrefix(filepath.Base(path), "_")
}
