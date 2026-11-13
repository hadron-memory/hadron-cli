package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1996: `hadron team grant actions` / `action` read an App's registered
// grant-action list. Vera's two binding conditions are pinned here:
//  1. who may read is not narrowed: an UNBOUND participating person reads with
//     their own credential, a bound worker with its session;
//  2. refusals stay indistinguishable end to end: the App ref goes straight to
//     the server (no pre-resolution, no label lookup), the output echoes the
//     ref as given, and every refusal arm renders byte-identical output.

const actionJSON1996 = `{"name":"schedule.create","parameters":[{"name":"agent","type":"agent-ref","refusesWildcard":true}],"tool":"hadron_create_schedule","enforcement":"AUDITED","issuerRule":"app-manager","source":"PLATFORM","futureServerField":"must not leak"}`

const emptyActions1996 = `{"data":{"grantActions":{"items":[],"total":0}}}`

// runActions1996 runs argv against a fake server. bound=false is the unbound
// participating person: no worker binding file at all.
func runActions1996(t *testing.T, bound bool, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	if bound {
		writeTeamBinding(t)
	} else {
		teamGitDir(t)
	}
	srv, calls := attnServer(t, responses)
	if bound {
		setTeamBindingServer(t, srv.URL)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func compact1996(s string) string {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

func actionCalls1996(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "GrantActions" || c.Op == "GrantAction" {
			got = append(got, c)
		}
	}
	return got
}

func TestTeamGrantActions1996UnboundPersonLists(t *testing.T) {
	page := map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[` + actionJSON1996 + `],"total":1}}}`}
	calls, out, _, err := runActions1996(t, false, page, "team", "grant", "actions", "--app", "hrn:app:acme.com:team", "--limit", "7", "--offset", "2", "--json")
	if err != nil {
		t.Fatalf("an unbound participating person must be able to read: %v", err)
	}
	got := actionCalls1996(calls)
	if len(got) != 1 {
		t.Fatalf("calls = %+v, want exactly one GrantActions", *calls)
	}
	if got[0].Session != "" {
		t.Errorf("unbound read carried a session %q", got[0].Session)
	}
	v := got[0].Vars
	if v["appRef"] != "hrn:app:acme.com:team" || v["limit"] != float64(7) || v["offset"] != float64(2) {
		t.Errorf("vars = %v", v)
	}
	var dto struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if e := json.Unmarshal([]byte(out), &dto); e != nil {
		t.Fatal(e)
	}
	if dto.Total != 1 || len(dto.Items) != 1 {
		t.Fatalf("json = %s", out)
	}
	it := dto.Items[0]
	if it["enforcement"] != "AUDITED" || it["name"] != "schedule.create" || it["issuerRule"] != "app-manager" {
		t.Errorf("server values must pass through: %v", it)
	}
	if _, leaked := it["futureServerField"]; leaked {
		t.Errorf("unknown server field leaked: %v", it)
	}
	if _, has := it["summary"]; has {
		t.Errorf("summary is out of scope (#3708 item 11): %v", it)
	}
}

func TestTeamGrantActions1996BoundWorkerUsesSession(t *testing.T) {
	page := map[string]string{"GrantActions": emptyActions1996}
	calls, _, _, err := runActions1996(t, true, page, "team", "grant", "actions", "--app", "hrn:app:acme.com:team")
	if err != nil {
		t.Fatal(err)
	}
	got := actionCalls1996(calls)
	if len(got) != 1 || got[0].Session != "s-new" {
		t.Fatalf("bound read must carry the worker session: %+v", got)
	}
	if got[0].Vars["limit"] != float64(20) {
		t.Errorf("default limit = %v, want 20", got[0].Vars["limit"])
	}
}

func TestTeamGrantActions1996CountOnlyAndHumanDeclaredLabel(t *testing.T) {
	_, out, _, err := runActions1996(t, false, map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[],"total":9}}}`}, "team", "grant", "actions", "--app", "a1", "--limit", "0", "--json")
	if err != nil || compact1996(out) != `{"total":9}` {
		t.Fatalf("count-only: err=%v out=%q", err, out)
	}
	page := map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[` + actionJSON1996 + `],"total":1}}}`}
	_, out, _, err = runActions1996(t, false, page, "team", "grant", "actions", "--app", "a1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schedule.create", "AUDITED", "declared", "not a guarantee"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
}

// Condition 2: no pre-resolution. Every refusal arm is the same server answer,
// so the CLI must add no signal: one call only, the ref as given, output that
// differs across arms only by the echoed ref.
func TestTeamGrantActions1996RefusalsIndistinguishable(t *testing.T) {
	refs := []string{"hrn:app:acme.com:unknown", "hrn:app:acme.com:not-mine", "not a ref at all", "019ff5f31e917d1b9308a80f97c9faeb"}
	var outs, errs []string
	for _, ref := range refs {
		calls, out, errOut, err := runActions1996(t, false, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", ref)
		if err != nil {
			t.Fatalf("%q: an empty page is exit 0, got %v", ref, err)
		}
		if n := len(*calls); n != 1 || (*calls)[0].Op != "GrantActions" {
			t.Fatalf("%q: the ref must go straight to grantActions, calls = %+v", ref, *calls)
		}
		if (*calls)[0].Vars["appRef"] != ref {
			t.Errorf("ref changed on the way: %v", (*calls)[0].Vars["appRef"])
		}
		if !strings.Contains(out, ref) {
			t.Errorf("output must echo the ref as given: %q", out)
		}
		outs = append(outs, strings.ReplaceAll(out, ref, "<REF>"))
		errs = append(errs, errOut)
	}
	for i := 1; i < len(refs); i++ {
		if outs[i] != outs[0] || errs[i] != errs[0] {
			t.Errorf("refusal arms differ: %q/%q vs %q/%q", outs[0], errs[0], outs[i], errs[i])
		}
	}
	// A bound worker asking for another App is refused the same way.
	_, out, errOut, err := runActions1996(t, true, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", refs[0])
	if err != nil || strings.ReplaceAll(out, refs[0], "<REF>") != outs[0] || errOut != errs[0] {
		t.Errorf("bound arm differs: err=%v out=%q", err, out)
	}
	if !strings.Contains(outs[0], "App: <REF> (from --app)") {
		t.Errorf("the render must name the scope and its source: %q", outs[0])
	}
	// --json empty page: the same fixed shape.
	_, jout, _, _ := runActions1996(t, false, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", "x", "--json")
	if compact1996(jout) != `{"items":[],"total":0}` {
		t.Errorf("json empty page = %q", jout)
	}
}

func TestTeamGrantActions1996EmptyParametersRenderAsArray(t *testing.T) {
	one := `{"name":"x.y","parameters":[],"tool":null,"enforcement":"SERVER","issuerRule":"APP_MANAGER","source":"PLATFORM"}`
	_, out, _, err := runActions1996(t, false, map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[` + one + `],"total":1}}}`}, "team", "grant", "actions", "--app", "a1", "--json")
	if err != nil || !strings.Contains(compact1996(out), `"parameters":[]`) || strings.Contains(out, `"parameters": null`) {
		t.Fatalf("empty parameters must be [] not null: err=%v out=%s", err, out)
	}
	if !strings.Contains(compact1996(out), `"tool":null`) {
		t.Errorf("a null tool passes through as null: %s", out)
	}
}

func TestTeamGrantAction1996ReadByName(t *testing.T) {
	calls, out, _, err := runActions1996(t, false, map[string]string{"GrantAction": `{"data":{"action":` + actionJSON1996 + `}}`}, "team", "grant", "action", "schedule.create", "--app", "a1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := actionCalls1996(calls)
	if len(got) != 1 || got[0].Vars["ref"] != "schedule.create" || got[0].Vars["appRef"] != "a1" {
		t.Fatalf("calls = %+v", got)
	}
	var m map[string]any
	if e := json.Unmarshal([]byte(out), &m); e != nil || m["enforcement"] != "AUDITED" || m["name"] != "schedule.create" {
		t.Fatalf("out = %s", out)
	}
	if _, leaked := m["futureServerField"]; leaked {
		t.Error("unknown server field leaked")
	}
}

func TestTeamGrantAction1996NotFoundIdenticalAcrossArms(t *testing.T) {
	var msgs []string
	for _, ref := range []string{"hrn:app:acme.com:unknown", "hrn:app:acme.com:not-mine", "###"} {
		for _, bound := range []bool{false, true} {
			calls, out, errOut, err := runActions1996(t, bound, map[string]string{"GrantAction": `{"data":{"action":null}}`}, "team", "grant", "action", "no.such", "--app", ref)
			if err == nil || exitCodeFor(err) != exitcode.NotFound {
				t.Fatalf("want NotFound, got %v", err)
			}
			if n := len(*calls); n != 1 {
				t.Fatalf("one call only, got %+v", *calls)
			}
			if out != "" || errOut != "" {
				t.Errorf("stdout/stderr must be empty before main prints the error: %q %q", out, errOut)
			}
			msgs = append(msgs, strings.ReplaceAll(err.Error(), ref, "<REF>"))
			if !strings.Contains(err.Error(), ref) {
				t.Errorf("message must echo the ref as given: %v", err)
			}
		}
	}
	for _, m := range msgs[1:] {
		if m != msgs[0] {
			t.Errorf("not-found text differs across arms: %q vs %q", msgs[0], m)
		}
	}
}

func TestTeamGrantActions1996NeedsAnApp(t *testing.T) {
	calls, _, _, err := runActions1996(t, false, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("no App named must be a usage error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no call without an App: %+v", *calls)
	}
}
