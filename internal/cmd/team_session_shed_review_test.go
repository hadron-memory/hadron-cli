package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hadron-cli#952 review findings on PR #1132: the recovery prompt's catch-up
// starts at the READ CURSOR, a bad transcript path degrades, and a failed claim
// is never silent.

func TestSessionShedPromptStartsAtTheReadCursorNotTheHead(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	out, _, err := runShed(t, url, "--print-prompt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chat read --since 41432 --all") || strings.Contains(out, "41435") {
		t.Errorf("catch-up must start at the cursor #41432, never the head #41435:\n%s", out)
	}
}

func TestSessionShedPromptWithoutAValidCursorInventsNoWatermark(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, map[string]string{"CheckChatCursor": `{"errors":[{"message":"denied"}]}`})
	out, _, err := runShed(t, url, "--print-prompt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "41435") || strings.Contains(out, "<watermark>") || strings.Contains(out, "--since") {
		t.Errorf("an unreadable cursor must not become the head or a placeholder:\n%s", out)
	}
	if !strings.Contains(out, "chat read --all") {
		t.Errorf("the catch-up step must remain, from the saved cursor:\n%s", out)
	}
}

func TestSessionShedAutoClaimFailureIsNotSilent(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, map[string]string{"CreateResumeClaim": `{"errors":[{"message":"boom"}]}`})
	out, errOut, err := runShed(t, url, "--auto")
	if err != nil {
		t.Fatalf("--auto never fails: %v", err)
	}
	if out != "" {
		t.Errorf("--auto prints nothing on stdout without --json: %q", out)
	}
	if !strings.Contains(errOut, "resume claim could not be written") {
		t.Errorf("a failed claim must leave one stderr line, got %q", errOut)
	}
}

func TestSessionShedTranscriptPartialOversizedLineIsDropped(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("0123456789", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runShed(t, url, "--transcript", path); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Input struct{ Content string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	if strings.Contains(in.Input.Content, "0123456789012345678901234567890123456789") {
		t.Errorf("a window that starts mid-line with no newline must not be embedded:\n%.200s", in.Input.Content)
	}
	if rig.count("CreateResumeClaim") != 1 {
		t.Errorf("the shed still completes: %v", rig.calls)
	}
}
