package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2694 D1/D5: the three org github verbs' "server returned no
// organization" branches were unreached by tests — the repo's own convention
// (#2492 and the #2538/#2547 comment tests) pins this {}-envelope shape on
// every other destructive verb. Each verb, for an empty data object AND an
// explicit null result, in text and --json mode: exit 1 (Error) naming the
// outcome-unknown state, empty stdout, and exactly ONE mutation sent with no
// follow-up call. D5: the unlink wording no longer advises a blind retry of a
// destructive verb — the state is unknown, so the portal is where to look.
func TestOrgGithub2694NoOrgEnvelopeExitsOneNamingIt(t *testing.T) {
	cases := []struct {
		verb, op, field, want string
		args                  []string
	}{
		{"link", "LinkGithubApp", "linkGithubApp",
			"server returned no organization; nothing was linked",
			[]string{"github", "link", "acme.com", "--installation-id", "998877"}},
		{"unlink", "UnlinkGithubApp", "unlinkGithubApp",
			"server returned no organization; the link state is unknown — check with the portal",
			[]string{"github", "unlink", "acme.com", "--yes"}},
		{"set-app", "UpdateOrgGithubApp", "updateOrgGithubApp",
			"server returned no organization; whether the credentials were stored is unknown",
			[]string{"github", "set-app", "acme.com", "--app-id", "12345", "--private-key-file", "-"}},
	}
	for _, c := range cases {
		for _, body := range []string{`{"data":{}}`, `{"data":{"` + c.field + `":null}}`} {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/empty=%v/json=%v", c.verb, body == `{"data":{}}`, jsonMode)
				t.Run(name, func(t *testing.T) {
					srv, calls, _ := countingGraphQL(t, map[string]string{c.op: body})
					f, out := testFactory(t)
					f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
					root := NewRootCmd(f)
					args := append([]string{"org"}, c.args...)
					if jsonMode {
						args = append(args, "--json")
					}
					root.SetArgs(append(args, "--server", srv.URL))
					err := root.Execute()
					if err == nil {
						t.Fatalf("must fail, got out=%q", out.String())
					}
					if got := exitCodeFor(err); got != exitcode.Error {
						t.Errorf("exit = %d, want %d (%v)", got, exitcode.Error, err)
					}
					if !strings.Contains(err.Error(), c.want) {
						t.Errorf("error = %q, want it to carry %q", err, c.want)
					}
					if c.verb == "unlink" && strings.Contains(err.Error(), "or retry") {
						t.Errorf("unlink outcome-unknown still advises a blind retry: %q", err)
					}
					if out.String() != "" {
						t.Errorf("stdout must be empty, got %q", out.String())
					}
					if calls(c.op) != 1 {
						t.Errorf("mutation sent %d times, want exactly once with no follow-up call", calls(c.op))
					}
				})
			}
		}
	}
}

// cli#2694 D1: nullable-when-unset rendering was unpinned — the 2512 table
// pins only the set case. githubAppId and githubInstallationId null render
// "(unset)" in text and stay present-but-null in --json, on every verb that
// can return them unset (link and unlink share the renderer and the DTO).
func TestOrgGithub2694UnsetFieldsRender(t *testing.T) {
	const unset = `{"id":"org1","urn":"hrn:org:acme.com","githubAppId":null,"githubInstallationId":null,"hasGithubAppPrivateKey":false}`
	for _, tc := range []struct {
		verb, op, field string
		args            []string
	}{
		{"link", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "acme.com", "--installation-id", "998877"}},
		{"unlink", "UnlinkGithubApp", "unlinkGithubApp", []string{"github", "unlink", "acme.com", "--yes"}},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.verb+fmt.Sprintf("/json=%v", jsonMode), func(t *testing.T) {
				srv, _, _ := countingGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + unset + `}}`})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"org"}, tc.args...)
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(append(args, "--server", srv.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if jsonMode {
					var dto map[string]any
					if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
						t.Fatal(err)
					}
					for _, nullField := range []string{"githubAppId", "githubInstallationId"} {
						if v, ok := dto[nullField]; !ok || v != nil {
							t.Errorf("%s = %v (%T), want the key present with JSON null", nullField, v, v)
						}
					}
					if dto["hasGithubAppPrivateKey"] != false || len(dto) != 5 {
						t.Errorf("DTO = %v", dto)
					}
					return
				}
				for _, want := range []string{
					"github app id: (unset)",
					"installation id: (unset)",
					"private key stored: no",
				} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("text output missing %q:\n%s", want, out.String())
					}
				}
			})
		}
	}
}
