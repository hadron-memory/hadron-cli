package modelreport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nid = "11111111-2222-3333-4444-555555555555"

func claudeLine(model string, extra string) string {
	return `{"type":"assistant","sessionId":"` + nid + `",` + extra + `"message":{"model":"` + model + `","role":"assistant"}}` + "\n"
}

func codexMeta(id, cwd string) string {
	return `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}` + "\n"
}

func codexTurn(model string) string {
	return `{"type":"turn_context","payload":{"model":"` + model + `"}}` + "\n"
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func opts(root, p string) Options {
	return Options{
		Recorded: p,
		Claim:    Claim{SessionID: nid, TranscriptPath: p, Cwd: "/work/tree"},
		Roots:    []string{root},
	}
}

func wantUnavailable(t *testing.T, got string, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) || got != "" {
		t.Fatalf("want unavailable, got %q, %v", got, err)
	}
}

func TestClaudeNewestAssistantWins(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("claude-opus-5-5", "")+claudeLine("claude-sonnet-5-5", ""))
	got, err := Read(opts(root, p))
	if err != nil || got != "claude-sonnet-5-5" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestClaudeSidechainAndSyntheticSkipped(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("claude-opus-5-5", "")+
		claudeLine("claude-haiku-5-5", `"isSidechain":true,`)+claudeLine("<synthetic>", ""))
	got, err := Read(opts(root, p))
	if err != nil || got != "claude-opus-5-5" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCodexTurnContext(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "r.jsonl", codexMeta(nid, "/work/tree")+codexTurn("gpt-a")+codexTurn("gpt-b"))
	got, err := Read(opts(root, p))
	if err != nil || got != "gpt-b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCodexCwdMismatchUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "r.jsonl", codexMeta(nid, "/elsewhere")+codexTurn("gpt-b"))
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestNativeIDMismatchUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", strings.Replace(claudeLine("claude-opus-5-5", ""), nid, "other-native", 1))
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
	p2 := write(t, root, "r.jsonl", codexMeta("other-native", "/work/tree")+codexTurn("gpt-b"))
	got, err = Read(opts(root, p2))
	wantUnavailable(t, got, err)
}

func TestNoHeaderIdentityUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", `{"type":"assistant","message":{"model":"m"}}`+"\n")
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestMissingRecordedUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m", ""))
	o := opts(root, p)
	o.Recorded = ""
	got, err := Read(o)
	wantUnavailable(t, got, err)
}

func TestHookPathNotRecordedPathUnavailable(t *testing.T) {
	root := t.TempDir()
	rec := write(t, root, "a.jsonl", claudeLine("m", ""))
	other := write(t, root, "b.jsonl", claudeLine("m", ""))
	o := opts(root, rec)
	o.Claim.TranscriptPath = other
	got, err := Read(o)
	wantUnavailable(t, got, err)
}

func TestSymlinkedHookPathResolvingToRecordedIsAccepted(t *testing.T) {
	root := t.TempDir()
	rec := write(t, root, "a.jsonl", claudeLine("m1", ""))
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(rec, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	o := opts(root, rec)
	o.Claim.TranscriptPath = link
	if got, err := Read(o); err != nil || got != "m1" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRecordedPathOutsideRootsUnavailable(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	p := write(t, other, "a.jsonl", claudeLine("m", ""))
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestRootPrefixSiblingRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sib := filepath.Join(base, "proj-evil")
	for _, d := range []string{root, sib} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := write(t, sib, "a.jsonl", claudeLine("m", ""))
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestSymlinkRecordedPointingOutsideRootUnavailable(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	target := write(t, other, "a.jsonl", claudeLine("m", ""))
	link := filepath.Join(root, "l.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	got, err := Read(opts(root, link))
	wantUnavailable(t, got, err)
}

func TestNotRegularFileUnavailable(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "dir.jsonl")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := Read(opts(root, d))
	wantUnavailable(t, got, err)
}

func TestPartialFinalLineIgnored(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m-old", "")+`{"type":"assistant","sessionId":"`+nid+`","message":{"model":"m-ne`)
	got, err := Read(opts(root, p))
	if err != nil || got != "m-old" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestMalformedLineSkipped(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m-old", "")+"not json\n")
	got, err := Read(opts(root, p))
	if err != nil || got != "m-old" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNoModelRecordUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", `{"type":"user","sessionId":"`+nid+`"}`+"\n")
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestOversizedRecordUnavailable(t *testing.T) {
	root := t.TempDir()
	big := `{"type":"user","sessionId":"` + nid + `","pad":"` + strings.Repeat("x", maxRecord+10) + `"}` + "\n"
	p := write(t, root, "a.jsonl", claudeLine("m", "")+big)
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestModelOutsideTailWindowUnavailable(t *testing.T) {
	root := t.TempDir()
	filler := `{"type":"user","sessionId":"` + nid + `","pad":"` + strings.Repeat("x", 1000) + `"}` + "\n"
	body := claudeLine("m-old", "") + strings.Repeat(filler, maxTail/len(filler)+5)
	p := write(t, root, "a.jsonl", body)
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestInvalidModelStringUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine(strings.Repeat("a", 129), ""))
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
	p2 := write(t, root, "b.jsonl", `{"type":"assistant","sessionId":"`+nid+`","message":{"model":"a\u0007b"}}`+"\n")
	got, err = Read(opts(root, p2))
	wantUnavailable(t, got, err)
}

func TestValidModel(t *testing.T) {
	for s, want := range map[string]bool{"claude-opus-5-5": true, "gpt-5.1-codex": true, "": false, " x": false, "a\nb": false, strings.Repeat("a", 129): false, strings.Repeat("a", 128): true} {
		if ValidModel(s) != want {
			t.Errorf("ValidModel(%q) = %v", s, !want)
		}
	}
}

func TestReplacedPathAfterCanonicalizationUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m1", ""))
	swapped := write(t, root, "b.jsonl", claudeLine("m-attacker", ""))
	afterCanonical = func() {
		if err := os.Rename(swapped, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterCanonical = nil })
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestReplacedPathAfterReadUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m1", ""))
	swapped := write(t, root, "b.jsonl", claudeLine("m2", ""))
	afterRead = func() {
		if err := os.Rename(swapped, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterRead = nil })
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestTruncationDuringReadUnavailable(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.jsonl", claudeLine("m1", "")+claudeLine("m2", ""))
	afterRead = func() {
		if err := os.Truncate(p, 10); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterRead = nil })
	got, err := Read(opts(root, p))
	wantUnavailable(t, got, err)
}

func TestDiagnosticsCarryNoPath(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "secret-name.jsonl", `{"type":"user"}`+"\n")
	_, err := Read(opts(root, p))
	if err == nil || strings.Contains(err.Error(), "secret-name") || strings.Contains(err.Error(), root) {
		t.Fatalf("error leaks path: %v", err)
	}
}
