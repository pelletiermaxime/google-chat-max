package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalKeepsOriginalProjectWhenConfigurationChanges(t *testing.T) {
	first, err := newResources("project-one")
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(t.TempDir(), "session.json")
	if err := saveResources(journal, first); err != nil {
		t.Fatal(err)
	}
	second, err := newResources("project-two")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := loadResources(journal)
	if err != nil || saved.Project != "project-one" || !strings.HasPrefix(saved.topic(), "projects/project-one/") || !strings.HasPrefix(second.queue(), "projects/project-two/") {
		t.Fatal("resource ownership followed the new configuration instead of the saved project")
	}
}

func TestJournalRequiresExplicitOriginalProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	original := []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResources(path); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatal("projectless journal was accepted or lacks recovery guidance")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatal("rejected journal was modified")
	}
	// Watch must reject unknown ownership before making any cleanup request,
	// even when the currently configured project is valid.
	t.Setenv("XDG_STATE_HOME", filepath.Dir(path))
	journal, err := streamJournalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, original, 0600); err != nil {
		t.Fatal(err)
	}
	c := &Client{project: "project-two"}
	if err := c.Watch(context.Background(), func(StreamEvent) bool { t.Fatal("unknown-ownership Watch emitted events"); return true }); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatal("Watch did not stop before cloud setup/cleanup")
	}
}

func TestInvalidProjectCannotStartLiveResources(t *testing.T) {
	if _, err := newResources(""); err == nil {
		t.Fatal("unconfigured live resources accepted")
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","project":"project/invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResources(path); err == nil {
		t.Fatal("unsafe project in journal accepted")
	}
	c := &Client{}
	if err := c.Watch(context.Background(), func(StreamEvent) bool { t.Fatal("unconfigured Watch emitted events"); return true }); err == nil {
		t.Fatal("unconfigured client started live setup")
	}
}
