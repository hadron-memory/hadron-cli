package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#887: `team worker list|get` show the server's Worker.family as sent, and
// `team session set-model --family` is the one deliberate path that sends it.

func withFamily(worker, family string) string {
	out := strings.Replace(worker, `"memoryId":"mw1"`, `"memoryId":"mw1","family":`+family, 1)
	if out == worker {
		panic("withFamily matched nothing — fixture shape changed")
	}
	return out
}

func runWorkerList887(t *testing.T, extra ...string) string {
	t.Helper()
	teamGitDir(t)
	rows := []string{
		withFamily(irisWorkerJSON, `"claude"`),
		strings.Replace(withFamily(irisWorkerJSON, `null`), `"id":"wkr1"`, `"id":"wkr2"`, 1),
		strings.Replace(withFamily(irisWorkerJSON, `"zeta-unmapped"`), `"id":"wkr1"`, `"id":"wkr3"`, 1),
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":         `{"data":{"workers":{"total":3,"items":[` + strings.Join(rows, ",") + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
		"AuthContext":     authContextHolgerJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL}, extra...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTeamWorkerListJSONCarriesFamilyAsSent(t *testing.T) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(runWorkerList887(t, "--json")), &rows); err != nil {
		t.Fatal(err)
	}
	want := []string{`"claude"`, "null", `"zeta-unmapped"`}
	for i, w := range want {
		got, ok := rows[i]["family"]
		if !ok {
			t.Fatalf("row %d has no family key", i)
		}
		if string(got) != w {
			t.Errorf("row %d family = %s, want %s", i, got, w)
		}
	}
}

func TestTeamWorkerListTableShowsFamilyBesideModel(t *testing.T) {
	out := runWorkerList887(t)
	header := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "WORKER") {
			header = l
		}
	}
	fam, mod := strings.Index(header, "FAMILY"), strings.Index(header, "MODEL")
	if fam < 0 || fam >= mod {
		t.Fatalf("want FAMILY before MODEL, header %q", header)
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out, "zeta-unmapped") {
		t.Errorf("family values missing:\n%s", out)
	}
}

// A null family is a dash; the host in a tool name never fills it in.
func TestTeamWorkerGetFamilyNullIsUnknown(t *testing.T) {
	teamGitDir(t)
	w := withFamily(irisWorkerJSON, `null`)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":       `{"data":{"worker":` + w + `}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &m); err != nil {
		t.Fatal(err)
	}
	if string(m["family"]) != "null" {
		t.Errorf("family = %s, want null", m["family"])
	}
}

func setModel887(t *testing.T, args ...string) (map[string]any, string, error) {
	t.Helper()
	setupSync(t, "")
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTeamSession": modelLogSessionJSON})
	out, _, err := runSyncOut(t, "", gql.URL, append([]string{"set-model"}, args...)...)
	return updateVars(t, captured), out, err
}

func usageErr(err error) bool {
	var ce *exitcode.CodedError
	return errors.As(err, &ce) && ce.Code == exitcode.Usage
}

func TestSetModelFamilyBothOnTheWire(t *testing.T) {
	v, _, err := setModel887(t, "gpt-x", "--family", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if v["llmModel"] != "gpt-x" || v["family"] != "codex" {
		t.Errorf("vars = %v", v)
	}
	for _, k := range []string{"prNumber", "branch"} {
		if _, ok := v[k]; ok {
			t.Errorf("%s must be omitted", k)
		}
	}
}

func TestSetModelFamilyAloneOmitsModel(t *testing.T) {
	v, out, err := setModel887(t, "--family", "codex", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v["family"] != "codex" {
		t.Errorf("vars = %v", v)
	}
	if _, ok := v["llmModel"]; ok {
		t.Errorf("llmModel must be omitted, never null: %v", v)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if string(r["model"]) != "null" || string(r["family"]) != `"codex"` || string(r["reported"]) != "true" {
		t.Errorf("receipt must not imply a model changed: %s", out)
	}
}

// GLM inside Claude Code: no family flag, no family on the wire — the server's
// derivation stays authoritative, and the CLI infers nothing from the host.
func TestSetModelModelAloneSendsNoFamily(t *testing.T) {
	v, out, err := setModel887(t, "glm-4.6", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v["family"]; ok {
		t.Errorf("family must be omitted: %v", v)
	}
	if strings.Contains(out, `"family"`) {
		t.Errorf("model-only receipt keeps its old shape: %s", out)
	}
	if !strings.Contains(out, `"model":"glm-4.6"`) {
		t.Errorf("receipt = %s", out)
	}
}

func TestSetModelArgumentContract(t *testing.T) {
	for name, args := range map[string][]string{
		"no args":         {},
		"two models":      {"a", "b"},
		"two + family":    {"a", "b", "--family", "codex"},
		"empty family":    {"m", "--family", ""},
		"empty family 2":  {"--family", ""},
		"blank family":    {"m", "--family", " "},
		"newline family":  {"m", "--family", "a\nb"},
		"overlong family": {"m", "--family", strings.Repeat("a", 129)},
		"empty model":     {""},
	} {
		t.Run(name, func(t *testing.T) {
			v, _, err := setModel887(t, args...)
			if !usageErr(err) {
				t.Errorf("want usage error, got %v", err)
			}
			if v != nil {
				t.Errorf("nothing may be sent: %v", v)
			}
		})
	}
}

// An unnormalizable token goes to the server as typed, and its refusal is
// surfaced with no success receipt.
func TestSetModelFamilyServerRefusalSurfaces(t *testing.T) {
	setupSync(t, "")
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"errors":[{"message":"unknown family","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`,
	})
	out, _, err := runSyncOut(t, "", gql.URL, "set-model", "m", "--family", "Not A Family")
	if err == nil {
		t.Fatal("server refusal must be an error")
	}
	if strings.Contains(out, "reported") {
		t.Errorf("no success receipt on refusal: %q", out)
	}
}

func TestSetModelHumanReceiptNamesOnlyWhatWasSent(t *testing.T) {
	_, out, err := setModel887(t, "--family", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "reported model") {
		t.Errorf("family-only receipt must not claim a model: %q", out)
	}
	if !strings.Contains(out, "family") || !strings.Contains(out, "codex") {
		t.Errorf("receipt = %q", out)
	}
}

// The hook stays model-only and quiet: no --family on sync-model.
func TestSyncModelHasNoFamilyFlag(t *testing.T) {
	env := setupSync(t, syncRecord("claude-opus-5-5"))
	gql, captured := captureGraphQL(t, map[string]string{
		"GetTeamSession":    sessionWithTranscript(env.transcript),
		"UpdateTeamSession": modelLogSessionJSON,
	})
	_, _, err := runSyncOut(t, hookJSON(t, env), gql.URL, "sync-model", "--hook", "--family", "codex")
	if err == nil {
		t.Error("sync-model must refuse --family")
	}
	if updateVars(t, captured) != nil {
		t.Error("nothing may be sent")
	}
}
