package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func findGoogleTool(name string) (string, error) {
	var fallback []string
	if runtime.GOOS == "darwin" {
		// Finder does not inherit a terminal's Homebrew PATH. Prefer the
		// explicit PATH, then check the standard Apple Silicon/Intel prefixes.
		fallback = []string{"/opt/homebrew/bin", "/usr/local/bin"}
	}
	return lookupGoogleTool(name, fallback)
}

func googleToolCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, path, args...)
	if runtime.GOOS == "darwin" {
		// npm-installed CLIs may use /usr/bin/env node. Keep the caller's
		// PATH first, and make Homebrew runtimes available to that child too.
		dirs := filepath.SplitList(os.Getenv("PATH"))
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
		command.Env = append(os.Environ(), "PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	}
	return command
}

func lookupGoogleTool(name string, fallback []string) (string, error) {
	path, err := exec.LookPath(name)
	if err == nil {
		return path, nil
	}
	for _, dir := range fallback {
		if path, fallbackErr := exec.LookPath(filepath.Join(dir, name)); fallbackErr == nil {
			return path, nil
		}
	}
	return "", err
}
