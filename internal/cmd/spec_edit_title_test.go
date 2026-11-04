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

// cli#810 — `spec edit --title` rewrites Node.name as "<citation> — <title>"
// through the SAME guarded write as body/abstract/description.

const (
	titleStatusSuperseded = `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"sp1","revision":8,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"SUPERSEDED","approval":{"revision":7,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"app:abc","hash":"aaaa"}},"mintStatus":{"minted":false,"mintedAt":null,"mintedBy":null,"mintedByInfo":null,"revision":null,"hash":null}}]}}}`
	titleWriteOK          = `{"data":{"updateSpecNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — New title","nodeType":"info","tags":["spec"],"updatedAt":"2026-09-26T00:00:00Z"}}}`
)

func titleMocks(write string) map[string][]string {
	m := guardMocks(write)
	m["NodeLiveRevisionsMinted"] = []string{titleStatusSuperseded}
	return m
}

type sentTitleEditEnvelope struct {
	Input map[string]any `json:"input"`
}

func sentTitleEditInput(t *testing.T, vars json.RawMessage) map[string]any {
	t.Helper()
	var v sentTitleEditEnvelope
	if err := json.Unmarshal(vars, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input
}

func TestSpecEditTitleOnlyIsGuardedAndReportsApproval(t *testing.T) {
	out, captured, err := runSpecEdit(t, titleMocks(titleWriteOK), "", "--title", "New title", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(captured["UpdateSpecNode"]); n != 1 {
		t.Fatalf("UpdateSpecNode calls = %d, want 1", n)
	}
	in := sentTitleEditInput(t, captured["UpdateSpecNode"][0])
	if in["name"] != "msg:010:02 — New title" || in["id"] != "sp1" || in["expectedRevision"] != float64(7) {
		t.Fatalf("payload %v", in)
	}
	for _, k := range []string{"content", "abstract", "description", "loc", "role", "tags"} {
		if _, ok := in[k]; ok {
			t.Fatalf("title-only edit must not send %s: %v", k, in)
		}
	}
	var res struct {
		Name           string `json:"name"`
		Changed        bool   `json:"changed"`
		TitleChanged   bool   `json:"titleChanged"`
		BodyChanged    bool   `json:"bodyChanged"`
		ApprovalStatus *struct {
			State string `json:"state"`
		} `json:"approvalStatus"`
		Changes []struct {
			Field, Before, After string
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Name != "msg:010:02 — New title" || !res.Changed || !res.TitleChanged || res.BodyChanged {
		t.Fatalf("result %+v", res)
	}
	if res.ApprovalStatus == nil || res.ApprovalStatus.State != "SUPERSEDED" {
		t.Fatalf("approval must come from the provider read: %+v", res.ApprovalStatus)
	}
	if len(res.Changes) != 1 || res.Changes[0].Field != "name" || res.Changes[0].Before != "msg:010:02 — W2" || res.Changes[0].After != "msg:010:02 — New title" {
		t.Fatalf("changes %+v", res.Changes)
	}
}

func TestSpecEditTitleTextReceipt(t *testing.T) {
	out, _, err := runSpecEdit(t, titleMocks(titleWriteOK), "", "--title", "New title")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"✓ updated msg:010:02 — New title", "title: updated", "Approval: SUPERSEDED"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSpecEditTitleWithBodyIsOneWrite(t *testing.T) {
	_, captured, err := runSpecEdit(t, titleMocks(titleWriteOK), "new body\n", "--title", "New title", "--content", "-")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(captured["UpdateSpecNode"]); n != 1 {
		t.Fatalf("combined edit must be one write, got %d", n)
	}
	in := sentTitleEditInput(t, captured["UpdateSpecNode"][0])
	if in["name"] != "msg:010:02 — New title" || in["content"] != "new body\n" || in["expectedRevision"] != float64(7) {
		t.Fatalf("payload %v", in)
	}
}

func TestSpecEditOmittedTitlePreservesName(t *testing.T) {
	_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "new body\n", "--content", "-")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sentTitleEditInput(t, captured["UpdateSpecNode"][0])["name"]; ok {
		t.Fatal("name must be omitted (preserve) when --title is absent")
	}
	if len(captured["NodeLiveRevisionsMinted"]) != 0 {
		t.Fatal("no approval read for a non-title edit")
	}
}

func TestSpecEditTitleNoOpWritesNothing(t *testing.T) {
	out, captured, err := runSpecEdit(t, guardMocks(""), "", "--title", "W2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(captured["UpdateSpecNode"]) != 0 {
		t.Fatal("identical title must not write")
	}
	if !strings.Contains(out, `"changed": false`) || !strings.Contains(out, `"changes": []`) {
		t.Fatalf("no-op result: %s", out)
	}
}

func TestSpecEditTitleRefusals(t *testing.T) {
	for _, title := range []string{"", "   ", "two\nlines", "msg:010:02 — Already prefixed"} {
		_, captured, err := runSpecEdit(t, map[string][]string{}, "", "--title", title)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("title %q: exit = %d, want %d (%v)", title, got, exitcode.Usage, err)
		}
		if len(captured) != 0 {
			t.Errorf("title %q: a refusal must precede every request, got %v", title, captured)
		}
	}
}

func TestSpecEditTitleDryRunWritesNothing(t *testing.T) {
	out, captured, err := runSpecEdit(t, guardMocks(""), "", "--title", "New title", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(captured["UpdateSpecNode"]) != 0 || len(captured["NodeLiveRevisionsMinted"]) != 0 {
		t.Fatalf("dry run must make no write and no status read: %v", captured)
	}
	var res struct {
		DryRun         bool            `json:"dryRun"`
		TitleChanged   bool            `json:"titleChanged"`
		Name           string          `json:"name"`
		ApprovalStatus json.RawMessage `json:"approvalStatus"`
		Changes        []struct {
			Field, Diff string
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || !res.TitleChanged || res.ApprovalStatus != nil || res.Name != "msg:010:02 — W2" {
		t.Fatalf("dry run %+v", res)
	}
	if len(res.Changes) != 1 || res.Changes[0].Field != "name" || !strings.Contains(res.Changes[0].Diff, "+msg:010:02 — New title") {
		t.Fatalf("changes %+v", res.Changes)
	}
}

func TestSpecEditTitleGuardsRefuseBeforeWrite(t *testing.T) {
	hash := func() string {
		out, _, err := runSpecEdit(t, guardMocks(""), "", "--title", "New title", "--dry-run", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			ProposalHash string `json:"proposalHash"`
		}
		_ = json.Unmarshal([]byte(out), &p)
		return p.ProposalHash
	}()
	for name, args := range map[string][]string{
		"stale revision": {"--expected-revision", "6", "--expected-node-id", "sp1", "--expected-proposal-hash", hash},
		"other node":     {"--expected-revision", "7", "--expected-node-id", "other", "--expected-proposal-hash", hash},
		"changed title":  {"--title", "A different title", "--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash},
	} {
		args := args
		if name != "changed title" {
			args = append([]string{"--title", "New title"}, args...)
		}
		_, captured, err := runSpecEdit(t, guardMocks(""), "", args...)
		if got := exitCodeFor(err); got != exitcode.Conflict {
			t.Errorf("%s: exit = %d, want %d (%v)", name, got, exitcode.Conflict, err)
		}
		if len(captured["UpdateSpecNode"]) != 0 {
			t.Errorf("%s: nothing may be written", name)
		}
	}
	// The same proposal approved earlier saves.
	_, captured, err := runSpecEdit(t, titleMocks(titleWriteOK), "", "--title", "New title",
		"--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash)
	if err != nil || len(captured["UpdateSpecNode"]) != 1 {
		t.Fatalf("approved proposal: err=%v writes=%d", err, len(captured["UpdateSpecNode"]))
	}
}

func TestSpecEditTitleServerConflictKeepsProposal(t *testing.T) {
	for _, tc := range []struct {
		name               string
		title, description bool
	}{
		{"title only", true, false}, {"description only", false, true}, {"title and description", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			conflict := `{"errors":[{"message":"changed","extensions":{"code":"NODE_WRITE_CONFLICT"}}]}`
			args := []string{}
			if tc.title {
				args = append(args, "--title", "New title")
			}
			if tc.description {
				path := filepath.Join(t.TempDir(), "description.txt")
				if err := os.WriteFile(path, []byte("My proposed description"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--description-file", path)
			}
			_, captured, err := runSpecEdit(t, guardMocks(conflict), "", args...)
			if got := exitCodeFor(err); got != exitcode.Conflict {
				t.Fatalf("exit = %d, want 5 (%v)", got, err)
			}
			if len(captured["UpdateSpecNode"]) != 1 {
				t.Fatal("exactly one write attempt")
			}
			match := regexp.MustCompile(`saved at (\S+?\.md)`).FindStringSubmatch(err.Error())
			if match == nil {
				t.Fatalf("no saved proposal: %v", err)
			}
			kept, readErr := os.ReadFile(match[1])
			if readErr != nil {
				t.Fatal(readErr)
			}
			titleHeader := "Proposed title: New title"
			if strings.Contains(string(kept), titleHeader) != tc.title {
				t.Fatalf("title preservation: %s", kept)
			}
			descriptionHeader := "<!-- Proposed description (--description-file): -->\nMy proposed description"
			if strings.Contains(string(kept), descriptionHeader) != tc.description {
				t.Fatalf("description preservation: %s", kept)
			}
			info, statErr := os.Stat(match[1])
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("spill mode: %v", info.Mode())
			}
		})
	}
}

func TestSpecEditTitleLostWriteIsNotSuccess(t *testing.T) {
	// The server answers with the OLD name: the retitle did not land.
	_, _, err := runSpecEdit(t, titleMocks(guardWriteOK), "", "--title", "New title")
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Fatalf("exit = %d, want %d (%v)", got, exitcode.Error, err)
	}
	if err == nil || !strings.Contains(err.Error(), "can't be confirmed") {
		t.Fatalf("error %v", err)
	}
}

func TestSpecEditTitleStatusReadFailureDoesNotFailSavedWrite(t *testing.T) {
	m := guardMocks(titleWriteOK)
	m["NodeLiveRevisionsMinted"] = []string{`{"errors":[{"message":"boom"}]}`}
	out, _, err := runSpecEdit(t, m, "", "--title", "New title")
	if err != nil {
		t.Fatalf("a saved retitle must not fail on the status read: %v", err)
	}
	if !strings.Contains(out, "approval status could not be read back") {
		t.Fatalf("output %s", out)
	}
}
