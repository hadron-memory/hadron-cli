package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#3134: --host-uses reads AppKey.hostUsesPage, so a partial list never reads
// as the whole one (hrn:node:hadronmemory.com:specs:cor:api:120:04).

// hostUsesRun3134 runs the plain and the instructed operation.
func hostUsesRun3134(t *testing.T, name string, srv *httptest.Server, args ...string) keyRun {
	t.Helper()
	if name == "instructed" {
		teamGitDir(t)
		writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
		args = append(args, "--instruction", "task:"+taskRef1592)
	}
	return runKeyArgs(t, srv, args...)
}

func TestAppKeyHostUsesPage3134ReadsThePagedField(t *testing.T) {
	for name, op := range map[string]string{"plain": "AppKeysScopesHostUses", "instructed": "AppKeysScopesHostUsesInstructed"} {
		t.Run(name, func(t *testing.T) {
			srv, _, reqs := keyServerCapture(t, 200, keysScopes1333)
			r := hostUsesRun3134(t, name, srv, "list", app1223, "--scopes", "--host-uses", "5", "--json")
			if r.err != nil {
				t.Fatal(r.err)
			}
			all := reqs()
			if v := onlyOp(t, all, op); v["usesLimit"] != float64(5) {
				t.Errorf("variables = %v", v)
			}
			q := map[string]string{"plain": gen.AppKeysScopesHostUses_Operation, "instructed": gen.AppKeysScopesHostUsesInstructed_Operation}[name]
			if !strings.Contains(q, "hostUsesPage(limit: $usesLimit)") || strings.Contains(q, "hostUses(") {
				t.Errorf("query does not read only hostUsesPage: %s", q)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(r.out), &rows); err != nil || len(rows) != 2 {
				t.Fatalf("json = %v %q", err, r.out)
			}
			if u, _ := rows[0]["hostUses"].([]any); len(u) != 1 || rows[0]["hostUsesTotal"] != float64(214) || rows[0]["hostUsesHasMore"] != true {
				t.Errorf("row 0 = %v", rows[0])
			}
			if rows[1]["hostUsesTotal"] != float64(0) || rows[1]["hostUsesHasMore"] != false {
				t.Errorf("a key with no uses must state total 0 and hasMore false, got %v", rows[1])
			}
			if r.errOut != "" {
				t.Errorf("no note is due at 5, got %q", r.errOut)
			}
		})
	}
}

func TestAppKeyHostUsesPage3134TotalAndMoreAbsentWithoutTheFlag(t *testing.T) {
	srv, _, _ := keyServerCapture(t, 200, keysScopes1333)
	r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, k := range []string{"hostUsesTotal", "hostUsesHasMore"} {
		if strings.Contains(r.out, k) {
			t.Errorf("%s must be absent without --host-uses: %s", k, r.out)
		}
	}
}

func TestAppKeyHostUsesPage3134Text(t *testing.T) {
	const use = `{"hostId":"orca1","action":"collect","count":3,"operationId":null,"refusedCode":null,"usedAt":"2026-10-03T00:00:00Z"}`
	for name, tc := range map[string]struct{ page, want string }{
		"more remain": {`{"items":[` + use + `],"total":214,"hasMore":true}`, "host uses: 1 of 214 (more remain)\n"},
		"all shown":   {`{"items":[` + use + `],"total":1,"hasMore":false}`, "host uses: 1 of 1\n"},
		"none":        {`{"items":[],"total":0,"hasMore":false}`, "host uses: none\n"},
		// An empty page is not proof of an empty list.
		"empty page of some":     {`{"items":[],"total":3,"hasMore":true}`, "host uses: 0 of 3 (more remain)\n"},
		"empty page, total only": {`{"items":[],"total":3,"hasMore":false}`, "host uses: 0 of 3\n"},
		"empty page, more":       {`{"items":[],"total":0,"hasMore":true}`, "host uses: 0 of 0 (more remain)\n"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := keyServer(t, 200, `{"data":{"appKeysPage":{"items":[{"id":"key_1","keyPreview":"p","createdAt":"2026-10-01T00:00:00Z","scopes":[],"hostUsesPage":`+tc.page+`}],"total":1,"hasMore":false}}}`)
			r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--host-uses", "1")
			if r.err != nil {
				t.Fatal(r.err)
			}
			if !strings.Contains(r.out, tc.want) {
				t.Errorf("out = %q, want %q", r.out, tc.want)
			}
		})
	}
}

// A key without its page is refused, not shown as a key with no uses.
func TestAppKeyHostUsesPage3134NullPage(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"data":{"appKeysPage":{"items":[{"id":"key_1","keyPreview":"p","createdAt":"2026-10-01T00:00:00Z","scopes":[],"hostUsesPage":null}],"total":1,"hasMore":false}}}`)
	r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--host-uses", "1", "--json")
	if r.err == nil || !strings.Contains(r.err.Error(), "invalid App-key row") || r.out != "" {
		t.Fatalf("err = %v out = %q", r.err, r.out)
	}
}

// Above the server's page cap the user's number is still sent as given, and a
// note on stderr says it is served as 200; stdout stays pure JSON.
func TestAppKeyHostUsesPage3134NoteAboveTheCap(t *testing.T) {
	for n, noted := range map[string]bool{"200": false, "201": true, "500": true} {
		for _, name := range []string{"plain", "instructed"} {
			t.Run(n+name, func(t *testing.T) {
				srv, _, reqs := keyServerCapture(t, 200, keysScopes1333)
				r := hostUsesRun3134(t, name, srv, "list", app1223, "--scopes", "--host-uses", n, "--json")
				if r.err != nil {
					t.Fatal(r.err)
				}
				vars, _ := reqs()[0]["variables"].(map[string]any)
				if b, _ := json.Marshal(vars["usesLimit"]); string(b) != n {
					t.Errorf("usesLimit = %s, want %s", b, n)
				}
				if got := strings.Contains(r.errOut, "at most 200 host uses per key") && strings.Contains(r.errOut, "--host-uses "+n); got != noted {
					t.Errorf("note = %v, want %v: %q", got, noted, r.errOut)
				}
				if !json.Valid([]byte(r.out)) {
					t.Errorf("stdout is not JSON: %q", r.out)
				}
			})
		}
	}
}

// A server without the field is named as too old: one request, no fallback to
// the unpaged field, nothing on stdout that could read as a complete list.
func TestAppKeyHostUsesPage3134OldServer(t *testing.T) {
	const rejected = `{"errors":[{"message":"Cannot query field \"hostUsesPage\" on type \"AppKey\". Did you mean \"hostUses\"?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	for _, name := range []string{"plain", "instructed"} {
		t.Run(name, func(t *testing.T) {
			srv, n, _ := keyServerCapture(t, 400, rejected)
			r := hostUsesRun3134(t, name, srv, "list", app1223, "--scopes", "--host-uses", "5")
			if r.err == nil || exitCodeFor(r.err) != 1 {
				t.Fatalf("want exit 1, got %v (%d)", r.err, exitCodeFor(r.err))
			}
			for _, want := range []string{"too old for --host-uses", "2026-11-24", "leave --host-uses out"} {
				if !strings.Contains(r.err.Error(), want) {
					t.Errorf("error %q lacks %q", r.err, want)
				}
			}
			if n.Load() != 1 || r.out != "" {
				t.Errorf("requests=%d out=%q, want one request and no output", n.Load(), r.out)
			}
		})
	}
	// Another rejection, alone or beside ours, is not "too old".
	for name, body := range map[string]string{
		"other field": strings.ReplaceAll(rejected, `\"hostUsesPage\"`, `\"scopes\"`),
		"mixed":       strings.Replace(rejected, `]}`, `,{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := keyServer(t, 400, body)
			r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--host-uses", "5")
			if r.err == nil || strings.Contains(r.err.Error(), "too old for --host-uses") {
				t.Errorf("err = %v", r.err)
			}
		})
	}
}

// The refresh that brings hostUsesPage also adds userRef to SetQuotaLimitInput,
// which quota set never sends: an omitted field, not an explicit null.
func TestQuotaSet3134DoesNotSendUserRef(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, `{"data":{"setQuotaLimit":`+quotaRowJSON+`}}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"quota", "set", "--org", "acme.com", "--action", "run.execute", "--limit", "5", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	vars := onlyOp(t, reqs(), "SetQuotaLimit")
	in, _ := vars["input"].(map[string]any)
	if b, _ := json.Marshal(in); string(b) != `{"action":"run.execute","limit":5,"orgRef":"acme.com"}` {
		t.Errorf("input = %s", b)
	}
}
