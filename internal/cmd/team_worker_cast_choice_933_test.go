package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// castChoiceArgs builds a `team worker cast` argv for the #933 cast-choice
// tests: a fixed App, role and name, plus whatever choice flags the case sets.
func castChoiceArgs(url string, extra ...string) []string {
	args := []string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--name", "Iris", "--json", "--server", url}
	return append(args, extra...)
}

func castVarsOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables: %v (%s)", err, raw)
	}
	return vars
}

// #933: the wire matrix. A named reportsTo sends ONLY reportsTo; --no-team
// sends ONLY noTeam:true; neither sends neither (omitted, never null or
// false) — on the cast AND on the dry-run preview.
func TestCastChoiceWireMatrix(t *testing.T) {
	for _, op := range []struct {
		name, opName, dryRun string
	}{
		{"cast", "CastWorker", ""},
		{"preview", "CastWorkerPreview", "--dry-run"},
	} {
		for _, c := range []struct {
			name     string
			flags    []string
			wantRT   any // nil: key must be absent
			wantNoTm any
		}{
			{"reports-to only", []string{"--reports-to", "Ada"}, "Ada", nil},
			{"no-team only", []string{"--no-team"}, nil, true},
			{"neither", nil, nil, nil},
			{"reports-to is sent as given (URN)", []string{"--reports-to", "hrn:worker:acme.com:eng-team:ada"}, "hrn:worker:acme.com:eng-team:ada", nil},
		} {
			t.Run(op.name+"/"+c.name, func(t *testing.T) {
				gql, captured := captureGraphQL(t, map[string]string{
					"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
					"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Iris","role":"backend-engineer",
						"agentId":"agt1","agentName":"backend-engineer","prompt":null,"teamPlacement":"UNSET","reportsToWorkerId":null}}}`,
					"TeamRoles": teamRolesJSON,
				})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := castChoiceArgs(gql.URL, c.flags...)
				if op.dryRun != "" {
					args = append(args, op.dryRun)
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("execute: %v", err)
				}
				vars := castVarsOf(t, captured[op.opName])
				for key, want := range map[string]any{"reportsTo": c.wantRT, "noTeam": c.wantNoTm} {
					got, present := vars[key]
					if want == nil && present {
						t.Errorf("%s must be OMITTED (never null/false), got %v", key, got)
					}
					if want != nil && got != want {
						t.Errorf("%s = %v, want %v", key, got, want)
					}
				}
			})
		}
	}
}

// #933: shape errors the CLI can see are refused locally, as usage (2), with
// NO request sent — no mutation, no preview, no retry.
func TestCastChoiceLocalRefusalsSendNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"both", []string{"--reports-to", "Ada", "--no-team"}, "never both"},
		{"both, no-team explicitly false", []string{"--reports-to", "Ada", "--no-team=false"}, "never both"},
		{"no-team=false alone", []string{"--no-team=false"}, "--no-team"},
		{"blank reports-to", []string{"--reports-to", ""}, "--reports-to"},
		{"whitespace reports-to", []string{"--reports-to", "   "}, "--reports-to"},
		{"blank reports-to with no-team", []string{"--reports-to", "", "--no-team"}, "never both"},
	} {
		for _, dry := range []bool{false, true} {
			t.Run(c.name, func(t *testing.T) {
				gql, captured := captureGraphQL(t, map[string]string{})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := castChoiceArgs(gql.URL, c.flags...)
				if dry {
					args = append(args, "--dry-run")
				}
				root.SetArgs(args)
				err := root.Execute()
				if code := exitCodeFor(err); code != exitcode.Usage {
					t.Errorf("exit = %d, want usage (2); err: %v", code, err)
				}
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("message must mention %q: %v", c.want, err)
				}
				if len(captured) != 0 {
					t.Errorf("a local refusal must send nothing, saw %v", captured)
				}
			})
		}
	}
}

// #933: the server's typed refusals surface verbatim, mapped, with exactly one
// request: no retry, no alternative choice.
func TestCastChoiceServerRefusalsVerbatimNoRetry(t *testing.T) {
	for _, c := range []struct {
		code, msg string
	}{
		{"WORKER_TEAM_CHOICE_CONFLICT", "Supply reportsTo or noTeam, never both."},
		{"WORKER_TEAM_CHOICE_REQUIRED", "Choose reportsTo or noTeam: true when casting a worker."},
		{"WORKER_TEAM_CHOICE_INVALID", "reportsTo must be a non-empty worker reference, or noTeam must be true."},
		{"WORKER_NOT_FOUND", "Worker not found in this App."},
	} {
		t.Run(c.code, func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op == "CastWorker" {
					calls++
				}
				return `{"errors":[{"message":"` + c.msg + `","extensions":{"code":"` + c.code + `"}}]}`
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(castChoiceArgs(gql.URL, "--reports-to", "Ada"))
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("server message must surface verbatim: %v", err)
			}
			want := exitcode.Usage
			if c.code == "WORKER_NOT_FOUND" {
				want = exitcode.NotFound
			}
			if got := exitCodeFor(err); got != want {
				t.Errorf("exit = %d, want %d", got, want)
			}
			if calls != 1 {
				t.Errorf("the cast is attempted once and never retried, saw %d", calls)
			}
		})
	}
}

// #933: the recorded placement reads back as returned. UNSET, NO_TEAM and
// REPORTS_TO are three states; null is WITHHELD (access-masked) and is never
// rendered as UNSET or NO_TEAM, in text or JSON.
func TestCastChoicePlacementReadback(t *testing.T) {
	worker := func(placement string) string {
		return strings.Replace(irisWorkerJSON, `"createdBy":"u-holger"}`,
			`"createdBy":"u-holger","teamPlacement":`+placement+`}`, 1)
	}
	for _, c := range []struct {
		name, placement, wantJSON, wantText string
	}{
		{"unset", `"UNSET"`, `"UNSET"`, "no reporting choice recorded (UNSET)"},
		{"no team", `"NO_TEAM"`, `"NO_TEAM"`, "no team"},
		{"reports to", `"REPORTS_TO"`, `"REPORTS_TO"`, "reports to"},
		{"withheld", `null`, `null`, "withheld"},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := map[string]string{"CastWorker": `{"data":{"castWorker":` + worker(c.placement) + `}}`, "TeamRoles": teamRolesJSON}

			gql, _ := captureGraphQL(t, resp)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(castChoiceArgs(gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto map[string]json.RawMessage
			_ = json.Unmarshal([]byte(out.String()), &dto)
			if got, ok := dto["teamPlacement"]; !ok || string(got) != c.wantJSON {
				t.Errorf("--json teamPlacement = %s (present=%v), want %s", got, ok, c.wantJSON)
			}

			gql2, _ := captureGraphQL(t, resp)
			f2, out2 := testFactory(t)
			root2 := NewRootCmd(f2)
			args := castChoiceArgs(gql2.URL)
			root2.SetArgs(append(args[:len(args)-3:len(args)-3], "--server", gql2.URL)) // drop --json
			if err := root2.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out2.String(), c.wantText) {
				t.Errorf("human output must say %q: %s", c.wantText, out2.String())
			}
			if c.name == "withheld" && (strings.Contains(out2.String(), "UNSET") || strings.Contains(out2.String(), "no team")) {
				t.Errorf("a withheld placement must never read as UNSET or no team: %s", out2.String())
			}
		})
	}
}

// #933: the dry-run shows the placement the cast would record; it is not an
// authorization and reserves nothing.
func TestCastChoicePreviewPlacement(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Iris","role":"backend-engineer",
			"agentId":"agt1","agentName":"backend-engineer","prompt":null,
			"teamPlacement":"NO_TEAM","reportsToWorkerId":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(castChoiceArgs(gql.URL, "--no-team", "--dry-run"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if string(dto["teamPlacement"]) != `"NO_TEAM"` {
		t.Errorf("preview --json teamPlacement: %s", out.String())
	}
}

// #933 / Victor's FIT: the help says to choose deliberately and that a cast
// without a choice will be refused ONCE ENFORCEMENT IS ON — never that it is
// refused now — and promises nothing about clearing or re-binding.
func TestCastChoiceHelpWording(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"--reports-to", "--no-team", "once enforcement is on", "Choose deliberately"} {
		if !strings.Contains(help, want) {
			t.Errorf("cast --help must carry %q", want)
		}
	}
	for _, banned := range []string{"clear to no team", "clearing a line", "on a later clear", "re-bind"} {
		if strings.Contains(strings.ToLower(help), banned) {
			t.Errorf("cast --help must not promise %q (S6/S7 unbuilt)", banned)
		}
	}
}
