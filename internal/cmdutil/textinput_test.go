package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTextInputInline(t *testing.T) {
	got, err := ResolveTextInput("abstract", "hello", "", nil)
	if err != nil || got != "hello" {
		t.Fatalf("inline = %q, err = %v", got, err)
	}
}

func TestResolveTextInputFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTextInput("abstract", "", path, nil)
	if err != nil || got != "from file\n" {
		t.Fatalf("file = %q, err = %v", got, err)
	}
}

func TestResolveTextInputStdin(t *testing.T) {
	got, err := ResolveTextInput("abstract", "-", "", strings.NewReader("piped"))
	if err != nil || got != "piped" {
		t.Fatalf("stdin = %q, err = %v", got, err)
	}
}

func TestResolveTextInputInlineAndFileConflict(t *testing.T) {
	_, err := ResolveTextInput("abstract", "x", "/tmp/whatever", nil)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusion error, got %v", err)
	}
}

func TestResolveTextInputMissingFile(t *testing.T) {
	_, err := ResolveTextInput("abstract", "", filepath.Join(t.TempDir(), "nope.md"), nil)
	if err == nil || !strings.Contains(err.Error(), "reading --abstract-file") {
		t.Fatalf("expected a read error naming the flag, got %v", err)
	}
}

func TestResolveTextInputEmpty(t *testing.T) {
	// No inline value, no file, not stdin: an empty result (the caller decides
	// whether that means "clear" or "leave unset").
	got, err := ResolveTextInput("abstract", "", "", nil)
	if err != nil || got != "" {
		t.Fatalf("empty = %q, err = %v", got, err)
	}
}
