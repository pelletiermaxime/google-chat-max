package chat

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGoogleToolLookupPreservesPATHAndFindsExecutableFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses Unix executable permissions")
	}
	pathDir, fallback := t.TempDir(), t.TempDir()
	t.Setenv("PATH", pathDir)
	expected := filepath.Join(fallback, "gws")
	if err := os.WriteFile(expected, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupGoogleTool("gws", []string{fallback}); err == nil {
		t.Fatal("a non-executable file was accepted as a CLI")
	}
	if err := os.Chmod(expected, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lookupGoogleTool("gws", []string{fallback}); err != nil || got != expected {
		t.Fatalf("fallback tool not found: %q, %v", got, err)
	}
	override := filepath.Join(pathDir, "gws")
	if err := os.WriteFile(override, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lookupGoogleTool("gws", []string{fallback}); err != nil || got != override {
		t.Fatalf("explicit PATH did not take precedence: %q, %v", got, err)
	}
}
