package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAuthCheckMissingToolsAndSavedLogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a shell executable")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	// Keep this fixture independent of real tools in macOS Homebrew prefixes.
	status := checkAuth(context.Background(), exec.LookPath)
	if status.GWSInstalled || status.LoginFound {
		t.Fatal("missing tools were reported ready")
	}
	script := "#!/bin/sh\nprintf '%s' '{\"auth_method\":\"oauth2\",\"client_config_exists\":true,\"has_refresh_token\":true,\"project_id\":\"project-one\",\"client_id\":\"private-metadata\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "gws"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	status = checkAuth(context.Background(), exec.LookPath)
	if !status.GWSInstalled || !status.LoginFound || !status.ClientConfigured || status.SuggestedProject != "project-one" || status.GCloudInstalled {
		t.Fatalf("saved login status not detected: %#v", status)
	}
	if err := os.WriteFile(filepath.Join(dir, "gws"), []byte("#!/bin/sh\necho private-raw-error >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	status = checkAuth(context.Background(), exec.LookPath)
	if status.Error == "" || strings.Contains(status.Error, "private-raw-error") {
		t.Fatal("authentication errors exposed raw CLI output")
	}
}

func TestOpenUsesSelectedProjectAndKeepsCredentialsInMemory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a shell executable")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("GOOGLE_WORKSPACE_CLI_CONFIG_DIR", dir)
	script := "#!/bin/sh\nprintf '%s' '{\"client_id\":\"fixture-client\",\"client_secret\":\"fixture-secret\",\"refresh_token\":\"fixture-refresh\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "gws"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := Open(context.Background(), "project-two")
	if err != nil || c.project != "project-two" {
		t.Fatalf("selected project not passed to client: %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("opening the client wrote credentials to disk")
	}
}
