package cmd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1531 fix head: Atilla's LOCAL P2s and Diego's BLIND P3.

// P2-1: the generated setAppProperties result is a nullable pointer. A null or
// missing result is a controlled exit 1 with nothing on stdout, never a panic:
// the write may have landed, so the message says to read before writing again.
func TestAppPropertiesWriteMissingResult1524(t *testing.T) {
	for _, verb := range [][]string{
		{"app", "properties", "set", appRef1524, `{"a":1}`},
		{"app", "properties", "clear", appRef1524, "--yes"},
	} {
		for name, data := range map[string]string{
			"null result":    `{"data":{"setAppProperties":null}}`,
			"missing result": `{"data":{}}`,
		} {
			t.Run(verb[2]+" "+name, func(t *testing.T) {
				calls, out, err := runApp1524(t, map[string]string{"SetAppProperties": data}, "", verb...)
				if exitCodeFor(err) != exitcode.Error || out != "" {
					t.Fatalf("exit %d, out %q: %v", exitCodeFor(err), out, err)
				}
				if !strings.Contains(err.Error(), "app properties get") {
					t.Errorf("no read-back remedy: %v", err)
				}
				if n := len(*calls); n != 1 {
					t.Errorf("the write must be sent once, got %d calls", n)
				}
			})
		}
	}
}

// P2-2: the claim is the bound worker's write, so it carries the worktree
// session (the worker claims, not the person behind the token).
func TestFindUntrackedClaimCarriesWorkerSession1524(t *testing.T) {
	r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, true, "", "")}
	calls, _, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		switch c.Op {
		case "ClaimUntrackedTaskItems":
			if c.Session != "s-new" {
				t.Fatalf("claim carried session %q, want the bound worker's", c.Session)
			}
			return
		}
	}
	t.Fatal("no ClaimUntrackedTaskItems call")
}

// Control: the list is a read and stays session-free.
func TestFindUntrackedListCarriesNoSession1524(t *testing.T) {
	r := map[string]string{"UntrackedTaskItems": untrackedPage1524("", 0, true, "")}
	calls, _, _, err := runFind1524(t, r, "--integration", "github", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range findCalls1524(calls) {
		if c.Op == "UntrackedTaskItems" && c.Session != "" {
			t.Fatalf("list carried session %q", c.Session)
		}
	}
}

// Unbound: a person claims as themself, with no session.
func TestFindUntrackedClaimUnboundIsPerson1524(t *testing.T) {
	r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, true, "", "")}
	calls, _, err := runApp1524(t, r, "", "team", "task", "find-untracked", "--app", appRef1524, "--integration", "github", "--assign-to-me", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "ClaimUntrackedTaskItems" || (*calls)[0].Session != "" {
		t.Fatalf("unbound claim: %+v", *calls)
	}
}

// Control: app properties writes are the App manager's and stay
// session-free even in a bound worktree.
func TestAppPropertiesWriteNotAttributedToWorker1524(t *testing.T) {
	r := setResponses1524(`{"a":1}`)
	calls, _, _, err := runTeamTaskAll1524(t, r, "app", "properties", "set", appRef1524, `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "SetAppProperties" {
			if c.Session != "" {
				t.Fatalf("app properties set carried worker session %q", c.Session)
			}
			return
		}
	}
	t.Fatal("no SetAppProperties call")
}

// Diego's P3: through the production failure path, a partial claim leaves
// exactly one JSON document (the claimed tasks) on stdout and the error
// envelope, with serverCode, on stderr.
func TestFindUntrackedPartialClaimStreams1524(t *testing.T) {
	r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, false, "claim_refused", "GITHUB_PERMISSION_MISSING")}
	writeTeamBinding(t)
	srv, _ := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	args := []string{"--server", srv.URL, "team", "task", "find-untracked", "--integration", "github", "--assign-to-me", "--limit", "3", "--json"}
	root := NewRootCmd(f)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatal("a partial claim must fail")
	}
	if code := renderFailure(f, args, err); code != exitcode.Forbidden {
		t.Fatalf("exit %d, want %d", code, exitcode.Forbidden)
	}

	dec := json.NewDecoder(strings.NewReader(out.String()))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, out.String())
	}
	if tasks, _ := doc["tasks"].([]any); len(tasks) != 1 {
		t.Errorf("stdout must carry the claimed task: %s", out.String())
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("stdout must hold exactly one JSON document: %q", out.String())
	}

	stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
	start := strings.Index(stderr, "{")
	if start < 0 {
		t.Fatalf("no error envelope on stderr: %q", stderr)
	}
	var env struct {
		Error struct {
			ServerCode string `json:"serverCode"`
			Message    string `json:"message"`
		} `json:"error"`
		ServerCode string `json:"serverCode"`
	}
	if err := json.NewDecoder(strings.NewReader(stderr[start:])).Decode(&env); err != nil {
		t.Fatalf("stderr envelope is not JSON: %v\n%s", err, stderr)
	}
	if env.Error.ServerCode != "GITHUB_PERMISSION_MISSING" && env.ServerCode != "GITHUB_PERMISSION_MISSING" {
		t.Errorf("envelope lost serverCode: %s", stderr)
	}
	if strings.Contains(out.String(), "GITHUB_PERMISSION_MISSING") && strings.Contains(out.String(), `"error"`) {
		t.Errorf("the error envelope leaked onto stdout: %s", out.String())
	}
}
