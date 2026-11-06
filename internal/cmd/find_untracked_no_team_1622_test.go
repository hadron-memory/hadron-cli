package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1622 (hadron-server#3422, cor:agt:120:03): `find-untracked --no-team`.
// The flag rides three SEPARATE request documents (the cli#1596 pattern), so
// the three existing documents stay what they were and a server without the
// argument keeps validating a call that does not use it.

const (
	listOpNT   = "UntrackedTaskItemsNoTeam"
	claimOpNT  = "ClaimUntrackedTaskItemsNoTeam"
	partyOpNT  = "ClaimUntrackedTaskItemsForPartiesNoTeam"
	listOpOld  = "UntrackedTaskItems"
	claimOpOld = "ClaimUntrackedTaskItems"
	partyOpOld = "ClaimUntrackedTaskItemsForParties"
)

func noTeamOK1622() map[string]string {
	ok := claimResult1524(claimedTaskJSON1524, true, "", "")
	page := untrackedPage1524("", 0, true, "")
	return map[string]string{
		listOpNT: page, claimOpNT: ok, partyOpNT: ok,
		listOpOld: page, claimOpOld: ok, partyOpOld: ok,
	}
}

func findOps1622(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		switch c.Op {
		case listOpNT, claimOpNT, partyOpNT, listOpOld, claimOpOld, partyOpOld:
			got = append(got, c)
		}
	}
	return got
}

func sha1622(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Item 1: each of the three paths sends noTeam true and no team.
func TestFindUntrackedNoTeamFilterOnAllThreePaths1622(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		op   string
	}{
		{"list", nil, listOpNT},
		{"claim", []string{"--assign-to-me", "--limit", "2"}, claimOpNT},
		{"claim naming parties", []string{"--assign-to-me", "--assignee", "worker:X", "--owner", "user:Y"}, partyOpNT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{"--integration", "github", "--no-team", "--repo", "acme/app", "--ready", "only"}, tc.argv...)
			calls, _, _, err := runFind1524(t, noTeamOK1622(), argv...)
			if err != nil {
				t.Fatal(err)
			}
			got := findOps1622(calls)
			if len(got) != 1 || got[0].Op != tc.op {
				t.Fatalf("want exactly one %s, got %v", tc.op, *calls)
			}
			v := got[0].Vars
			if v["noTeam"] != true {
				t.Errorf("noTeam must be sent true: %v", v)
			}
			if _, present := v["team"]; present {
				t.Errorf("team must not be sent beside noTeam: %v", v)
			}
			if v["integration"] != "github" || v["repository"] != "acme/app" || v["ready"] != "ONLY" {
				t.Errorf("the other filters pass through: %v", v)
			}
			if !strings.Contains(got[0].Query, "noTeam: $noTeam") || strings.Contains(got[0].Query, "$team") {
				t.Errorf("the document must pass noTeam and declare no $team:\n%s", got[0].Query)
			}
		})
	}
}

// Item 2: without --no-team (omitted, or =false) every path is the old
// document, byte for byte, with no noTeam variable.
func TestFindUntrackedWithoutNoTeamIsTheOldDocument1622(t *testing.T) {
	// sha256 of the three documents as they were on main before this change.
	want := map[string]string{
		listOpOld:  "8418811dd3df5a1c40c391044e353cf778d043f1e23f992f4f2f1a237ab2a5a5",
		claimOpOld: "c6ed7bd643644dc609a2e765388b1e9442b7abe54eceaf77e9ec230572569180",
		partyOpOld: "12335850e5570e9d56067b22bca1f49cabb5208e71dd74a6c8d32cc70a2f382d",
	}
	for op, docs := range map[string]string{
		listOpOld: gen.UntrackedTaskItems_Operation, claimOpOld: gen.ClaimUntrackedTaskItems_Operation,
		partyOpOld: gen.ClaimUntrackedTaskItemsForParties_Operation,
	} {
		if sha1622(docs) != want[op] {
			t.Errorf("the %s document changed; it must stay byte for byte (cli#1622 item 2)", op)
		}
	}
	for _, flag := range [][]string{nil, {"--no-team=false"}} {
		for _, tc := range []struct {
			name string
			argv []string
			op   string
		}{
			{"list", nil, listOpOld},
			{"claim", []string{"--assign-to-me"}, claimOpOld},
			{"claim naming parties", []string{"--assign-to-me", "--assignee", "worker:X", "--owner", "user:Y"}, partyOpOld},
		} {
			t.Run(strings.Join(flag, "")+"/"+tc.name, func(t *testing.T) {
				argv := append(append([]string{"--integration", "github", "--team", "Marco"}, flag...), tc.argv...)
				calls, _, _, err := runFind1524(t, noTeamOK1622(), argv...)
				if err != nil {
					t.Fatal(err)
				}
				got := findOps1622(calls)
				if len(got) != 1 || got[0].Op != tc.op {
					t.Fatalf("want exactly one %s, got %v", tc.op, *calls)
				}
				if _, present := got[0].Vars["noTeam"]; present {
					t.Errorf("noTeam must not be sent: %v", got[0].Vars)
				}
				if strings.Contains(got[0].Query, "noTeam") {
					t.Errorf("the old document must not mention noTeam:\n%s", got[0].Query)
				}
				if got[0].Vars["team"] != "Marco" {
					t.Errorf("--team alone is unchanged: %v", got[0].Vars)
				}
			})
		}
	}
}

// Item 3: the conflict is refused first, with no request, whatever the --team
// value; --no-team=false is not a conflict and keeps the old validation.
func TestFindUntrackedNoTeamTeamConflict1622(t *testing.T) {
	for name, argv := range map[string][]string{
		"team value":          {"--no-team", "--team", "Marco"},
		"empty team":          {"--no-team", "--team", ""},
		"blank team":          {"--no-team", "--team", "  "},
		"claim":               {"--no-team", "--assign-to-me", "--team", "Marco"},
		"claim with parties":  {"--no-team", "--assign-to-me", "--team", "Marco", "--assignee", "worker:X", "--owner", "user:Y"},
		"explicit true":       {"--no-team=true", "--team", "Marco"},
		"team before no-team": {"--team", "Marco", "--no-team"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, _, err := runFind1524(t, noTeamOK1622(), append([]string{"--integration", "github"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("the flag must exist: %v", err)
			}
			if !strings.Contains(err.Error(), "--no-team and --team cannot be combined") {
				t.Errorf("the conflict line must win, got: %v", err)
			}
			if strings.Contains(err.Error(), "needs the team's lead") {
				t.Errorf("the --team lead message must not win: %v", err)
			}
			if len(*calls) != 0 {
				t.Errorf("zero requests expected (not even the binding's App read), got %v", *calls)
			}
			if out != "" {
				t.Errorf("nothing on stdout: %q", out)
			}
		})
	}

	t.Run("--no-team=false with an empty --team keeps the existing refusal", func(t *testing.T) {
		calls, _, _, err := runFind1524(t, noTeamOK1622(), "--integration", "github", "--no-team=false", "--team", "")
		if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "needs the team's lead") {
			t.Fatalf("want the existing --team refusal, got %v", err)
		}
		if len(*calls) != 0 {
			t.Errorf("zero requests expected: %v", *calls)
		}
	})
	t.Run("--no-team=false with --team X is the ordinary filtered list", func(t *testing.T) {
		calls, _, _, err := runFind1524(t, noTeamOK1622(), "--integration", "github", "--no-team=false", "--team", "Marco")
		if err != nil {
			t.Fatal(err)
		}
		if got := findOps1622(calls); len(got) != 1 || got[0].Op != listOpOld || got[0].Vars["team"] != "Marco" {
			t.Fatalf("want the old list with team, got %v", *calls)
		}
	})
}

// Item 4: one request, no fallback, whatever the answer.
func TestFindUntrackedNoTeamOneRequestNoFallback1622(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		op   string
	}{
		{"list", nil, listOpNT},
		{"claim", []string{"--assign-to-me", "--limit", "2"}, claimOpNT},
		{"claim naming parties", []string{"--assign-to-me", "--assignee", "worker:X", "--owner", "user:Y"}, partyOpNT},
	} {
		for kind, resp := range map[string]string{
			"refusal":     `{"errors":[{"message":"refused","extensions":{"code":"GITHUB_UNAVAILABLE"}}]}`,
			"unsupported": `{"errors":[{"message":"Unknown argument \"noTeam\" on field \"Query.untrackedTaskItems\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				r := map[string]string{tc.op: resp}
				calls, _, _, err := runFind1524(t, r, append([]string{"--integration", "github", "--no-team"}, tc.argv...)...)
				if err == nil {
					t.Fatal("a refused call must fail")
				}
				if got := findOps1622(calls); len(got) != 1 || got[0].Op != tc.op {
					t.Errorf("exactly one request, never retried or widened: %v", *calls)
				}
			})
		}
	}
}

// Item 5: only an unknown noTeam, alone, on the field the request named is the
// "does not support --no-team" statement; every other failure is reported as
// before.
func TestFindUntrackedNoTeamSchemaSkew1622(t *testing.T) {
	unknown := func(arg, field string) string {
		return `{"message":"Unknown argument \"` + arg + `\" on field \"` + field + `\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	}
	other := `{"message":"Cannot query field \"surprise\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	for _, tc := range []struct {
		name  string
		argv  []string
		op    string
		field string
	}{
		{"list", nil, listOpNT, "Query.untrackedTaskItems"},
		{"claim", []string{"--assign-to-me"}, claimOpNT, "Mutation.claimUntrackedTaskItems"},
		{"claim naming parties", []string{"--assign-to-me", "--assignee", "worker:X", "--owner", "user:Y"}, partyOpNT, "Mutation.claimUntrackedTaskItems"},
	} {
		run := func(t *testing.T, errs string) error {
			t.Helper()
			r := map[string]string{tc.op: `{"errors":[` + errs + `]}`}
			calls, out, _, err := runFind1524(t, r, append([]string{"--integration", "github", "--no-team", "--json"}, tc.argv...)...)
			if out != "" {
				t.Errorf("a thrown error prints nothing on stdout: %q", out)
			}
			if err == nil {
				t.Fatal("must fail")
			}
			if got := findOps1622(calls); len(got) != 1 {
				t.Errorf("exactly one request: %v", *calls)
			}
			return err
		}
		t.Run(tc.name+"/unknown noTeam", func(t *testing.T) {
			err := run(t, unknown("noTeam", tc.field))
			if exitCodeFor(err) == exitcode.OK || !strings.Contains(err.Error(), "this server does not support --no-team") {
				t.Fatalf("want the unsupported statement, got exit %d: %v", exitCodeFor(err), err)
			}
		})
		for name, errs := range map[string]string{
			"unrelated validation error":      other,
			"mixed with another error":        unknown("noTeam", tc.field) + "," + other,
			"unknown noTeam on another field": unknown("noTeam", "Query.somethingElse"),
			"another unknown argument":        unknown("assignee", tc.field) + "," + unknown("owner", tc.field),
		} {
			t.Run(tc.name+"/"+name+" is not relabelled", func(t *testing.T) {
				err := run(t, errs)
				if strings.Contains(err.Error(), "does not support --no-team") {
					t.Fatalf("must not be relabelled: %v", err)
				}
			})
		}
	}
}

// Item 6: a server BAD_USER_INPUT is surfaced with its code.
func TestFindUntrackedNoTeamBadUserInput1622(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		op   string
	}{
		{"list", nil, listOpNT},
		{"claim", []string{"--assign-to-me"}, claimOpNT},
		{"claim naming parties", []string{"--assign-to-me", "--assignee", "worker:X", "--owner", "user:Y"}, partyOpNT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := map[string]string{tc.op: `{"errors":[{"message":"noTeam cannot be combined with team","extensions":{"code":"BAD_USER_INPUT"}}]}`}
			_, _, _, err := runFind1524(t, r, append([]string{"--integration", "github", "--no-team", "--json"}, tc.argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if env := envelope1524(t, err); env["serverCode"] != "BAD_USER_INPUT" {
				t.Errorf("serverCode = %v", env["serverCode"])
			}
		})
	}
}

// Item 7: the result shape and the help.
func TestFindUntrackedNoTeamResultAndHelp1622(t *testing.T) {
	t.Run("--json list shape is unchanged", func(t *testing.T) {
		r := noTeamOK1622()
		r[listOpNT] = untrackedPage1524(untrackedItemJSON1524, 1, true, "")
		_, out, _, err := runFind1524(t, r, "--integration", "github", "--no-team", "--json")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"items"`, `"total"`, `"complete"`, `"incompleteReason"`, `"linkedPullRequestMerged"`} {
			if !strings.Contains(out, k) {
				t.Errorf("missing %s in %s", k, out)
			}
		}
	})
	t.Run("--help names the flag, its meaning and the conflict", func(t *testing.T) {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
		root.SetOut(out)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		h := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{"--no-team", "none of the integration's team labels", "cannot be combined"} {
			if !strings.Contains(h, want) {
				t.Errorf("help is missing %q", want)
			}
		}
	})
}
