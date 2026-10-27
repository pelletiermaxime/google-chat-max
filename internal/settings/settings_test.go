package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateConfig(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration-path integration is Linux-specific")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "google-chat-max", "config.json")
}

func TestConfigMissingSaveReloadAndReplace(t *testing.T) {
	path := isolateConfig(t)
	config, err := Load()
	if err != nil || config.ProjectID != "" {
		t.Fatalf("missing config: %#v %v", config, err)
	}
	for _, project := range []string{"project-one", "project-two"} {
		if err := Save(Config{ProjectID: project}); err != nil {
			t.Fatal(err)
		}
		config, err = Load()
		if err != nil || config.ProjectID != project {
			t.Fatalf("reload: %#v %v", config, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions are not private")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "secret") || strings.Contains(string(data), "token") {
		t.Fatal("unexpected configuration contents")
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("save left temporary configuration files")
	}
}

func TestInvalidConfigDoesNotReplaceSavedProject(t *testing.T) {
	path := isolateConfig(t)
	if err := Save(Config{ProjectID: "project-one"}); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"", "short", "Project-ID", "project/other", "project-", "123456", strings.Repeat("p", 31)} {
		if err := Save(Config{ProjectID: project}); err == nil {
			t.Fatalf("invalid project accepted: %q", project)
		}
	}
	config, err := Load()
	if err != nil || config.ProjectID != "project-one" {
		t.Fatal("invalid save damaged the existing project")
	}
	if err := os.WriteFile(path, []byte(`{"project_id":"project-one"} {"project_id":"project-two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("multiple configuration documents accepted")
	}
}
