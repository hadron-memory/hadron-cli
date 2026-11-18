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
	for _, request := range []string{"1", "true", "cbe7dd3203a86ccbac893e49c14ca2082b6d19a9", "HEAD"} {
		t.Run(request, func(t *testing.T) {
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
			before := map[string][]byte{}
			entries, _ := os.ReadDir(goldens)
			for _, e := range entries {
				b, _ := os.ReadFile(filepath.Join(goldens, e.Name()))
				before[e.Name()] = b
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestChatReadDualDrain1516$", "-test.count=1")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "UPDATE_1516_GOLDENS="+request)
			out, _ := cmd.CombinedOutput() // a refusal exits non-zero; what matters is the bytes
			for name, want := range before {
				got, err := os.ReadFile(filepath.Join(goldens, name))
				if err != nil {
					t.Fatalf("%s vanished: %v", name, err)
				}
				if string(got) != string(want) {
					t.Fatalf("UPDATE_1516_GOLDENS=%s overwrote the baseline %s from a non-baseline checkout\nchild output:\n%s", request, name, out)
				}
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
		if strings.Contains(out, "qualified body") && !strings.Contains(out, "unqualified body") || strings.Contains(strings.ReplaceAll(out, "unqualified body", ""), "qualified body") || strings.Contains(out, "Qualified") {
			t.Fatalf("the qualified duplicate reached the output: %s", out)
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
		locs = append(locs, m["loc"].(string))
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
