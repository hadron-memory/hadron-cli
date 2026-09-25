package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #715, @codex on #724: a file carrying rev= can meet a server that predates
// the field. That server refuses the whole plan (measured on production
// 0.19.0), so the command asks once more without revisions. Only on that
// exact refusal: any other error is still the run's.

// revisionRefusal is production 0.19.0's answer, verbatim.
const revisionRefusal = `{"errors":[{"message":"Variable \"$input\" got invalid value { dirName: \"x\", revision: 7 } at \"input.files[0]\"; Field \"revision\" is not defined by type \"SkillFileFactsInput\".","extensions":{"code":"BAD_USER_INPUT"}}]}`

// olderServer answers op like a server without revisions: a request whose
// files carry `revision` is refused, one without succeeds with ok. It records
// whether each call carried a revision.
func olderServer(t *testing.T, op, ok, refusal string) (*httptest.Server, func() []bool) {
	t.Helper()
	var mu sync.Mutex
	var calls []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "GetMemory":
			_, _ = w.Write([]byte(skillMemOrg))
		case op:
			carries := strings.Contains(string(raw), `"revision"`)
			mu.Lock()
			calls = append(calls, carries)
			mu.Unlock()
			if carries {
				_, _ = w.Write([]byte(refusal))
				return
			}
			_, _ = w.Write([]byte(ok))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), calls...)
	}
}

func skillFileWithRevision(t *testing.T, root string) {
	t.Helper()
	path := writeSkillFile(t, root, "with-rev")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), "<!-- hadron-skill id="+statusNodeID+" ", "<!-- hadron-skill id="+statusNodeID+" rev=7 ", 1)
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSkillStatusRetriesWithoutRevisionsOnAnOlderServer(t *testing.T) {
	root := t.TempDir()
	skillFileWithRevision(t, root)
	srv, calls := olderServer(t, "SkillPlan", skillPlanResp(`"current"`, "false", ""), revisionRefusal)
	f, out := testFactory(t)
	cmd := NewRootCmd(f)
	cmd.SetArgs([]string{"skill", "status", "--server", srv.URL, "-m", "hrn:mem:hadronmemory.com:core", "--to", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("an older server must not fail the run: %v\n%s", err, out.String())
	}
	if got := calls(); len(got) != 2 || !got[0] || got[1] {
		t.Errorf("calls carrying a revision = %v, want [true false]: one retry, without it", got)
	}
}

// Any other refusal is the run's error, with no retry.
func TestSkillStatusDoesNotRetryOtherInputErrors(t *testing.T) {
	root := t.TempDir()
	skillFileWithRevision(t, root)
	other := `{"errors":[{"message":"Variable \"$input\" got invalid value; Field \"host\" is invalid.","extensions":{"code":"BAD_USER_INPUT"}}]}`
	srv, calls := olderServer(t, "SkillPlan", skillPlanResp(`"current"`, "false", ""), other)
	f, _ := testFactory(t)
	cmd := NewRootCmd(f)
	cmd.SetArgs([]string{"skill", "status", "--server", srv.URL, "-m", "hrn:mem:hadronmemory.com:core", "--to", root, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Error("a different input error must fail the run")
	}
	if got := calls(); len(got) != 1 {
		t.Errorf("calls = %v, want exactly one: no retry for another error", got)
	}
}

func TestSkillExportRetriesWithoutRevisionsOnAnOlderServer(t *testing.T) {
	h := accHome(t)
	skillFileWithRevision(t, filepath.Join(h, ".claude", "skills"))
	emptyPlan := `{"data":{"skillPlan":{"scanned":0,"judged":0,"entries":[],"orphans":[],"unrecognized":[]}}}`
	srv, calls := olderServer(t, "SkillExportPlan", emptyPlan, revisionRefusal)
	_, raw, err := runExportRaw(t, srv.URL)
	if code := exitCodeFor(err); code != 0 {
		t.Fatalf("exit %d: an older server must not fail the Claude host: %v\n%s", code, err, raw)
	}
	// Claude: refused with the revision, then asked again without it. Codex:
	// its root holds no files, so its one call carries none.
	if got := calls(); len(got) != 3 || !got[0] || got[1] || got[2] {
		t.Errorf("calls carrying a revision = %v, want [true false false]", got)
	}
}
