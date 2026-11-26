package cmd

import (
	"strings"
	"testing"
)

// cli#3210: one masker on every surface that prints the server's sync text, in
// text and --json: pull (stored syncError on an OK and a non-OK answer, and a
// refusal), memory get, and push. Each surface gets a URL-credential sentinel
// and a bare-token sentinel for every well-known shape; none may reach stdout,
// stderr or the error text. A token of an unknown shape cannot be recognised,
// so the shapes tested are the documented floor.

const (
	sentURL   = "https://user:URLSENTINEL@host/repo.git"
	sentToken = "ghp_BARESENTINEL1234"
)

var sentinelText = "git failed for " + sentURL + " with " + sentToken + ", also github_pat_PATSENTINEL_12345 and glpat-LABSENTINEL_1234 and gho_OAUTHSENTINEL1234"

var sentinelLeaks = []string{"URLSENTINEL", "BARESENTINEL", "PATSENTINEL", "LABSENTINEL", "OAUTHSENTINEL"}

func assertNoSentinel(t *testing.T, surface string, got ...string) {
	t.Helper()
	for _, g := range got {
		for _, s := range sentinelLeaks {
			if strings.Contains(g, s) {
				t.Errorf("%s leaked %s: %q", surface, s, g)
			}
		}
	}
}

func TestSyncMaskerOnPullStoredError3210(t *testing.T) {
	for _, tc := range []struct{ name, status string }{{"non-OK answer", "ERROR"}, {"OK answer with a stored error", "OK"}} {
		for _, asJSON := range []bool{false, true} {
			gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, nil, sp(syncedAt), sp(sentinelText), nil, tc.status) + `}}`})
			f, out := testFactory(t)
			errOut := &strings.Builder{}
			f.IOStreams.ErrOut = errOut
			root := NewRootCmd(f)
			args := []string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			_ = root.Execute()
			assertNoSentinel(t, "pull "+tc.name, out.String(), errOut.String())
			if !strings.Contains(out.String(), "[redacted]") {
				t.Errorf("pull %s json=%v: nothing was masked: %s", tc.name, asJSON, out.String())
			}
		}
	}
}

func TestSyncMaskerOnPullRefusal3210(t *testing.T) {
	gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"errors":[{"message":"` + sentinelText + `","extensions":{"code":"SYNC_FAILED"}}]}`})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want error")
	}
	assertNoSentinel(t, "pull refusal", out.String(), errOut.String(), err.Error())
}

func TestSyncMaskerOnMemoryGet3210(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		memory := syncMemoryJSON(t, sp("https://github.com/acme/kb"), sp(syncedAt), sp(sentinelText), nil, "ERROR")
		out, err := runMemoryGetSync(t, memory, asJSON)
		if err != nil {
			t.Fatal(err)
		}
		assertNoSentinel(t, "memory get", out)
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("memory get json=%v: nothing was masked: %s", asJSON, out)
		}
	}
}

func TestSyncMaskerOnPush3210(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		s := newPushStub(t, 200, `{"errors":[{"message":"`+sentinelText+`","extensions":{"code":"FORBIDDEN"}}]}`)
		args := []string{"--yes"}
		if asJSON {
			args = append(args, "--json")
		}
		out, errOut, err := runPush(t, s, args...)
		if err == nil {
			t.Fatal("want error")
		}
		assertNoSentinel(t, "push", out, errOut, err.Error())
	}
}
