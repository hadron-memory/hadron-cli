package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#738 — `spec edit` saves are GUARDED: the write carries the revision the
// proposal was computed against as expectedRevision, and the server refuses
// the proposed edit if the spec has changed since. It may record drift while
// refusing. No unguarded path exists.

// guardMocks is editMocks as a queue: the spec is stored at revision 7 with a
// Mustache body, and the write answer is chosen per test.
func guardMocks(write string) map[string][]string {
	body, _ := json.Marshal("# msg:010:02 — W2\n\nHello {{name}}.\n")
	node := `{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2",` +
		`"tags":["spec"],"role":null,"content":` + string(body) + `,"abstract":null,"abstractOriginHash":null,"revision":7}}}`
	m := map[string][]string{
		"ResolveUrn":         {resolveSpecJSON},
		"GetSpecNodeForEdit": {node},
	}
	if write != "" {
		m["UpdateSpecNode"] = []string{write}
	}
	return m
}

const guardWriteOK = `{"data":{"updateSpecNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec"],"updatedAt":"2026-09-26T00:00:00Z"}}}`

func sentRevision(t *testing.T, vars json.RawMessage) *int {
	t.Helper()
	var v struct {
		Input struct {
			ExpectedRevision *int `json:"expectedRevision"`
		} `json:"input"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input.ExpectedRevision
}

func sentNodeID(t *testing.T, vars json.RawMessage) (id *string, memoryID *string, loc *string) {
	t.Helper()
	var v struct {
		Input struct {
			ID       *string `json:"id"`
			MemoryID *string `json:"memoryId"`
			Loc      *string `json:"loc"`
		} `json:"input"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input.ID, v.Input.MemoryID, v.Input.Loc
}

func runSpecEdit(t *testing.T, mocks map[string][]string, stdin string, args ...string) (string, map[string][]json.RawMessage, error) {
	t.Helper()
	gql, captured := queueGraphQL(t, mocks)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "edit", "msg:010:02", "-m", specMem, "--server", gql.URL}, args...))
	err := root.Execute()
	return out.String(), captured, err
}

func previewProposalHash(t *testing.T, body string) string {
	t.Helper()
	out, _, err := runSpecEdit(t, guardMocks(""), body, "--content", "-", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		ProposalHash string `json:"proposalHash"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(preview.ProposalHash, "sha256:") {
		t.Fatalf("invalid proposal hash: %q", preview.ProposalHash)
	}
	return preview.ProposalHash
}

// A read-then-save in one run is guarded by the revision of THAT read.
func TestSpecEditSaveIsGuardedByTheReadRevision(t *testing.T) {
	_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# new body\n", "--content", "-")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if n := len(captured["UpdateSpecNode"]); n != 1 {
		t.Fatalf("want exactly one write, got %d", n)
	}
	if rev := sentRevision(t, captured["UpdateSpecNode"][0]); rev == nil || *rev != 7 {
		t.Errorf("the save must send expectedRevision 7 (the revision read with the body), got %v", rev)
	}
	if id, mem, loc := sentNodeID(t, captured["UpdateSpecNode"][0]); id == nil || *id != "sp1" || mem != nil || loc != nil {
		t.Errorf("the save must select the read node ID alone, got id=%v memoryId=%v loc=%v", id, mem, loc)
	}
}

// The ID/revision/hash triple carries an EARLIER preview's identity and write.
func TestSpecEditExpectedRevisionThatStillMatchesSaves(t *testing.T) {
	hash := previewProposalHash(t, "# new body\n")
	_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# new body\n", "--content", "-", "--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rev := sentRevision(t, captured["UpdateSpecNode"][0]); rev == nil || *rev != 7 {
		t.Errorf("expectedRevision = %v, want 7", rev)
	}
}

func TestSpecEditRejectsReplacementAtSameCitationAndRevision(t *testing.T) {
	hash := previewProposalHash(t, "# new body\n")
	m := guardMocks(guardWriteOK)
	m["GetSpecNodeForEdit"][0] = strings.Replace(m["GetSpecNodeForEdit"][0], `"id":"sp1"`, `"id":"sp2"`, 1)
	_, captured, err := runSpecEdit(t, m, "# new body\n", "--content", "-", "--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("replacement at the same revision: exit = %d, want 5 (%v)", got, err)
	}
	if _, wrote := captured["UpdateSpecNode"]; wrote {
		t.Error("replacement node must not receive the earlier approval")
	}
}

// A proposal approved against revision 5 must not be saved over revision 7:
// refused with exit 5 before any write — dry run included, since a preview of
// a stale proposal would be approved against text that is no longer there.
func TestSpecEditStaleExpectedRevisionIsRefusedBeforeWriting(t *testing.T) {
	hash := previewProposalHash(t, "# new body\n")
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		args := append([]string{"--content", "-", "--expected-revision", "5", "--expected-node-id", "sp1", "--expected-proposal-hash", hash}, extra...)
		_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# new body\n", args...)
		if got := exitCodeFor(err); got != exitcode.Conflict {
			t.Errorf("args %v: exit = %d, want %d (%v)", args, got, exitcode.Conflict, err)
		}
		if _, wrote := captured["UpdateSpecNode"]; wrote {
			t.Errorf("args %v: a stale proposal must not be written", args)
		}
		if err != nil && !strings.Contains(err.Error(), "approved again") {
			t.Errorf("the refusal must say to reconcile and re-approve, got: %v", err)
		}
	}
}

func TestSpecEditRejectsChangedProposalAtSameNodeRevision(t *testing.T) {
	hash := previewProposalHash(t, "# approved body\n")
	_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# changed body\n", "--content", "-", "--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("changed proposal: exit = %d, want 5 (%v)", got, err)
	}
	if _, wrote := captured["UpdateSpecNode"]; wrote {
		t.Error("a changed proposal must not inherit the prior approval")
	}
}

func TestSpecEditRejectsContentFileChangedAfterPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte("# approved body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runSpecEdit(t, guardMocks(""), "", "--content-file", path, "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		ProposalHash string `json:"proposalHash"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# unapproved body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "", "--content-file", path, "--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", preview.ProposalHash)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("changed file: exit = %d, want 5 (%v)", got, err)
	}
	if _, wrote := captured["UpdateSpecNode"]; wrote {
		t.Error("changed file must not be written under the earlier approval")
	}
}

// The server's NODE_WRITE_CONFLICT (someone saved between our read and our
// write): exit 5, proposal not applied, NO unguarded retry, and the proposal is
// KEPT in a file — it may exist only in an editor buffer or piped stdin.
func TestSpecEditConflictKeepsTheProposalAndDoesNotRetry(t *testing.T) {
	conflict := `{"errors":[{"message":"Node at loc \"msg:010:02\" changed since it was read — re-read and retry.","extensions":{"code":"NODE_WRITE_CONFLICT"}}]}`
	_, captured, err := runSpecEdit(t, guardMocks(conflict), "# my proposal {{name}}\n", "--content", "-")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want %d (%v)", got, exitcode.Conflict, err)
	}
	if n := len(captured["UpdateSpecNode"]); n != 1 {
		t.Errorf("exactly one guarded attempt, never an unguarded retry; got %d writes", n)
	}
	if !strings.Contains(err.Error(), "your proposed edit was not applied") ||
		!strings.Contains(err.Error(), "server may have recorded out-of-band drift") ||
		strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("conflict must distinguish the rejected proposal from possible server reconciliation: %v", err)
	}
	m := regexp.MustCompile(`saved at (\S+?\.md)`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("the refusal must say where the proposal is kept: %v", err)
	}
	defer func() { _ = os.Remove(m[1]) }()
	kept, rerr := os.ReadFile(m[1])
	if rerr != nil {
		t.Fatalf("reading the kept proposal: %v", rerr)
	}
	if !strings.Contains(string(kept), "# my proposal {{name}}") {
		t.Errorf("the kept file must hold the proposed body, placeholders intact; got:\n%s", kept)
	}
}

func TestSpecEditConflictReportsUnpreservedProposal(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	conflict := `{"errors":[{"message":"stale","extensions":{"code":"NODE_WRITE_CONFLICT"}}]}`
	_, _, err := runSpecEdit(t, guardMocks(conflict), "# proposal\n", "--content", "-")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (%v)", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "could not be saved") || strings.Contains(err.Error(), "is kept") {
		t.Errorf("a failed spill must report that no saved copy exists, got %v", err)
	}
	if !strings.Contains(err.Error(), "your proposed edit was not applied") ||
		!strings.Contains(err.Error(), "server may have recorded out-of-band drift") ||
		strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("failed spill must distinguish the rejected proposal from possible server reconciliation: %v", err)
	}
}

// The dry run reports the revision it was computed against — in --json and in
// the text, with the flag to carry it to the approved save.
func TestSpecEditDryRunReportsItsRevision(t *testing.T) {
	out, _, err := runSpecEdit(t, guardMocks(""), "# new body\n", "--content", "-", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Revision     int    `json:"revision"`
		NodeID       string `json:"nodeId"`
		ProposalHash string `json:"proposalHash"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out)
	}
	if dto.Revision != 7 || dto.NodeID != "sp1" || !strings.HasPrefix(dto.ProposalHash, "sha256:") {
		t.Errorf("preview identity = %s at %d with %s, want sp1 at 7 with hash", dto.NodeID, dto.Revision, dto.ProposalHash)
	}
	text, _, err := runSpecEdit(t, guardMocks(""), "# new body\n", "--content", "-", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(text, "--expected-revision 7") || !strings.Contains(text, "--expected-node-id sp1") || !strings.Contains(text, "--expected-proposal-hash "+dto.ProposalHash) {
		t.Errorf("the dry run must name all values needed to save exactly this proposal:\n%s", text)
	}
	if !strings.Contains(text, "A pre-write refusal writes nothing") ||
		!strings.Contains(text, "on a server-reported write-time conflict") ||
		!strings.Contains(text, "your proposed edit is not applied but the server may record out-of-band drift") {
		t.Errorf("the later-save instruction must distinguish pre-write refusal from possible server reconciliation:\n%s", text)
	}
}

// Refusals that must happen before anything is sent or written.
func TestSpecEditGuardRefusals(t *testing.T) {
	t.Run("non-positive --expected-revision", func(t *testing.T) {
		_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# x\n", "--content", "-", "--expected-revision", "0", "--expected-node-id", "sp1", "--expected-proposal-hash", "sha256:x")
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d", got, exitcode.Usage)
		}
		if len(captured) != 0 {
			t.Errorf("refused before any request, got %v", captured)
		}
	})
	t.Run("incomplete preview identity", func(t *testing.T) {
		for _, flags := range [][]string{{"--expected-revision", "7"}, {"--expected-node-id", "sp1"}, {"--expected-proposal-hash", "sha256:x"}, {"--expected-revision", "7", "--expected-node-id", "sp1"}} {
			_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "# x\n", append([]string{"--content", "-"}, flags...)...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("flags %v: exit = %d, want 2", flags, got)
			}
			if len(captured) != 0 {
				t.Errorf("flags %v: refused before any request, got %v", flags, captured)
			}
		}
	})
	t.Run("a server without revisions", func(t *testing.T) {
		for _, response := range []string{
			`{"errors":[{"message":"Cannot query field \"revision\" on type \"Node\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
			`{"errors":[{"message":"Unknown field \"revision\" on type \"Node\"."}]}`,
			`{"errors":[{"message":"revision is absent","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		} {
			m := guardMocks(guardWriteOK)
			m["GetSpecNodeForEdit"] = []string{response}
			_, captured, err := runSpecEdit(t, m, "# x\n", "--content", "-")
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("response %s: exit = %d, want %d (%v)", response, got, exitcode.Usage, err)
			}
			if err == nil || !strings.Contains(err.Error(), "predates node revisions") {
				t.Errorf("the refusal must name the missing capability, got %v", err)
			}
			if _, wrote := captured["UpdateSpecNode"]; wrote {
				t.Error("no unguarded write on a server that cannot guard")
			}
		}
	})
	t.Run("a server without guarded writes", func(t *testing.T) {
		m := guardMocks(`{"errors":[{"message":"Field \"expectedRevision\" is not defined by type \"UpdateNodeInput\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`)
		_, captured, err := runSpecEdit(t, m, "# x\n", "--content", "-")
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("exit = %d, want %d (%v)", got, exitcode.Usage, err)
		}
		if n := len(captured["UpdateSpecNode"]); n != 1 {
			t.Errorf("one guarded attempt and NO unguarded fallback, got %d writes", n)
		}
		if err == nil || !strings.Contains(err.Error(), "does not support guarded saves") {
			t.Errorf("the refusal must name the missing capability, got %v", err)
		}
	})
}
