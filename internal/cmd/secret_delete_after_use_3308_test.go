package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3308 (companion to hadron-server#5335): `secret update --delete-after-use`,
// `secret get`'s deleteAfterUse, and the hint on a refused auto-delete mark. Fake HTTP only.

// secret3308 is an App secret row; extra is appended inside the object (",\"k\":v").
func secret3308(protected bool, extra string) string {
	p := "false"
	if protected {
		p = "true"
	}
	return `{"id":"s1","ownerType":"app","ownerId":"app1","name":"deploy-token","kind":"generic","metadata":null,` +
		`"currentVersion":2,"protected":` + p + `,"createdAt":"2026-11-27T00:00:00Z","createdBy":"u1","updatedAt":"2026-11-27T00:00:00Z","updatedBy":"u1"` + extra + `}`
}

func updated3308(row string) map[string]string {
	return map[string]string{"UpdateSecretDeleteAfterUse": `{"data":{"updateSecret":` + row + `}}`}
}

// The flag pair on the wire: each spelling sends its value alone, once, on the flag's own document.
func TestSecretUpdateDeleteAfterUseWire3308(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want bool
	}{
		{"--delete-after-use", true},
		{"--delete-after-use=false", false},
		{"--no-delete-after-use", false},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			row := secret3308(false, `,"deleteAfterUse":`+map[bool]string{true: "true", false: "false"}[tc.want])
			srv, calls := attnServer(t, updated3308(row))
			out, err := runSecretArgs3308(t, srv.URL, "update", "s1", tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if got := opsOf2631(calls); len(got) != 1 || got[0] != "UpdateSecretDeleteAfterUse" {
				t.Fatalf("one mutation, got %v", got)
			}
			want := `{"ref":"s1","deleteAfterUse":` + map[bool]string{true: "true", false: "false"}[tc.want] + `}`
			if got := string((*calls)[0].RawVars); got != want {
				t.Errorf("variables:\n got %s\nwant %s", got, want)
			}
			if !strings.Contains(out, "✓ updated secret deploy-token") || !strings.Contains(out, "delete after use: "+map[bool]string{true: "true", false: "false"}[tc.want]) {
				t.Errorf("receipt: %q", out)
			}
		})
	}
}

// --json on the flagged update carries the server's deleteAfterUse beside the secret fields.
func TestSecretUpdateDeleteAfterUseJSON3308(t *testing.T) {
	srv, _ := attnServer(t, updated3308(secret3308(false, `,"deleteAfterUse":true`)))
	out, err := runSecretArgs3308(t, srv.URL, "update", "s1", "--delete-after-use", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatal(e)
	}
	if got["deleteAfterUse"] != true || got["name"] != "deploy-token" || got["id"] != "s1" {
		t.Errorf("json: %s", out)
	}
}

// An unset flag sends no deleteAfterUse argument: the pre-existing documents are used.
func TestSecretUpdateWithoutTheFlagSendsNoDeleteAfterUse3308(t *testing.T) {
	for _, tc := range []struct {
		op   string
		args []string
	}{
		{"UpdateSecret", []string{"--meta", "env=prod"}},
		{"UpdateSecretProtected", []string{"--protected=false"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{tc.op: `{"data":{"updateSecret":` + secret3308(false, "") + `}}`})
			out, err := runSecretArgs3308(t, srv.URL, append([]string{"update", "s1", "--json"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if raw := string((*calls)[0].RawVars); strings.Contains(raw, "deleteAfterUse") {
				t.Errorf("unset flag was sent: %s", raw)
			}
			if strings.Contains(out, "deleteAfterUse") {
				t.Errorf("the unflagged --json shape changed: %s", out)
			}
		})
	}
}

// The option rides with the other changes as given; the server judges the combination.
func TestSecretUpdateDeleteAfterUseWithOtherChanges3308(t *testing.T) {
	srv, calls := attnServer(t, updated3308(secret3308(false, `,"deleteAfterUse":true`)))
	if _, err := runSecretArgs3308(t, srv.URL, "update", "s1", "--delete-after-use", "--meta", "env=prod", "--protected=false"); err != nil {
		t.Fatal(err)
	}
	const want = `{"ref":"s1","metadata":{"env":"prod"},"protected":false,"deleteAfterUse":true}`
	if got := string((*calls)[0].RawVars); got != want {
		t.Errorf("variables:\n got %s\nwant %s", got, want)
	}

	refused := map[string]string{"UpdateSecretDeleteAfterUse": serverRefusal2720("BAD_USER_INPUT", "On a protected secret, change deleteAfterUse on its own.")}
	srv, calls = attnServer(t, refused)
	_, err := runSecretArgs3308(t, srv.URL, "update", "s1", "--delete-after-use", "--meta", "env=prod")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "change deleteAfterUse on its own") || len(*calls) != 1 {
		t.Errorf("the server's refusal passes through: exit=%d calls=%d %v", exitCodeFor(err), len(*calls), err)
	}
}

// On a protected secret the change is staged: the request id and URL byte for byte, one
// mutation, exit 5, and nothing else is called (the ceremony is a person's).
func TestSecretUpdateDeleteAfterUseStaged3308(t *testing.T) {
	const url = "https://portal.example/secret-change/req-3308?a=1&b=%2F"
	for _, flag := range []string{"--delete-after-use", "--no-delete-after-use"} {
		t.Run(flag, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"UpdateSecretDeleteAfterUse": `{"errors":[{"message":"needs passkey","path":["updateSecret"],"extensions":{"code":"SECRET_PROTECTED_NEEDS_PASSKEY","requestId":"req-3308","ceremonyUrl":` + mustJSON2631(url) + `}}]}`})
			out, err := runSecretArgs3308(t, srv.URL, "update", "s1", flag)
			if err == nil || exitCodeFor(err) != exitcode.Conflict {
				t.Fatalf("exit=%d want %d: %v", exitCodeFor(err), exitcode.Conflict, err)
			}
			for _, want := range []string{"staged, not applied: the delete-after-use change of the secret", "request id: req-3308", "ceremony URL: " + url + "\n"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %q", want, err.Error())
				}
			}
			if out != "" || len(*calls) != 1 {
				t.Errorf("one mutation and no success output: calls=%v out=%q", opsOf2631(calls), out)
			}
		})
	}
}

// A returned secret that does not report the asked value is not a success.
func TestSecretUpdateDeleteAfterUseUnconfirmed3308(t *testing.T) {
	for name, row := range map[string]string{
		"other value": secret3308(true, `,"deleteAfterUse":false`),
		"no value":    secret3308(true, ""),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, updated3308(row))
			out, err := runSecretArgs3308(t, srv.URL, "update", "s1", "--delete-after-use")
			if err == nil || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(err.Error(), "not confirmed") || out != "" {
				t.Errorf("exit=%d out=%q err=%v", exitCodeFor(err), out, err)
			}
		})
	}
}

func TestSecretUpdateDeleteAfterUseContradictionSendsNothing3308(t *testing.T) {
	for _, args := range [][]string{
		{"--delete-after-use", "--no-delete-after-use"},
		{"--no-delete-after-use=false"},
		{"--delete-after-use", "false"},
	} {
		srv, calls := attnServer(t, map[string]string{})
		_, err := runSecretArgs3308(t, srv.URL, append([]string{"update", "s1"}, args...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "delete-after-use") || len(*calls) != 0 {
			t.Errorf("%v: exit=%d calls=%d err=%v", args, exitCodeFor(err), len(*calls), err)
		}
	}
}

func TestSecretGetShowsDeleteAfterUse3308(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{"Secret": `{"data":{"secret":` + secret3308(true, `,"deleteAfterUse":true`) + `}}`})
	out, err := runSecretArgs3308(t, srv.URL, "get", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "delete after use: true") {
		t.Errorf("text: %q", out)
	}
	out, err = runSecretArgs3308(t, srv.URL, "get", "s1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if e := json.Unmarshal([]byte(out), &got); e != nil || got["deleteAfterUse"] != true {
		t.Errorf("json: %v %s", e, out)
	}
}

// A server without the field keeps `secret get` working: one retry on the older document,
// deleteAfterUse null (unknown is never printed as false). Any other failure is not retried.
func TestSecretGetOnAServerWithoutTheField3308(t *testing.T) {
	unknown := `{"errors":[{"message":"Cannot query field \"deleteAfterUse\" on type \"Secret\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv, calls := attnServer(t, map[string]string{"Secret": unknown, "SecretWithoutDeleteAfterUse": `{"data":{"secret":` + secret3308(false, "") + `}}`})
	out, err := runSecretArgs3308(t, srv.URL, "get", "s1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"deleteAfterUse": null`) {
		t.Errorf("json: %s", out)
	}
	if got := opsOf2631(calls); len(got) != 2 {
		t.Errorf("calls: %v", got)
	}
	out, err = runSecretArgs3308(t, srv.URL, "get", "s1")
	if err != nil || !strings.Contains(out, "delete after use: -") {
		t.Errorf("text: %v %q", err, out)
	}

	srv, calls = attnServer(t, map[string]string{"Secret": serverRefusal2720("FORBIDDEN", "no")})
	if _, err := runSecretArgs3308(t, srv.URL, "get", "s1"); err == nil || exitCodeFor(err) != exitcode.Forbidden || len(*calls) != 1 {
		t.Errorf("exit=%d calls=%d %v", exitCodeFor(err), len(*calls), err)
	}
}

// SECRET_AUTO_DELETE_NOT_ALLOWED covers two causes the server answers identically, so the CLI
// keeps the permission exit and the server's message and names both causes and the way out.
func TestSecretClientIssueAutoDeleteRefusalNamesBothCauses3308(t *testing.T) {
	const message = "You may not set an auto-delete mark for this secret."
	srv, calls := attnServer(t, map[string]string{"IssueSecretReadClient": serverRefusal2720("SECRET_AUTO_DELETE_NOT_ALLOWED", message)})
	_, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--auto-delete", "--max-reads", "1")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden || len(*calls) != 1 {
		t.Fatalf("exit=%d calls=%d %v", exitCodeFor(err), len(*calls), err)
	}
	for _, want := range []string{message, "neither manage the App nor wrote the secret's current version", "protected and its delete-after-use option is off", "hadron secret update <id|urn> --delete-after-use", "passkey"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %q", want, err.Error())
		}
	}
}

func runSecretArgs3308(t *testing.T, srvURL string, args ...string) (stdout string, err error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"secret"}, args...), "--server", srvURL))
	err = root.Execute()
	return out.String(), err
}
