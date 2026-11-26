package cmd

// cli#3139 (relays hadron-server#5074): `quota list` and `quota set` reach a
// person's counters (--user) and an App on its own (--app alone), following
// the server's four ref combinations and nothing else. The CLI relays; the
// server decides every access question. On main both commands require --org
// and have no --user, so every --app-alone and --user pin below is red there.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const quotaUserRow3139 = `{"id":"q9","scopeKind":"user","scopeId":"u1","action":"run.execute","window":"day",
	"used":0,"limit":50,"resetAt":"2026-11-26T00:00:00Z"}`

type quotaRun3139 struct {
	out, errOut string
	captured    map[string]json.RawMessage
	err         error
}

// runQuota3139 runs the real command against a capturing fake. activeApp, when
// set, is the configured App context (the thing that must fill nothing here).
func runQuota3139(t *testing.T, responses map[string]string, activeApp string, args ...string) quotaRun3139 {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	if activeApp != "" {
		cfg, err := f.Config()
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Set("app", activeApp); err != nil {
			t.Fatal(err)
		}
	}
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return quotaRun3139{
		out:      out.String(),
		errOut:   f.IOStreams.ErrOut.(*strings.Builder).String(),
		captured: captured,
		err:      err,
	}
}

// listOp3139 is the operation `quota list` sends for a set of flags: the org
// forms keep the original document, the App-alone and person forms use the
// scoped one.
func listOp3139(flags []string) string {
	if slices.Contains(flags, "--org") {
		return "QuotaCounters"
	}
	return "QuotaCountersScoped"
}

func quotaReplies3139() map[string]string {
	return map[string]string{
		"QuotaCounters":       `{"data":{"quotaCounters":[` + quotaUserRow3139 + `]}}`,
		"QuotaCountersScoped": `{"data":{"quotaCounters":[` + quotaUserRow3139 + `]}}`,
		"SetQuotaLimit":       `{"data":{"setQuotaLimit":` + quotaUserRow3139 + `}}`,
	}
}

// refVars3139 returns the ref variables the request carried, whether they sit
// at the top level (list) or under input (set), as raw JSON so an omitted key
// is told from an explicit null.
func refVars3139(t *testing.T, r quotaRun3139, op string) map[string]json.RawMessage {
	t.Helper()
	v := vars1343(t, r.captured, op)
	if in, ok := v["input"]; ok {
		m := map[string]json.RawMessage{}
		if err := json.Unmarshal(in, &m); err != nil {
			t.Fatalf("decode input: %v", err)
		}
		return m
	}
	return v
}

// The four allowed forms: refs go out verbatim, every other ref is omitted
// (not null), and the quota operation is the only request (no pre-lookup of
// --app or --user).
func TestQuotaFourFormsSendRefsVerbatimAndOnlyThose3139(t *testing.T) {
	forms := []struct {
		name string
		flag []string
		want map[string]string // JSON key -> raw JSON value
	}{
		{"org", []string{"--org", "acme.com"}, map[string]string{"orgRef": `"acme.com"`}},
		{"org and app", []string{"--org", "acme.com", "--app", "bot"}, map[string]string{"orgRef": `"acme.com"`, "appRef": `"bot"`}},
		{"app alone", []string{"--app", "hrn:app:acme.com:bot"}, map[string]string{"appRef": `"hrn:app:acme.com:bot"`}},
		{"user alone", []string{"--user", "u-123"}, map[string]string{"userRef": `"u-123"`}},
	}
	for _, c := range forms {
		for _, cmd := range []string{"list", "set"} {
			t.Run(cmd+"/"+c.name, func(t *testing.T) {
				args := append([]string{"quota", cmd}, c.flag...)
				op := listOp3139(c.flag)
				if cmd == "set" {
					args = append(args, "--action", "run.execute", "--limit", "5")
					op = "SetQuotaLimit"
				}
				r := runQuota3139(t, quotaReplies3139(), "", args...)
				if r.err != nil {
					t.Fatalf("execute: %v", r.err)
				}
				if len(r.captured) != 1 {
					t.Errorf("only the quota operation may be sent (no pre-lookup of a ref), got %v", keys1343(r.captured))
				}
				got := refVars3139(t, r, op)
				for _, key := range []string{"orgRef", "appRef", "userRef"} {
					want, named := c.want[key]
					raw, sent := got[key]
					if named != sent || (named && string(raw) != want) {
						t.Errorf("%s: sent=%v value=%s, want named=%v value=%s (all vars %v)", key, sent, raw, named, want, got)
					}
				}
			})
		}
	}
}

// A configured App context fills nothing: with --user or --app it is not sent
// as a ref, and with no ref at all the command still refuses.
func TestQuotaAppContextFillsNothing3139(t *testing.T) {
	r := runQuota3139(t, quotaReplies3139(), "ctx-app", "quota", "list", "--user", "u-123")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if got := refVars3139(t, r, "QuotaCountersScoped"); got["appRef"] != nil || got["orgRef"] != nil {
		t.Errorf("the App context must not fill appRef or orgRef: %v", got)
	}
	r = runQuota3139(t, quotaReplies3139(), "ctx-app", "quota", "set", "--user", "u-123", "--action", "run.execute", "--limit", "5")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if got := refVars3139(t, r, "SetQuotaLimit"); got["appRef"] != nil || got["orgRef"] != nil {
		t.Errorf("the App context must not fill appRef or orgRef on set: %v", got)
	}
	for _, args := range [][]string{
		{"quota", "list"},
		{"quota", "set", "--action", "run.execute", "--limit", "5"},
	} {
		r = runQuota3139(t, quotaReplies3139(), "ctx-app", args...)
		if exitCodeFor(r.err) != exitcode.Usage || len(r.captured) != 0 {
			t.Errorf("%v with only an App context: want exit 2 and nothing sent, got exit %d sent %v", args, exitCodeFor(r.err), keys1343(r.captured))
		}
	}
}

// Anything but the server's four forms is a usage error (exit 2) before any
// request: --user with --org or --app, no ref, and an explicitly empty ref.
func TestQuotaUsageRefusalsSendNothing3139(t *testing.T) {
	bad := map[string][]string{
		"user with org":          {"--user", "u1", "--org", "acme.com"},
		"user with app":          {"--user", "u1", "--app", "bot"},
		"user with org and app":  {"--user", "u1", "--org", "acme.com", "--app", "bot"},
		"no ref":                 {},
		"empty org":              {"--org", ""},
		"blank org":              {"--org", "   "},
		"empty app":              {"--app", ""},
		"empty org with app":     {"--org", "", "--app", "bot"},
		"empty user":             {"--user", ""},
		"blank user":             {"--user", "  "},
		"empty app with an org":  {"--org", "acme.com", "--app", ""},
		"empty user with an org": {"--org", "acme.com", "--user", ""},
	}
	for name, flags := range bad {
		for _, cmd := range []string{"list", "set"} {
			t.Run(cmd+"/"+name, func(t *testing.T) {
				args := append([]string{"quota", cmd}, flags...)
				if cmd == "set" {
					args = append(args, "--action", "run.execute", "--limit", "5")
				}
				r := runQuota3139(t, quotaReplies3139(), "", args...)
				if exitCodeFor(r.err) != exitcode.Usage {
					t.Fatalf("want exit 2, got %d (%v)", exitCodeFor(r.err), r.err)
				}
				if len(r.captured) != 0 {
					t.Errorf("a usage refusal sends nothing, sent %v", keys1343(r.captured))
				}
			})
		}
	}
}

// The server's answers are relayed by error code, not by message text:
// BAD_USER_INPUT 2, NOT_FOUND 4, FORBIDDEN 8.
func TestQuotaServerRefusalsExitByCode3139(t *testing.T) {
	want := map[string]int{"BAD_USER_INPUT": 2, "NOT_FOUND": 4, "FORBIDDEN": 8}
	forms := map[string][]string{"app alone": {"--app", "bot"}, "user alone": {"--user", "u1"}}
	for code, exit := range want {
		for name, flags := range forms {
			for _, cmd := range []string{"list", "set"} {
				t.Run(code+"/"+cmd+"/"+name, func(t *testing.T) {
					reply := `{"errors":[{"message":"whatever the server says","extensions":{"code":"` + code + `"}}],"data":null}`
					args := append([]string{"quota", cmd}, flags...)
					op := listOp3139(flags)
					if cmd == "set" {
						args = append(args, "--action", "run.execute", "--limit", "5")
						op = "SetQuotaLimit"
					}
					r := runQuota3139(t, map[string]string{op: reply}, "", args...)
					if exitCodeFor(r.err) != exit {
						t.Errorf("%s must exit %d, got %d (%v)", code, exit, exitCodeFor(r.err), r.err)
					}
					if len(r.captured) != 1 {
						t.Errorf("one request, then the error: sent %v", keys1343(r.captured))
					}
				})
			}
		}
	}
}

// Denial parity: the CLI adds nothing that tells a denied ref from an unknown
// one. Two different refs answered the same way give the same exit code AND
// the same stderr bytes, so the CLI never echoes the ref back or special-cases
// "App not found". The fake can pin only the relay; the caller classes
// (non-manager, App key, impersonated, run) are QA's, against a real stack.
func TestQuotaDenialParityOnExitCodeAndStderrBytes3139(t *testing.T) {
	notFound := `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}],"data":null}`
	forbidden := `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	for _, c := range []struct {
		name, reply string
		a, b        []string
	}{
		{"app denied vs unknown", notFound, []string{"--app", "hrn:app:acme.com:denied"}, []string{"--app", "hrn:app:acme.com:no-such-app"}},
		{"user existing vs missing", forbidden, []string{"--user", "u-exists"}, []string{"--user", "u-missing"}},
	} {
		for _, cmd := range []string{"list", "set"} {
			t.Run(c.name+"/"+cmd, func(t *testing.T) {
				op := listOp3139(c.a)
				extra := []string{}
				if cmd == "set" {
					op = "SetQuotaLimit"
					extra = []string{"--action", "run.execute", "--limit", "5"}
				}
				ra := runQuota3139(t, map[string]string{op: c.reply}, "", append(append([]string{"quota", cmd}, c.a...), extra...)...)
				rb := runQuota3139(t, map[string]string{op: c.reply}, "", append(append([]string{"quota", cmd}, c.b...), extra...)...)
				if ra.err == nil || rb.err == nil || exitCodeFor(ra.err) != exitCodeFor(rb.err) {
					t.Fatalf("same exit code: %v vs %v", ra.err, rb.err)
				}
				if ra.errOut != rb.errOut || ra.errOut == "" {
					t.Errorf("same stderr bytes for both refs:\n%q\n%q", ra.errOut, rb.errOut)
				}
			})
		}
	}
}

// A user row is reported as scopeKind "user", in --json and in the table.
func TestQuotaUserRowRenders3139(t *testing.T) {
	r := runQuota3139(t, quotaReplies3139(), "", "quota", "list", "--user", "u1", "--json")
	var rows []map[string]any
	if r.err != nil || json.Unmarshal([]byte(r.out), &rows) != nil || len(rows) != 1 || rows[0]["scopeKind"] != "user" || rows[0]["scopeId"] != "u1" {
		t.Fatalf("--json row: err=%v out=%s", r.err, r.out)
	}
	r = runQuota3139(t, quotaReplies3139(), "", "quota", "list", "--user", "u1")
	if r.err != nil || !strings.Contains(r.out, "user:u1") {
		t.Errorf("table names the user scope: err=%v out=%q", r.err, r.out)
	}
	r = runQuota3139(t, quotaReplies3139(), "", "quota", "set", "--user", "u1", "--action", "run.execute", "--limit", "50")
	if r.err != nil || !strings.Contains(r.out, "user:u1") {
		t.Errorf("set prints the user scope: err=%v out=%q", r.err, r.out)
	}
}

// The help states, per form, who may use it, what it shows and the exit codes
// the server gives; it makes no enforcement claim for a person's limit.
func TestQuotaHelpPerForm3139(t *testing.T) {
	flat := func(args ...string) string { return strings.Join(strings.Fields(cmdHelpOut(t, args...)), " ") }
	list := flat("quota", "list")
	for _, want := range []string{
		"--org", "--app", "--user",
		"organization's App", "person's App", "platform admin", "owner role",
		"Every refusal of this form is exit 4", "whether or not the person exists",
		"scopeKind (org, app or user)", "an older server refuses them as a schema mismatch",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("quota list --help lacks %q:\n%s", want, list)
		}
	}
	set := flat("quota", "set")
	for _, want := range []string{
		"--user", "platform admin", "Every refusal of this form is exit 4",
		"shared by all their Apps", "once a person's App can run",
		"An impersonated session cannot set a limit at all", "an older server's own refusal is passed through",
	} {
		if !strings.Contains(set, want) {
			t.Errorf("quota set --help lacks %q:\n%s", want, set)
		}
	}
	if strings.Contains(set, "schema mismatch") {
		t.Errorf("quota set --help must not promise a schema-mismatch refusal nobody has observed for set:\n%s", set)
	}
	grp := flat("quota")
	if strings.Contains(grp, "Both need ADMIN or OWNER in the org") {
		t.Errorf("the group page still says every form needs an org role:\n%s", grp)
	}
}

// The org forms must keep working against a server that predates the person
// and App-alone forms: GraphQL rejects an unknown argument in the document even
// when its variable is omitted, so the org document must name no userRef and
// the scoped one must be the only document that does.
func TestQuotaOrgDocumentNamesNoUserRef3139(t *testing.T) {
	if strings.Contains(gen.QuotaCounters_Operation, "userRef") {
		t.Errorf("the org document must not name userRef (an older server rejects it even unset):\n%s", gen.QuotaCounters_Operation)
	}
	if !strings.Contains(gen.QuotaCountersScoped_Operation, "userRef") || strings.Contains(gen.QuotaCountersScoped_Operation, "orgRef") {
		t.Errorf("the scoped document carries userRef and no orgRef:\n%s", gen.QuotaCountersScoped_Operation)
	}
	// SetQuotaLimit carries the refs inside its input value, not as document
	// arguments, so it needs no second document.
	if strings.Contains(gen.SetQuotaLimit_Operation, "userRef") {
		t.Errorf("SetQuotaLimit names userRef as a document argument:\n%s", gen.SetQuotaLimit_Operation)
	}
}

// A ref is sent exactly as typed, padding included: the CLI does not trim,
// resolve or normalise it, so the server answers for what the person wrote
// (and a blank one is refused locally, above).
func TestQuotaRefsAreSentAsTypedPaddingIncluded3139(t *testing.T) {
	for _, c := range []struct {
		name string
		flag []string
		key  string
		want string
	}{
		{"app alone", []string{"--app", " hrn:app:acme.com:bot "}, "appRef", `" hrn:app:acme.com:bot "`},
		{"user alone", []string{"--user", " u-1 "}, "userRef", `" u-1 "`},
	} {
		for _, cmd := range []string{"list", "set"} {
			t.Run(cmd+"/"+c.name, func(t *testing.T) {
				args := append([]string{"quota", cmd}, c.flag...)
				op := listOp3139(c.flag)
				if cmd == "set" {
					args = append(args, "--action", "run.execute", "--limit", "5")
					op = "SetQuotaLimit"
				}
				r := runQuota3139(t, quotaReplies3139(), "", args...)
				if r.err != nil {
					t.Fatalf("execute: %v", r.err)
				}
				if got := refVars3139(t, r, op)[c.key]; string(got) != c.want {
					t.Errorf("%s = %s, want %s (sent as typed)", c.key, got, c.want)
				}
			})
		}
	}
}

// What an older server does with the new forms, pinned as the help states it. `quota list` sends the
// scoped document, which an older server rejects as a validation failure: the CLI's schema-skew
// message, exit 2. `quota set` has no second document, so the server's own refusal reaches the user
// with its code and nothing more is promised. The org forms send the original document either way
// (TestQuotaOrgDocumentNamesNoUserRef3139).
func TestQuotaOlderServerRefusalsAreWhatTheHelpSays3139(t *testing.T) {
	validation := `{"errors":[{"message":"Unknown argument \"userRef\" on field \"Query.quotaCounters\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
	for _, flags := range [][]string{{"--app", "bot"}, {"--user", "u1"}} {
		r := runQuota3139(t, map[string]string{"QuotaCountersScoped": validation}, "", append([]string{"quota", "list"}, flags...)...)
		if exitCodeFor(r.err) != exitcode.Usage || !strings.Contains(r.errOut, "out of sync") {
			t.Errorf("list %v against an older server: want exit 2 and the schema-skew line, got exit %d %q", flags, exitCodeFor(r.err), r.errOut)
		}
		if len(r.captured) != 1 {
			t.Errorf("one request, never retried: %v", keys1343(r.captured))
		}
	}
	bad := `{"errors":[{"message":"Variable \"$input\" got invalid value: field \"userRef\" is not defined","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	for _, flags := range [][]string{{"--app", "bot"}, {"--user", "u1"}} {
		r := runQuota3139(t, map[string]string{"SetQuotaLimit": bad}, "", append([]string{"quota", "set"}, append(flags, "--action", "run.execute", "--limit", "5")...)...)
		if r.err == nil || !strings.Contains(r.errOut, "is not defined") {
			t.Errorf("set %v: the older server's own refusal reaches the user: err=%v %q", flags, r.err, r.errOut)
		}
		if len(r.captured) != 1 {
			t.Errorf("one request, never retried: %v", keys1343(r.captured))
		}
	}
}

// A blank ref's refusal does not suggest leaving that flag out when it is the only ref given.
func TestQuotaBlankRefMessageDoesNotSuggestLeavingTheOnlyRefOut3139(t *testing.T) {
	r := runQuota3139(t, quotaReplies3139(), "", "quota", "list", "--app", "")
	if exitCodeFor(r.err) != exitcode.Usage || strings.Contains(r.errOut, "leave --app out") || !strings.Contains(r.errOut, "--app is empty") {
		t.Errorf("want the empty-ref refusal without a leave-it-out suggestion, got exit %d %q", exitCodeFor(r.err), r.errOut)
	}
}

// cli#3189 item 7: `quota set --help` says an impersonated session is refused first, exit 1. The server's
// refusal today (hadron-server impersonationReadOnlyPlugin, #3374/ruling 0205) is HTTP 403 with
// IMPERSONATION_READ_ONLY and the message below, before any resolver runs, for every form. The reply
// is that real shape, so the help's exit code is checked against what the CLI does with it.
func TestQuotaSetImpersonatedRefusalExitsOneForEveryForm3189(t *testing.T) {
	// The message and the 403 payload below are copied from hadron-server's
	// src/api/graphql/impersonationReadOnlyPlugin.ts (the pre-resolver refusal), so a change there is
	// where this fixture goes stale (cli#3216 item 5).
	const message = "This is a read-only impersonation session; mutations are disabled."
	forms := map[string][]string{
		"org":        {"--org", "acme.com"},
		"org+app":    {"--org", "acme.com", "--app", "bot"},
		"app alone":  {"--app", "bot"},
		"user alone": {"--user", "u1"},
	}
	for name, flags := range forms {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32 // the handler runs on the server goroutine
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"errors":[{"message":"` + message + `","extensions":{"code":"IMPERSONATION_READ_ONLY","blockedFields":["setQuotaLimit"]}}]}`))
			}))
			t.Cleanup(srv.Close)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"quota", "set"}, flags...), "--action", "run.execute", "--limit", "5", "--server", srv.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "read-only impersonation session") {
				t.Errorf("want exit 1 carrying the server's read-only message: %v", err)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("one request, then the refusal: %d", n)
			}
		})
	}
}
