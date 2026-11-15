package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2013 (hadron-server#3997 S2): App secrets, set, versions, events.

const appSecretJSON = `{"id":"sec_app","ownerType":"app","ownerId":"app1","name":"deploy-token","kind":"generic",
	"metadata":null,"currentVersion":3,"protected":true,"createdAt":"2026-11-14T00:00:00Z","createdBy":"u1",
	"updatedAt":"2026-11-14T00:00:00Z","updatedBy":"u1"}`

func runSecret(t *testing.T, stdin string, responses map[string]string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"secret"}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

func TestSecretCreateAndListAcceptScopeApp(t *testing.T) {
	_, cap, err := runSecret(t, "tok\n", map[string]string{"CreateSecret": `{"data":{"createSecret":` + appSecretJSON + `}}`},
		"create", "--name", "deploy-token", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", "-", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ OwnerType, OwnerRef string }
	_ = json.Unmarshal(cap["CreateSecret"], &v)
	if v.OwnerType != "app" || v.OwnerRef != "hrn:app:acme.com:ops" {
		t.Fatalf("vars %s", cap["CreateSecret"])
	}
	_, cap, err = runSecret(t, "", map[string]string{"Secrets": `{"data":{"secrets":{"items":[],"total":0}}}`},
		"list", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--json")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(cap["Secrets"], &v)
	if v.OwnerType != "app" || v.OwnerRef != "hrn:app:acme.com:ops" {
		t.Fatalf("vars %s", cap["Secrets"])
	}
	// --scope app, like org, needs the owner named.
	_, _, err = runSecret(t, "", nil, "list", "--scope", "app")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("scope app without --owner: %v", err)
	}
}

func TestSecretSetCallsUpdateWithOnlyTheValueAndPrintsTheVersion(t *testing.T) {
	out, cap, err := runSecret(t, "new-secret-value\n", map[string]string{"UpdateSecret": `{"data":{"updateSecret":` + appSecretJSON + `}}`},
		"set", "sec_app", "--value-file", "-")
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(cap["UpdateSecret"], &vars)
	if vars["ref"] != "sec_app" || vars["value"] != "new-secret-value" {
		t.Fatalf("vars %v", vars)
	}
	if _, has := vars["metadata"]; has {
		t.Fatalf("set must not send metadata (omitted = preserve): %v", vars)
	}
	if !strings.Contains(out, "current version: 3") || strings.Contains(out, "new-secret-value") {
		t.Fatalf("output %q", out)
	}
}

func TestSecretSetShowsNoVersionForAnOrgSecretAndNeverTakesArgv(t *testing.T) {
	org := strings.Replace(appSecretJSON, `"currentVersion":3`, `"currentVersion":null`, 1)
	out, _, err := runSecret(t, "v\n", map[string]string{"UpdateSecret": `{"data":{"updateSecret":` + org + `}}`}, "set", "s", "--value-file", "-")
	if err != nil || !strings.Contains(out, "current version: -") {
		t.Fatalf("%v %q", err, out)
	}
	// no --value-file: refused before any call (usage), value is never argv.
	_, cap, err := runSecret(t, "", nil, "set", "s")
	if err == nil || exitCodeFor(err) != exitcode.Usage || cap["UpdateSecret"] != nil {
		t.Fatalf("set without --value-file: %v %v", err, cap)
	}
	if _, _, err = runSecret(t, "v\n", nil, "set", "s", "positional-value", "--value-file", "-"); err == nil {
		t.Fatal("a positional value must be refused")
	}
}

func TestSecretGetJSONCarriesCurrentVersionAndProtected(t *testing.T) {
	out, _, err := runSecret(t, "", map[string]string{"Secret": `{"data":{"secret":` + appSecretJSON + `}}`}, "get", "sec_app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		CurrentVersion *int `json:"currentVersion"`
		Protected      bool `json:"protected"`
	}
	if json.Unmarshal([]byte(out), &d) != nil || d.CurrentVersion == nil || *d.CurrentVersion != 3 || !d.Protected {
		t.Fatalf("%s", out)
	}
}

const versionsResp = `{"data":{"secret":{"id":"sec_app","name":"deploy-token","currentVersion":3,"protected":false,
 "versions":{"total":3,"hasMore":false,"items":[
  {"versionNo":3,"sizeBytes":12,"writtenAt":"2026-11-14T03:00:00Z","supersededAt":null,"writtenByUserId":"u1","writtenByWorkerId":null},
  {"versionNo":2,"sizeBytes":10,"writtenAt":"2026-11-13T03:00:00Z","supersededAt":"2026-11-14T03:00:00Z","writtenByUserId":null,"writtenByWorkerId":"w1"}]}}}}`

func TestSecretVersionsShowsServerFieldsAndOmitsUnsetPaging(t *testing.T) {
	out, cap, err := runSecret(t, "", map[string]string{"SecretVersions": versionsResp}, "versions", "sec_app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		CurrentVersion int
		Total          int
		Versions       []struct{ VersionNo, SizeBytes int }
	}
	if json.Unmarshal([]byte(out), &d) != nil || d.CurrentVersion != 3 || len(d.Versions) != 2 || d.Versions[0].VersionNo != 3 {
		t.Fatalf("%s", out)
	}
	if strings.Contains(string(cap["SecretVersions"]), "limit") || strings.Contains(string(cap["SecretVersions"]), "offset") {
		t.Fatalf("unset --limit/--offset must be omitted (limit 0 means count-only): %s", cap["SecretVersions"])
	}
	human, _, _ := runSecret(t, "", map[string]string{"SecretVersions": versionsResp}, "versions", "sec_app")
	if !strings.Contains(human, "current version: 3") || !strings.Contains(human, "12 B") {
		t.Fatalf("%q", human)
	}
}

func TestSecretVersionsOfAnOrgSecretIsNoVersionsNotInferred(t *testing.T) {
	empty := `{"data":{"secret":{"id":"s","name":"n","currentVersion":null,"protected":false,"versions":{"total":0,"hasMore":false,"items":[]}}}}`
	out, _, err := runSecret(t, "", map[string]string{"SecretVersions": empty}, "versions", "s", "--json")
	if err != nil || !strings.Contains(out, `"versions": []`) && !strings.Contains(out, `"versions":[]`) || !strings.Contains(out, "null") {
		t.Fatalf("%v %s", err, out)
	}
	human, _, _ := runSecret(t, "", map[string]string{"SecretVersions": empty}, "versions", "s")
	if !strings.Contains(human, "no versions") || !strings.Contains(human, "current version: -") {
		t.Fatalf("%q", human)
	}
}

func TestSecretVersionsNotFoundAndPassThroughPaging(t *testing.T) {
	_, _, err := runSecret(t, "", map[string]string{"SecretVersions": `{"data":{"secret":null}}`}, "versions", "nope")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("%v", err)
	}
	_, cap, err := runSecret(t, "", map[string]string{"SecretVersions": versionsResp}, "versions", "s", "--limit", "5", "--offset", "10")
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Limit, Offset int }
	_ = json.Unmarshal(cap["SecretVersions"], &v)
	if v.Limit != 5 || v.Offset != 10 {
		t.Fatalf("%s", cap["SecretVersions"])
	}
	if _, _, err = runSecret(t, "", nil, "versions", "s", "--all", "--limit", "5"); err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("--all with --limit: %v", err)
	}
}

const eventsResp = `{"data":{"secretEvents":{"total":1,"items":[
 {"id":"e1","at":"2026-11-14T03:00:00Z","kind":"VERSION","outcome":"OK","secretId":"s","secretName":"n","versionNo":3,"actorUserId":"u1","workerId":null,"sessionId":null}]}}}`

func TestSecretEventsShowsItemsAndPassesPagingThrough(t *testing.T) {
	out, cap, err := runSecret(t, "", map[string]string{"SecretEvents": eventsResp}, "events", "s", "--limit", "7", "--offset", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Total  int
		Events []struct{ Kind string }
	}
	if json.Unmarshal([]byte(out), &d) != nil || d.Total != 1 || len(d.Events) != 1 || d.Events[0].Kind != "VERSION" {
		t.Fatalf("%s", out)
	}
	var v struct{ Limit, Offset int }
	_ = json.Unmarshal(cap["SecretEvents"], &v)
	if v.Limit != 7 || v.Offset != 2 {
		t.Fatalf("%s", cap["SecretEvents"])
	}
	_, cap, _ = runSecret(t, "", map[string]string{"SecretEvents": eventsResp}, "events", "s")
	if strings.Contains(string(cap["SecretEvents"]), "limit") {
		t.Fatalf("unset paging must be omitted: %s", cap["SecretEvents"])
	}
}

// cor:sec:020:02: an excluded caller's empty page is printed as empty, with
// success, never as a refusal.
func TestSecretEventsEmptyPageIsEmptyNotForbidden(t *testing.T) {
	empty := `{"data":{"secretEvents":{"total":0,"items":[]}}}`
	out, _, err := runSecret(t, "", map[string]string{"SecretEvents": empty}, "events", "s")
	if err != nil || !strings.Contains(out, "no events") || strings.Contains(strings.ToLower(out), "forbidden") {
		t.Fatalf("%v %q", err, out)
	}
	j, _, err := runSecret(t, "", map[string]string{"SecretEvents": empty}, "events", "s", "--json")
	if err != nil || (!strings.Contains(j, `"events": []`) && !strings.Contains(j, `"events":[]`)) {
		t.Fatalf("%v %s", err, j)
	}
}

// pagedServer answers SecretVersions / SecretEvents by the request's offset
// from one logical list of `total` rows, recording each (limit, offset).
func pagedServer(t *testing.T, total, pageSize int) (*httptest.Server, *[][2]int) {
	t.Helper()
	var mu sync.Mutex
	seen := &[][2]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct{ Limit, Offset *int }
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		off := 0
		if body.Variables.Offset != nil {
			off = *body.Variables.Offset
		}
		lim := 0
		if body.Variables.Limit != nil {
			lim = *body.Variables.Limit
		}
		mu.Lock()
		*seen = append(*seen, [2]int{lim, off})
		mu.Unlock()
		n := min(pageSize, total-off)
		var items []string
		for k := 0; k < n; k++ {
			if body.OperationName == "SecretEvents" {
				items = append(items, fmt.Sprintf(`{"id":"e%d","at":"2026-11-14T03:00:00Z","kind":"VERSION","outcome":"OK","secretId":"s","secretName":"n","versionNo":%d,"actorUserId":null,"workerId":null,"sessionId":null}`, off+k, off+k))
			} else {
				items = append(items, fmt.Sprintf(`{"versionNo":%d,"sizeBytes":1,"writtenAt":"2026-11-14T03:00:00Z","supersededAt":null,"writtenByUserId":null,"writtenByWorkerId":null}`, total-off-k))
			}
		}
		list := strings.Join(items, ",")
		if body.OperationName == "SecretEvents" {
			fmt.Fprintf(w, `{"data":{"secretEvents":{"total":%d,"items":[%s]}}}`, total, list)
		} else {
			fmt.Fprintf(w, `{"data":{"secret":{"id":"s","name":"n","currentVersion":%d,"protected":false,"versions":{"total":%d,"hasMore":%t,"items":[%s]}}}}`, total, total, off+n < total, list)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// F1: --all walks offset pages until total, with increasing offsets, no
// duplicates, and stops at total.
func TestSecretAllWalksEveryPageForVersionsAndEvents(t *testing.T) {
	for _, sub := range []string{"versions", "events"} {
		srv, seen := pagedServer(t, 450, 200)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"secret", sub, "s", "--all", "--json", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		want := [][2]int{{200, 0}, {200, 200}, {200, 400}}
		if fmt.Sprint(*seen) != fmt.Sprint(want) {
			t.Fatalf("%s requests (limit, offset) = %v, want %v", sub, *seen, want)
		}
		var d struct {
			Total    int
			Versions []struct{ VersionNo int }
			Events   []struct{ ID string }
		}
		if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, e := range d.Events {
			ids[e.ID] = true
		}
		if d.Total != 450 || len(d.Versions)+len(d.Events) != 450 || (sub == "events" && len(ids) != 450) {
			t.Fatalf("%s: total %d, %d versions, %d events, %d distinct", sub, d.Total, len(d.Versions), len(d.Events), len(ids))
		}
	}
}

func TestSecretNegativePagingIsRefusedLocally(t *testing.T) {
	for _, sub := range []string{"versions", "events"} {
		for _, flag := range []string{"--limit", "--offset"} {
			_, cap, err := runSecret(t, "", nil, sub, "s", flag, "-1")
			if err == nil || exitCodeFor(err) != exitcode.Usage || len(cap) != 0 {
				t.Fatalf("%s %s -1: %v %v", sub, flag, err, cap)
			}
		}
	}
}

// QA F1: a server refusal keeps its extensions.code (SECRET_PROTECTED) for the
// --json envelope on every secret command that can draw it.
func TestSecretRefusalsKeepServerCode(t *testing.T) {
	refusal := `{"errors":[{"message":"This secret is protected: it changes only through a change request.","extensions":{"code":"SECRET_PROTECTED"}}]}`
	for _, tc := range []struct {
		op    string
		stdin string
		args  []string
	}{
		{"UpdateSecret", "v\n", []string{"set", "s", "--value-file", "-"}},
		{"UpdateSecret", "v\n", []string{"update", "s", "--value-file", "-"}},
		{"DeleteSecret", "", []string{"rm", "s", "--yes"}},
	} {
		_, _, err := runSecret(t, tc.stdin, map[string]string{tc.op: refusal}, tc.args...)
		var coded interface{ ServerCode() string }
		if err == nil || !errors.As(err, &coded) || coded.ServerCode() != "SECRET_PROTECTED" {
			t.Fatalf("%v: err=%v must carry serverCode SECRET_PROTECTED", tc.args, err)
		}
	}
}

// QA F2: a count-only page (--limit 0) with a non-zero total is not "none".
func TestSecretCountOnlyPageIsNotNoneAtAll(t *testing.T) {
	v := `{"data":{"secret":{"id":"s","name":"n","currentVersion":3,"protected":false,"versions":{"total":3,"hasMore":true,"items":[]}}}}`
	out, _, err := runSecret(t, "", map[string]string{"SecretVersions": v}, "versions", "s", "--limit", "0")
	if err != nil || strings.Contains(out, "no versions") || !strings.Contains(out, "3 kept versions") {
		t.Fatalf("%v %q", err, out)
	}
	e := `{"data":{"secretEvents":{"total":4,"items":[]}}}`
	out, _, err = runSecret(t, "", map[string]string{"SecretEvents": e}, "events", "s", "--limit", "0")
	if err != nil || strings.Contains(out, "no events") || !strings.Contains(out, "4 events") {
		t.Fatalf("%v %q", err, out)
	}
}

// Rook P3-1: currentVersion is the server's field, never the newest listed version.
func TestSecretVersionsCurrentVersionIsTheFieldNotTheList(t *testing.T) {
	r := `{"data":{"secret":{"id":"s","name":"n","currentVersion":7,"protected":false,"versions":{"total":1,"hasMore":false,"items":[
	 {"versionNo":3,"sizeBytes":1,"writtenAt":"2026-11-14T03:00:00Z","supersededAt":null,"writtenByUserId":null,"writtenByWorkerId":null}]}}}}`
	out, _, err := runSecret(t, "", map[string]string{"SecretVersions": r}, "versions", "s")
	if err != nil || !strings.Contains(out, "current version: 7") {
		t.Fatalf("%v %q", err, out)
	}
}

// Rook P3-2 (cor:sec:020:01): help never says a run can read an App secret.
func TestSecretHelpSaysARunNeverReadsAnAppSecret(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"create", "--help"}} {
		out, _, _ := runSecret(t, "", nil, args...)
		if !strings.Contains(out, "never resolve") && !strings.Contains(out, "never resolves") {
			t.Fatalf("secret %v help must say a run never resolves an App secret:\n%s", args, out)
		}
		if strings.Contains(out, "run can read an App secret") {
			t.Fatalf("help suggests a run reads an App secret")
		}
	}
}

func TestSecretEventsAllRefusesLimitAndOffset(t *testing.T) {
	_, _, err := runSecret(t, "", nil, "events", "s", "--all", "--offset", "2")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("%v", err)
	}
}
