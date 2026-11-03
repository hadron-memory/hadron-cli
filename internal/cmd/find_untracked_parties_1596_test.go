package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1596 (hadron-server#3132 S4, cor:agt:120:03): --assignee/--owner on
// the find-untracked claim. The claim stays selected only by --assign-to-me;
// the party flags ride a SEPARATE document so the no-flag claim is the old
// document byte for byte (an older server still validates it).

const claimPartiesOp1596 = "ClaimUntrackedTaskItemsForParties"

func claimOK1596() map[string]string {
	ok := claimResult1524(claimedTaskJSON1524, true, "", "")
	return map[string]string{
		"ClaimUntrackedTaskItems": ok,
		claimPartiesOp1596:        ok,
		"UntrackedTaskItems":      untrackedPage1524("", 0, true, ""),
	}
}

func claimWrites1596(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "ClaimUntrackedTaskItems" || c.Op == claimPartiesOp1596 || c.Op == "UntrackedTaskItems" {
			got = append(got, c)
		}
	}
	return got
}

// Row 2 + malformed refs: refused locally, exit 2, ZERO requests of any kind
// (not even the binding's App read), never a list turned into a write.
func TestFindUntrackedPartyFlagsRefusedLocally1596(t *testing.T) {
	for name, argv := range map[string][]string{
		"--assignee without --assign-to-me":    {"--assignee", "worker:Iris"},
		"--owner without --assign-to-me":       {"--owner", "user:holger"},
		"both without --assign-to-me":          {"--assignee", "worker:Iris", "--owner", "user:holger"},
		"--assignee empty":                     {"--assign-to-me", "--assignee", ""},
		"--owner empty":                        {"--assign-to-me", "--owner", ""},
		"--assignee without a kind":            {"--assign-to-me", "--assignee", "Iris"},
		"--owner with an unknown kind":         {"--assign-to-me", "--owner", "team:Marco"},
		"--assignee with an empty ref":         {"--assign-to-me", "--assignee", "worker:"},
		"valid --assignee, malformed --owner":  {"--assign-to-me", "--assignee", "worker:Iris", "--owner", "holger"},
		"empty --assignee without the switch":  {"--assignee", ""},
		"--owner=user:x with --skip, no claim": {"--owner", "user:x", "--skip", "2"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, _, err := runFind1524(t, claimOK1596(), append([]string{"--integration", "github"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("the flags must exist: %v", err)
			}
			if len(*calls) != 0 {
				t.Errorf("zero requests expected, got %v", *calls)
			}
			if out != "" {
				t.Errorf("nothing on stdout: %q", out)
			}
		})
	}
}

// Row 1: without the switch or the party flags the list is unchanged.
func TestFindUntrackedListUnchanged1596(t *testing.T) {
	calls, _, _, err := runFind1524(t, claimOK1596(), "--integration", "github")
	if err != nil {
		t.Fatal(err)
	}
	got := claimWrites1596(calls)
	if len(got) != 1 || got[0].Op != "UntrackedTaskItems" {
		t.Fatalf("want one UntrackedTaskItems, got %v", *calls)
	}
}

// Row 3: the claim without party flags is the OLD document: no assignee or
// owner in the query text or the variables (not null, not a default, no self
// inference), and the session attribution of the bound worktree as before.
func TestFindUntrackedClaimWithoutPartiesIsTheOldDocument1596(t *testing.T) {
	calls, _, _, err := runFind1524(t, claimOK1596(), "--integration", "github", "--assign-to-me", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	got := claimWrites1596(calls)
	if len(got) != 1 || got[0].Op != "ClaimUntrackedTaskItems" {
		t.Fatalf("want one ClaimUntrackedTaskItems, got %v", *calls)
	}
	c := got[0]
	for _, k := range []string{"assignee", "owner"} {
		if _, present := c.Vars[k]; present {
			t.Errorf("%s must not be sent: %v", k, c.Vars)
		}
		if strings.Contains(c.Query, "$"+k) {
			t.Errorf("the no-flag document must not declare $%s:\n%s", k, c.Query)
		}
	}
	for k := range c.Vars {
		switch k {
		case "appRef", "integration", "team", "repository", "ready", "limit":
		default:
			t.Errorf("unexpected variable %q: %v", k, c.Vars)
		}
	}
	if c.Session == "" {
		t.Errorf("a bound worktree's claim carries its session as before")
	}
}

// Row 4: with the switch, each CHANGED party flag is sent as given (raw
// worker:/user: refs, colons after the first kept, nothing resolved); an
// unset one is omitted. One request, through the bound session as before.
func TestFindUntrackedClaimSendsGivenParties1596(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argv     []string
		assignee map[string]any
		owner    map[string]any
	}{
		{"both", []string{"--assignee", "worker:Iris", "--owner", "user:holger"},
			map[string]any{"worker": "Iris"}, map[string]any{"user": "holger"}},
		{"assignee only", []string{"--assignee", "worker:hrn:worker:acme.com:eng-team:iris"},
			map[string]any{"worker": "hrn:worker:acme.com:eng-team:iris"}, nil},
		{"owner only", []string{"--owner", "worker:Marco"}, nil, map[string]any{"worker": "Marco"}},
		{"user assignee", []string{"--assignee", "user:u-123", "--owner", "user:u-456"},
			map[string]any{"user": "u-123"}, map[string]any{"user": "u-456"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{"--integration", "github", "--assign-to-me", "--team", "Marco"}, tc.argv...)
			calls, _, _, err := runFind1524(t, claimOK1596(), argv...)
			if err != nil {
				t.Fatal(err)
			}
			got := claimWrites1596(calls)
			if len(got) != 1 || got[0].Op != claimPartiesOp1596 {
				t.Fatalf("want one %s, got %v", claimPartiesOp1596, *calls)
			}
			v := got[0].Vars
			for key, want := range map[string]map[string]any{"assignee": tc.assignee, "owner": tc.owner} {
				raw, present := v[key]
				if want == nil {
					if present {
						t.Errorf("%s unset must be omitted, got %v", key, raw)
					}
					continue
				}
				m, _ := raw.(map[string]any)
				if len(m) != len(want) {
					t.Errorf("%s = %v, want %v", key, raw, want)
				}
				for k, w := range want {
					if m[k] != w {
						t.Errorf("%s.%s = %v, want %v", key, k, m[k], w)
					}
				}
			}
			if v["integration"] != "github" || v["team"] != "Marco" {
				t.Errorf("other variables lost: %v", v)
			}
			if got[0].Session == "" {
				t.Errorf("attribution unchanged: the bound session rides the claim")
			}
		})
	}
}

// Acceptance 2: the server's refusals pass through with their code (no local
// authority guess): a person/worker naming parties gets BAD_USER_INPUT; a key
// gets the REQUIRED/INVALID codes; a key with a session gets
// APP_KEY_SESSION_BOUND. One request, never retried or downgraded.
func TestFindUntrackedClaimPartyRefusals1596(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"BAD_USER_INPUT", exitcode.Usage},
		{"TASK_ASSIGNEE_REQUIRED", exitcode.Usage},
		{"TASK_OWNER_REQUIRED", exitcode.Usage},
		{"TASK_ASSIGNEE_INVALID", exitcode.Usage},
		{"TASK_OWNER_INVALID", exitcode.Usage},
		{"APP_KEY_SESSION_BOUND", exitcode.Usage},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := claimOK1596()
			r[claimPartiesOp1596] = `{"errors":[{"message":"refused by the server","extensions":{"code":"` + tc.code + `"}}]}`
			calls, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--assignee", "worker:Iris", "--owner", "user:holger", "--json")
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), tc.exit, err)
			}
			if out != "" {
				t.Errorf("a thrown refusal prints nothing on stdout: %q", out)
			}
			if env := envelope1524(t, err); env["serverCode"] != tc.code {
				t.Errorf("serverCode = %v, want %s", env["serverCode"], tc.code)
			}
			if !strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), "refused by the server") {
				t.Errorf("message keeps the code and the server's sentence: %v", err)
			}
			got := claimWrites1596(calls)
			if len(got) != 1 || got[0].Op != claimPartiesOp1596 {
				t.Errorf("exactly one party claim, never retried or downgraded: %v", *calls)
			}
		})
	}
}

// A stopped walk on the party document prints the claims made, then fails
// with the refusal's code, exactly as the no-flag claim does.
func TestFindUntrackedPartyClaimPartialResult1596(t *testing.T) {
	r := claimOK1596()
	r[claimPartiesOp1596] = claimResult1524(claimedTaskJSON1524, false, "claim_refused", "TASK_ASSIGNEE_INVALID")
	_, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--assignee", "worker:Iris", "--owner", "user:holger", "--json")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
	}
	if !strings.Contains(out, `"refusalCode":"TASK_ASSIGNEE_INVALID"`) || !strings.Contains(out, "issues/42") {
		t.Errorf("the claimed tasks print first:\n%s", out)
	}
}

// The help no longer says an App key is refused, and names the server as the
// judge of who may name parties.
func TestFindUntrackedPartyHelp1596(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	if strings.Contains(help, "TASK_CLAIM_PRINCIPAL_UNSUPPORTED") || strings.Contains(help, "an App key is refused") {
		t.Errorf("stale App-key refusal still in help:\n%s", help)
	}
	for _, want := range []string{"--assignee", "--owner", "only with --assign-to-me", "BAD_USER_INPUT", "TASK_ASSIGNEE_REQUIRED", "APP_KEY_SESSION_BOUND"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
