package cmd

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3197: one signing request over a selection of grants (hadron-server#5001,
// cor:acl:140:07). Every pin here is red on a main without the three verbs.

const ceremonySel3197 = "https://hadronmemory.com/grant-selection-signing/sel1?a=b&c=%2Fd/é"

func item3197(id, grant, url string) string {
	u, _ := json.Marshal(url)
	return `{"id":"` + id + `","grantId":"` + grant + `","version":1,"termsHash":"h-` + grant + `","payloadVersion":"hadron-grant-selection-signature-v1","state":"OPEN",` +
		`"requestedBy":{"userId":"u1","appId":null,"workerId":"wkr1","sessionId":"s-new"},` +
		`"createdAt":"c","expiresAt":"e","endedAt":null,"lastRefusedAt":null,"ceremonyUrl":` + string(u) + `,"futureItemField":"x"}`
}

func selection3197(url string) string {
	u, _ := json.Marshal(url)
	return `{"id":"sel1","state":"OPEN","payloadVersion":"hadron-grant-selection-signature-v1",` +
		`"requestedBy":{"userId":"u1","appId":null,"workerId":"wkr1","sessionId":"s-new"},` +
		`"createdAt":"c","expiresAt":"2026-11-26T00:00:00Z","endedAt":null,"lastRefusedAt":"2026-11-25T12:00:00Z","ceremonyUrl":` + string(u) + `,` +
		`"items":[` + item3197("r1", "g1", url) + `,` + item3197("r2", "g2", url) + `,` + item3197("r3", "g3", url) + `],"futureServerField":"x"}`
}

type verb3197 struct {
	name, op, root string
	args           []string
}

func verbs3197() []verb3197 {
	return []verb3197{
		{"request", "TeamRequestGrantSelectionSignature", "requestGrantSelectionSignature", []string{"team", "grant", "request-selection-signature", "g3", "g1", "g2"}},
		{"get", "TeamGrantSigningSelection", "grantSigningSelection", []string{"team", "grant", "signing-request", "get-selection", "sel1"}},
		{"withdraw", "TeamWithdrawGrantSigningSelection", "withdrawGrantSigningSelection", []string{"team", "grant", "signing-request", "withdraw-selection", "sel1"}},
	}
}

func (v verb3197) ok(url string) string {
	return `{"data":{"` + v.root + `":` + selection3197(url) + `}}`
}

// Acceptance 1: three ids, ONE call carrying all three as given, and the one
// ceremonyUrl printed unmodified, with the item count and the expiry.
func TestGrantSelection3197OneCallVerbatimURL(t *testing.T) {
	v := verbs3197()[0]
	calls, out, err := runTeamTask(t, map[string]string{v.op: v.ok(ceremonySel3197)}, v.args...)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != v.op || (*calls)[0].Session != "s-new" {
		t.Fatalf("calls %+v", *calls)
	}
	var vars map[string]any
	if err := json.Unmarshal((*calls)[0].RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"grantRefs": []any{"g3", "g1", "g2"}}; !reflect.DeepEqual(vars, want) {
		t.Fatalf("vars %v", vars)
	}
	lines := strings.Split(out, "\n")
	for _, want := range []string{"ceremonyUrl: " + ceremonySel3197, "items: 3", "expiresAt: 2026-11-26T00:00:00Z", "id: sel1", "state: OPEN", "lastRefusedAt: 2026-11-25T12:00:00Z"} {
		if !slices.Contains(lines, want) {
			t.Errorf("output lacks line %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, ceremonySel3197); n != 1 {
		t.Errorf("ceremony URL printed %d times, want 1:\n%s", n, out)
	}
	for _, g := range []string{"g1", "r1", "g2", "r2", "g3", "r3"} {
		if !strings.Contains(out, g) {
			t.Errorf("no item row naming %s:\n%s", g, out)
		}
	}
}

// The CLI owns no cap: a list of 30 is sent whole and the server judges it.
// A repeated id is sent as given too.
func TestGrantSelection3197NoPreCheck(t *testing.T) {
	v := verbs3197()[0]
	args := []string{"team", "grant", "request-selection-signature", "g1", "g1"}
	for i := 0; i < 28; i++ {
		args = append(args, "x")
	}
	calls, _, err := runTeamTask(t, map[string]string{v.op: gqlErrorJSON("SELECTION_TOO_LARGE")}, args...)
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 1 {
		t.Fatalf("exit %d calls %d err %v", exitCodeFor(err), len(*calls), err)
	}
	var vars struct{ GrantRefs []string }
	if err := json.Unmarshal((*calls)[0].RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(vars.GrantRefs, args[3:]) {
		t.Fatalf("sent %v", vars.GrantRefs)
	}
}

func TestGrantSelection3197BlankAndMissingIDsSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"team", "grant", "request-selection-signature"},
		{"team", "grant", "request-selection-signature", "g1", "  "},
		{"team", "grant", "signing-request", "get-selection", " "},
		{"team", "grant", "signing-request", "withdraw-selection", ""},
	} {
		calls, _, err := runTeamTask(t, map[string]string{}, args...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Errorf("%v: exit %d calls %v err %v", args, exitCodeFor(err), *calls, err)
		}
	}
}

// Acceptance 2: the --json shape, exactly, with nothing the server did not return.
func TestGrantSelection3197JSONShape(t *testing.T) {
	for _, v := range verbs3197() {
		t.Run(v.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{v.op: v.ok(ceremonySel3197)}, append(v.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			clean := strings.NewReplacer(`,"futureItemField":"x"`, "", `,"futureServerField":"x"`, "").Replace(selection3197(ceremonySel3197))
			if err := json.Unmarshal([]byte(clean), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s\nwant %s", out, clean)
			}
			// Every field the DTO carries is one the operation selects.
			fields, requestedBy := selection1868(t, findCall1868(calls, v.op).Query, v.root)
			wantFields := []string{"ceremonyUrl", "createdAt", "endedAt", "expiresAt", "id", "items", "lastRefusedAt", "payloadVersion", "requestedBy", "state"}
			if !slices.Equal(fields, wantFields) || !slices.Equal(requestedBy, []string{"appId", "sessionId", "userId", "workerId"}) {
				t.Fatalf("selected %v / %v", fields, requestedBy)
			}
		})
	}
}

func TestGrantSelection3197EmptyItemsRenderAsArray(t *testing.T) {
	v := verbs3197()[1]
	resp := `{"data":{"grantSigningSelection":{"id":"sel1","state":"OPEN","payloadVersion":"p","requestedBy":null,"createdAt":"c","expiresAt":"e","endedAt":null,"lastRefusedAt":null,"ceremonyUrl":"https://x/y","items":[]}}}`
	_, out, err := runTeamTask(t, map[string]string{v.op: resp}, append(v.args, "--json")...)
	if err != nil || !strings.Contains(out, `"items": []`) {
		t.Fatalf("err %v out %s", err, out)
	}
	// cli#3234: the schema says requestedBy is never null; if one arrives the
	// emitted key is still the object, each of its four fields null.
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"userId": nil, "appId": nil, "workerId": nil, "sessionId": nil}
	if got, ok := d["requestedBy"].(map[string]any); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("requestedBy %#v", d["requestedBy"])
	}
}

func refusal3197(code, message string, extensions map[string]any) string {
	ext := map[string]any{"code": code}
	for k, v := range extensions {
		ext[k] = v
	}
	b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": ext}}})
	return string(b)
}

// Acceptances 3 and 5, and the gate's added FORBIDDEN rows: the server's code
// and message, the mapped exit, and the extension the server sent.
func TestGrantSelection3197Refusals(t *testing.T) {
	cases := []struct {
		verb       int
		code       string
		ext        map[string]any
		want       int
		text       []string
		extensions map[string]any
	}{
		{0, "SELECTION_EMPTY", nil, 2, nil, nil},
		{0, "SELECTION_TOO_LARGE", map[string]any{"cap": 25}, 2, []string{"cap: 25"}, map[string]any{"cap": 25.0}},
		{0, "SELECTION_DUPLICATE_GRANT", map[string]any{"grantRef": "g1"}, 2, []string{"grantRef: g1"}, map[string]any{"grantRef": "g1"}},
		{0, "SELECTION_MIXED_APPS", nil, 2, nil, nil},
		{0, "GRANT_NOT_FOUND", map[string]any{"grantRef": "g2"}, 4, []string{"grantRef: g2"}, map[string]any{"grantRef": "g2"}},
		{0, "GRANT_NOT_PENDING", map[string]any{"grantRef": "g2"}, 5, []string{"grantRef: g2"}, map[string]any{"grantRef": "g2"}},
		{0, "GRANT_EXPIRED", map[string]any{"grantRef": "g3"}, 5, []string{"grantRef: g3"}, map[string]any{"grantRef": "g3"}},
		{0, "GRANT_TERMS_UNREPRESENTABLE", map[string]any{"grantRef": "g3", "reason": "BAD_VALUE"}, 5,
			[]string{"grantRef: g3", "reason: BAD_VALUE"}, map[string]any{"grantRef": "g3", "reason": "BAD_VALUE"}},
		{0, "FORBIDDEN", nil, 8, nil, nil},
		{2, "FORBIDDEN", nil, 8, nil, nil},
		{2, "SIGNING_REQUEST_NOT_FOUND", map[string]any{"id": "sel1"}, 4, nil, nil},
		{2, "SIGNING_REQUEST_NOT_REQUESTER", map[string]any{"id": "sel1"}, 8, nil, nil},
		{2, "SIGNING_REQUEST_ENDED", map[string]any{"id": "sel1", "state": "COMPLETED"}, 5, []string{"state: COMPLETED"}, map[string]any{"state": "COMPLETED"}},
	}
	for _, tc := range cases {
		v := verbs3197()[tc.verb]
		t.Run(v.name+"/"+tc.code, func(t *testing.T) {
			resp := map[string]string{v.op: refusal3197(tc.code, "server words for it.", tc.ext)}
			_, out, err := runTeamTask(t, resp, v.args...)
			if err == nil || exitCodeFor(err) != tc.want || serverCode1669(err) != tc.code || out != "" {
				t.Fatalf("exit %d err %v out %q, want %d", exitCodeFor(err), err, out, tc.want)
			}
			for _, s := range append([]string{tc.code, "server words for it."}, tc.text...) {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("message lacks %q: %v", s, err)
				}
			}
			r := runTeamTaskFactory(t, resp, append(v.args, "--json")...)
			if code := renderError(r.Factory, r.Err); code != tc.want {
				t.Fatalf("json exit %d", code)
			}
			env := envelope3197(t, r.Factory.IOStreams.ErrOut.(*strings.Builder).String()+r.Out.String())
			if env["serverCode"] != tc.code || !strings.Contains(env["message"].(string), "server words for it.") {
				t.Fatalf("envelope %v", env)
			}
			got, _ := env["extensions"].(map[string]any)
			if !reflect.DeepEqual(got, tc.extensions) {
				t.Fatalf("extensions %v, want %v", got, tc.extensions)
			}
		})
	}
}

// A masked server fault keeps its own message: no code is appended as though it
// were a refusal.
func TestGrantSelection3197InternalFaultUnchanged(t *testing.T) {
	v := verbs3197()[0]
	_, _, err := runTeamTask(t, map[string]string{v.op: refusal3197("INTERNAL_SERVER_ERROR", "Internal server error", nil)}, v.args...)
	if exitCodeFor(err) != exitcode.Error || err == nil || strings.Contains(err.Error(), "(INTERNAL_SERVER_ERROR") {
		t.Fatalf("exit %d err %v", exitCodeFor(err), err)
	}
}

// envelope3197 reads the --json error envelope's "error" object.
func envelope3197(t *testing.T, s string) map[string]any {
	t.Helper()
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no envelope in %q", s)
	}
	var doc struct{ Error map[string]any }
	if err := json.NewDecoder(strings.NewReader(s[start:])).Decode(&doc); err != nil || doc.Error == nil {
		t.Fatalf("envelope %q: %v", s, err)
	}
	return doc.Error
}

// Acceptance 3: a ref the caller did not supply is never shown, in either mode;
// an extension the CLI does not name (withdraw's id) is not copied either.
func TestGrantSelection3197ForeignRefNeverShown(t *testing.T) {
	v := verbs3197()[0]
	resp := map[string]string{v.op: refusal3197("GRANT_NOT_FOUND", "Grant not found", map[string]any{"grantRef": "foreign-grant", "other": "foreign-other"})}
	r := runTeamTaskFactory(t, resp, append(v.args, "--json")...)
	if r.Err == nil {
		t.Fatal("the refusal must fail the command")
	}
	renderError(r.Factory, r.Err)
	all := r.Factory.IOStreams.ErrOut.(*strings.Builder).String() + r.Out.String() + errString1868(r.Err)
	if strings.Contains(all, "foreign-") || strings.Contains(all, `"extensions"`) {
		t.Fatalf("a ref the caller did not supply was shown: %s", all)
	}
}

// Server-sent extension text cannot act on a terminal.
func TestGrantSelection3197ExtensionTextIsNeutralised(t *testing.T) {
	v := verbs3197()[0]
	resp := map[string]string{v.op: refusal3197("GRANT_TERMS_UNREPRESENTABLE", "no", map[string]any{"reason": "A\x1b[2J\u202eB"})}
	_, _, err := runTeamTask(t, resp, v.args...)
	if err == nil || !strings.Contains(err.Error(), "reason: ") || strings.ContainsAny(err.Error(), "\x1b\u202e") {
		t.Fatalf("err %q", err)
	}
}

// Acceptance 4: unknown and unreadable are the server's one null, one answer,
// and the id given is not shown.
func TestGrantSelection3197NotFoundIsOneAnswer(t *testing.T) {
	v := verbs3197()[1]
	args := []string{"team", "grant", "signing-request", "get-selection", "secretish-id-123"}
	_, out, err := runTeamTask(t, map[string]string{v.op: `{"data":{"grantSigningSelection":null}}`}, args...)
	if exitCodeFor(err) != exitcode.NotFound || out != "" || err.Error() != "signing selection not found (or not visible to this caller); the id given is not shown" {
		t.Fatalf("exit %d out %q err %v", exitCodeFor(err), out, err)
	}
}

func TestGrantSelection3197NullMutationResult(t *testing.T) {
	for _, i := range []int{0, 2} {
		v := verbs3197()[i]
		_, _, err := runTeamTask(t, map[string]string{v.op: `{"data":{"` + v.root + `":null}}`}, v.args...)
		if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "no signing selection") {
			t.Errorf("%s: exit %d err %v", v.name, exitCodeFor(err), err)
		}
	}
}

func TestGrantSelection3197UnsafeURLRefusesTextOnly(t *testing.T) {
	bad := "https://x/\u202egrant"
	for _, v := range verbs3197() {
		_, out, err := runTeamTask(t, map[string]string{v.op: v.ok(bad)}, v.args...)
		if exitCodeFor(err) != exitcode.Error || out != "" {
			t.Errorf("%s: exit %d out %q err %v", v.name, exitCodeFor(err), out, err)
		}
		_, out, err = runTeamTask(t, map[string]string{v.op: v.ok(bad)}, append(v.args, "--json")...)
		var d struct{ CeremonyURL string }
		if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.CeremonyURL != bad {
			t.Errorf("%s json: %v %q", v.name, err, out)
		}
	}
}

// A server with grant signing but no selection says so, exit 2, one call.
func TestGrantSelection3197OlderServer(t *testing.T) {
	for _, v := range verbs3197() {
		calls, _, err := runTeamTask(t, map[string]string{v.op: missingFieldJSON(v.root)}, v.args...)
		if exitCodeFor(err) != exitcode.Usage || err == nil || err.Error() != "this server does not offer signing a selection of grants" ||
			serverCode1669(err) != "GRAPHQL_VALIDATION_FAILED" || len(*calls) != 1 {
			t.Errorf("%s: exit %d calls %d err %v", v.name, exitCodeFor(err), len(*calls), err)
		}
	}
}

func TestGrantSelection3197UnboundAndBindingGuards(t *testing.T) {
	for _, v := range verbs3197() {
		t.Run(v.name, func(t *testing.T) {
			calls, stderr, err := unboundRun1868(t, map[string]string{v.op: v.ok(ceremonySel3197)}, v.args...)
			if err != nil || len(*calls) != 1 || (*calls)[0].Session != "" || !strings.Contains(stderr, "sent without a worker session") {
				t.Fatalf("err %v calls %+v stderr %q", err, *calls, stderr)
			}
			writeTeamBinding(t)
			setTeamBindingServer(t, "http://elsewhere.invalid")
			srv, other := attnServer(t, map[string]string{v.op: v.ok(ceremonySel3197)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL}, v.args...))
			if code := exitCodeFor(root.Execute()); code != exitcode.Usage || len(*other) != 0 {
				t.Fatalf("exit %d calls %v", code, *other)
			}
			// cli#3234: --app naming another App is refused too; only the App
			// identity read goes out, never the selection operation.
			resp := map[string]string{v.op: v.ok(ceremonySel3197), "TeamAppIdentity": `{"data":{"app":{"id":"capp999999999999999999999","urn":"hrn:app:acme.com:other","name":"Other"}}}`}
			calls, _, err = runTeamTask(t, resp, append([]string{"--app", "some-other-app"}, v.args...)...)
			if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "another App") ||
				findCall1868(calls, v.op) != nil || findCall1868(calls, "TeamAppIdentity") == nil {
				t.Fatalf("--app: exit %d err %v calls %+v", exitCodeFor(err), err, *calls)
			}
		})
	}
}

// Acceptance 7: the help names the cap, the per-grant line and the ceremony page.
func TestGrantSelection3197Help(t *testing.T) {
	for _, v := range verbs3197() {
		argv := v.args[:3]
		if v.name != "request" {
			argv = v.args[:4]
		}
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, argv...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(out.String()), " ")
		want := []string{"ceremony page", "never signs", "each grant activates or fails on its own", "does not offer"}
		if v.name == "request" {
			want = append(want, "at most 25", "only when it is one you passed")
		}
		for _, s := range want {
			if !strings.Contains(text, s) {
				t.Errorf("%v help lacks %q:\n%s", argv, s, out.String())
			}
		}
	}
}
