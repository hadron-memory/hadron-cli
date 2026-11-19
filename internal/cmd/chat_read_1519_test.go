package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1519 — the follow-ups from the BLIND review of #1518 (chat read's two
// independent drains). The first test is the red one: it asks the golden
// update path to overwrite a baseline from a checkout that is not the pinned
// one. The others pin merge behaviour that already holds, so they are shown
// by mutants rather than red on main.

// copyGoldens1519 copies the committed goldens into dir/testdata/chat-read-1516
// so a child run of the test binary (cwd = dir) updates the COPY, never the
// repository's baseline.
func copyGoldens1519(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join("testdata", "chat-read-1516")
	dst := filepath.Join(dir, "testdata", "chat-read-1516")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// An update request from a checkout other than the pinned baseline must not be
// able to overwrite the committed goldens: they are the recorded behaviour of
// main BEFORE the dual-drain change, and regenerating them from the current
// code would silently replace that baseline with current behaviour. Every
// spelling of the request is tried; each must leave the (copied) goldens
// byte-identical, here with a sentinel appended so an overwrite is visible.
func TestChatReadGoldenUpdateCannotOverwriteTheBaseline1519(t *testing.T) {
	differs, _ := checkoutDiffersFromBaseline1516()
	for _, request := range []string{"1", "true", baseline1516, "HEAD"} {
		t.Run(request, func(t *testing.T) {
			// The full pinned revision is the one spelling the guard ALLOWS, and
			// only from a checkout whose production code equals the baseline (the
			// original recording commit, a bisect step). There a legitimate
			// regeneration would be authorised, so this control cannot apply.
			if request == baseline1516 && !differs {
				t.Skip("this checkout equals the baseline, where the pinned revision is an authorised regeneration")
			}
			dir := t.TempDir()
			goldens := copyGoldens1519(t, dir)
			target := filepath.Join(goldens, "since0-jsonfalse.golden")
			orig, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			tampered := append(append([]byte{}, orig...), []byte("SENTINEL: an overwrite would erase this line\n")...)
			if err := os.WriteFile(target, tampered, 0o644); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(goldens)
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, e := range entries {
				b, err := os.ReadFile(filepath.Join(goldens, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				before[e.Name()] = b
			}
			if len(before) != 7 { // six goldens and PROVENANCE: the comparison below must have something to compare
				t.Fatalf("expected 7 baseline files in the copy, got %d", len(before))
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestChatReadDualDrain1516$", "-test.count=1")
			cmd.Dir = dir
			// One value for the variable: drop any the parent already exports.
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "UPDATE_1516_GOLDENS=") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			cmd.Env = append(cmd.Env, "UPDATE_1516_GOLDENS="+request)
			out, runErr := cmd.CombinedOutput()
			for name, want := range before {
				got, err := os.ReadFile(filepath.Join(goldens, name))
				if err != nil {
					t.Fatalf("%s vanished: %v", name, err)
				}
				if string(got) != string(want) {
					t.Fatalf("UPDATE_1516_GOLDENS=%s overwrote the baseline %s from a non-baseline checkout\nchild output:\n%s", request, name, out)
				}
			}
			// Unchanged bytes alone could be a child that never ran (a renamed test,
			// an exec failure): the refusal itself must be observed.
			if runErr == nil || !strings.Contains(string(out), "refusing to regenerate the 1516 goldens") {
				t.Fatalf("the child did not refuse the update request %q (err %v):\n%s", request, runErr, out)
			}
		})
	}
}

// duplicateReader serves one same-loc row in both streams, with different
// content AND envelope (and seq), so a merge that lets the qualified row win
// shows in every field. Identical duplicate rows could not tell the two apart.
func TestChatReadDuplicateLocKeepsTheUnqualifiedRow1519(t *testing.T) {
	unq := chatMsgCanonical("chats:api:messages:t5-dup", 5, "unqualified body", `{"author":"Legacy","role":"Engineer"}`)
	qual := chatMsgCanonical("chats:api:messages:t5-dup", 6, "qualified body", `{"authorName":"Qualified","sessionId":"sq","authorWorkerId":"wq"}`)
	other := chatMsg("chats:api:messages:t7-other", 7, `{"author":"Other","body":"unique"}`)
	s, calls := chatRowsReader1516(t, map[string][]string{"legacy": {unq, other}, "chat-message": {qual}}, "")

	text, err := executeChat1516(t, s.URL, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[5] Legacy (Engineer): unqualified body\n[7] Other: unique\n"; text != want {
		t.Fatalf("text transcript:\nwant %q\ngot  %q", want, text)
	}
	raw, err := executeChat1516(t, s.URL, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var dup []map[string]any
	for _, m := range doc.Messages {
		if m["loc"] == "chats:api:messages:t5-dup" {
			dup = append(dup, m)
		}
	}
	if len(dup) != 1 {
		t.Fatalf("the duplicate loc must be emitted exactly once, got %d: %s", len(dup), raw)
	}
	m := dup[0]
	if m["seq"] != float64(5) || m["author"] != "Legacy" || m["role"] != "Engineer" || m["body"] != "unqualified body" {
		t.Fatalf("the unqualified row's content and envelope must win: %v", m)
	}
	for _, leaked := range []string{"sessionId", "authorWorkerId"} {
		if _, ok := m[leaked]; ok {
			t.Fatalf("the qualified row's envelope field %q leaked into the winner: %v", leaked, m)
		}
	}
	for _, out := range []string{text, raw} {
		// "unqualified body" contains "qualified body", so strip the winner first.
		if strings.Contains(strings.ReplaceAll(out, "unqualified body", ""), "qualified body") {
			t.Fatalf("the qualified duplicate's body reached the output: %s", out)
		}
		if strings.Contains(out, "Qualified") {
			t.Fatalf("the qualified duplicate's author reached the output: %s", out)
		}
	}
	c := calls()
	if len(c["legacy"]) == 0 || len(c["chat-message"]) == 0 {
		t.Fatalf("both drains must run: %v", c)
	}
}

// Qualified-only nil-seq rows and the exact container loc. The explicit stream
// carries nil-seq rows the unqualified stream does not have, and the container
// itself. After the merge: sequenced rows ascending, then nil-seq rows in the
// concatenated-stream order (the unqualified nil-seq row first, then the
// qualified-only ones in their stream order), and the container loc absent
// from both outputs. This is the order of a stable sort over the concatenated
// streams, not a cross-stream snapshot or global ordering guarantee.
func TestChatReadQualifiedOnlyNilSeqRowsAndContainerExclusion1519(t *testing.T) {
	nilLegacy := `{"node":{"loc":"chats:api:messages:t4-old","seq":null,"content":"legacy no sequence","data":{"author":"Old"}}}`
	nilQualA := `{"node":{"loc":"chats:api:messages:t8-qa","seq":null,"content":"qualified only A","data":{"authorName":"QA","sessionId":"s8"}}}`
	nilQualB := `{"node":{"loc":"chats:api:messages:t9-qb","seq":null,"content":"qualified only B","data":{"authorName":"QB","sessionId":"s9"}}}`
	container := `{"node":{"loc":"chats:api:messages","seq":null,"content":"container body","data":null}}`
	// Similar loc that is NOT the container: it is a message and must remain.
	sibling := `{"node":{"loc":"chats:api:messages:t6-container-sibling","seq":null,"content":"sibling body","data":{"author":"Sib"}}}`
	legacySeq := chatMsg("chats:api:messages:t1-iris", 1, `{"author":"Iris","body":"first"}`)
	platformSeq := chatMsgCanonical("chats:api:messages:t2-rufus", 2, "second", `{"authorName":"Rufus","sessionId":"s1"}`)
	recordSeq := chatMsg("chats:api:messages:t3-iris", 3, `{"author":"Iris","body":"third"}`)
	rows := map[string][]string{
		"legacy":       {legacySeq, recordSeq, nilLegacy, container, sibling},
		"chat-message": {nilQualA, platformSeq, container, nilQualB},
	}

	wantText := strings.Join([]string{
		"[1] Iris: first",
		"[2] Rufus: second",
		"[3] Iris: third",
		"[?] Old: legacy no sequence",
		"[?] Sib: sibling body",
		"[?] QA: qualified only A",
		"[?] QB: qualified only B",
		"",
	}, "\n")
	s, _ := chatRowsReader1516(t, rows, "")
	text, err := executeChat1516(t, s.URL, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if text != wantText {
		t.Fatalf("merged order:\nwant %q\ngot  %q", wantText, text)
	}
	raw, err := executeChat1516(t, s.URL, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages  []map[string]any `json:"messages"`
		NextSince float64          `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var locs []string
	for _, m := range doc.Messages {
		loc, ok := m["loc"].(string)
		if !ok {
			t.Fatalf("a message has no string loc: %v", m)
		}
		locs = append(locs, loc)
	}
	wantLocs := []string{
		"chats:api:messages:t1-iris", "chats:api:messages:t2-rufus", "chats:api:messages:t3-iris",
		"chats:api:messages:t4-old", "chats:api:messages:t6-container-sibling", "chats:api:messages:t8-qa", "chats:api:messages:t9-qb",
	}
	if strings.Join(locs, ",") != strings.Join(wantLocs, ",") || doc.NextSince != 3 {
		t.Fatalf("JSON order/nextSince:\nwant %v nextSince 3\ngot  %v nextSince %v", wantLocs, locs, doc.NextSince)
	}
	for _, out := range []string{text, raw} {
		if strings.Contains(out, "container body") {
			t.Fatalf("the exact container loc reached the output: %s", out)
		}
	}
	for _, m := range doc.Messages {
		if m["loc"] == "chats:api:messages" {
			t.Fatalf("the container is a message in the JSON: %v", m)
		}
	}

	// --since keeps its behaviour: nil-seq rows (either stream) are dropped,
	// sequenced rows after it stay in order, and the container stays out.
	s2, _ := chatRowsReader1516(t, rows, "")
	since, err := executeChat1516(t, s2.URL, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[2] Rufus: second\n[3] Iris: third\n"; since != want {
		t.Fatalf("--since 1:\nwant %q\ngot  %q", want, since)
	}
}

// The committed baseline is exactly what its PROVENANCE says: it names the
// pinned revision, lists every golden, and each SHA-256 matches. A golden
// replaced without a matching, reviewed PROVENANCE change fails here.
func TestChatReadGoldenBaselineMatchesItsProvenance1519(t *testing.T) {
	dir := filepath.Join("testdata", "chat-read-1516")
	raw, err := os.ReadFile(filepath.Join(dir, "PROVENANCE"))
	if err != nil {
		t.Fatalf("the baseline has no PROVENANCE: %v", err)
	}
	revision, sums, err := parseProvenance1516(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if revision != baseline1516 {
		t.Fatalf("PROVENANCE names %s, the pinned baseline is %s", revision, baseline1516)
	}
	goldens, err := readGoldens1516(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Six is a deliberate tripwire: the since {0,1,3} x JSON {false,true} grid of
	// TestChatReadDualDrain1516. Adding a case means re-recording from the pinned
	// baseline and updating this number in the same reviewed change.
	if len(goldens) != 6 || len(sums) != len(goldens) {
		t.Fatalf("PROVENANCE lists %d goldens, %d on disk (want 6)", len(sums), len(goldens))
	}
	if got := provenance1516(revision, goldens); got != string(raw) {
		t.Fatalf("a golden differs from the recorded baseline; PROVENANCE would read:\n%s\nbut is:\n%s", got, raw)
	}
}

// The guard, as a table: only a request that names the full pinned revision,
// from code identical to it, may write; and a regeneration's PROVENANCE names
// the revision it was recorded from.
func TestChatReadGoldenUpdateGuard1519(t *testing.T) {
	for _, tc := range []struct {
		name      string
		request   string
		differs   bool
		wantAllow bool
	}{
		{"pinned revision from the baseline code", baseline1516, false, true},
		{"pinned revision from newer code", baseline1516, true, false},
		{"bare flag", "1", false, false},
		{"another revision", "0123456789abcdef0123456789abcdef01234567", false, false},
		{"short revision", baseline1516[:8], false, false},
		{"empty", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := guardGoldenUpdate1516(tc.request, tc.differs, "test")
			if (err == nil) != tc.wantAllow {
				t.Fatalf("allow=%v, got %v", tc.wantAllow, err)
			}
		})
	}
	text := provenance1516(baseline1516, map[string][]byte{"a.golden": []byte("x"), "b.golden": []byte("y")})
	revision, sums, err := parseProvenance1516(text)
	if err != nil || revision != baseline1516 || len(sums) != 2 || sums["a.golden"] == "" {
		t.Fatalf("a regeneration's provenance must name its revision and hash each golden: %q %v %v", revision, sums, err)
	}
}

// The comparison behind the guard sees the WORKING TREE and the whole
// production tree, in a throwaway repository: committed, uncommitted and
// untracked changes to production code all count; test files, testdata and
// docs do not; and anything it cannot establish fails closed.
func TestChatReadBaselineComparisonSeesTheWorkingTree1519(t *testing.T) {
	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(t *testing.T, dir, rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newRepo := func(t *testing.T) (dir, baseline string) {
		t.Helper()
		dir = t.TempDir()
		git(t, dir, "init", "-q")
		write(t, dir, "internal/cmd/chat/read.go", "package chat\n")
		write(t, dir, "internal/cmd/root.go", "package cmd\n")
		write(t, dir, "cmd/hadron/main.go", "package main\n")
		write(t, dir, "go.mod", "module m\n")
		write(t, dir, "internal/cmd/read_test.go", "package cmd\n")
		write(t, dir, "internal/cmd/testdata/g.golden", "x\n")
		write(t, dir, "docs/note.md", "doc\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "baseline")
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return dir, strings.TrimSpace(string(out))
	}
	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, dir string)
		differs bool
	}{
		{"clean tree", func(*testing.T, string) {}, false},
		{"only test files, testdata and docs change (committed)", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/read_test.go", "package cmd // changed\n")
			write(t, dir, "internal/cmd/testdata/g.golden", "y\n")
			write(t, dir, "docs/note.md", "changed\n")
			git(t, dir, "commit", "-q", "-am", "tests")
		}, false},
		{"an untracked test file or doc", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/new_test.go", "package cmd\n")
			write(t, dir, "docs/new.md", "doc\n")
		}, false},
		{"committed change to the reader", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/chat/read.go", "package chat // changed\n")
			git(t, dir, "commit", "-q", "-am", "reader")
		}, true},
		{"UNCOMMITTED edit to the reader", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/chat/read.go", "package chat // local edit\n")
		}, true},
		{"UNTRACKED production file", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/chat/extra.go", "package chat\n")
		}, true},
		{"change outside the reader (command wiring)", func(t *testing.T, dir string) {
			write(t, dir, "internal/cmd/root.go", "package cmd // wiring\n")
			git(t, dir, "commit", "-q", "-am", "wiring")
		}, true},
		{"change to the entry point", func(t *testing.T, dir string) {
			write(t, dir, "cmd/hadron/main.go", "package main // changed\n")
		}, true},
		{"change to go.mod", func(t *testing.T, dir string) {
			write(t, dir, "go.mod", "module m // changed\n")
		}, true},
		{"deleted production file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "internal/cmd/chat/read.go")); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, baseline := newRepo(t)
			tc.mutate(t, dir)
			got, detail := compareWithBaseline1516(dir, baseline, baselineCodePathspecs1516)
			if got != tc.differs {
				t.Fatalf("differs=%v (%s), want %v", got, detail, tc.differs)
			}
			if got && !strings.Contains(detail, "HEAD ") {
				t.Fatalf("a refusal must name the checkout's HEAD: %q", detail)
			}
		})
	}
	t.Run("fails closed", func(t *testing.T) {
		dir, _ := newRepo(t)
		if got, _ := compareWithBaseline1516(dir, "0123456789abcdef0123456789abcdef01234567", baselineCodePathspecs1516); !got {
			t.Fatal("a missing baseline revision must read as differing")
		}
		if got, _ := compareWithBaseline1516(filepath.Join(dir, "nope"), "HEAD", baselineCodePathspecs1516); !got {
			t.Fatal("an unreadable directory must read as differing")
		}
	})
}
