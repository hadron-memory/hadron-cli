package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1847 (V58, server side hadron-server#3977/#4004): a caller who cannot reach
// an App is answered exactly as for an App that does not exist,
// APP_NOT_FOUND, which exits 4 like `team task list`. A caller who can reach
// the App but may not do the act keeps FORBIDDEN, exit 8. The CLI maps codes
// only, so these pins land green as regression guards.

const appNotFoundMessage1847 = "App not found."

func appRefusal1847(code, message string) string {
	raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": code}}}})
	return string(raw)
}

func TestNonParticipantAppRefusalExits1847(t *testing.T) {
	// Every operation these commands may send answers the one refusal, so the
	// pin does not depend on which of them a command makes first.
	ops := []string{"TeamChatReadHead", "TeamChatReadMetadata", "TeamChatMessages", "TeamChatReadPage", "TeamChatMessagesAttributed", "TeamChatReadPageAttributed", "TeamChatReadHeadAttributed", "WorkersRoster", "Workers", "TeamRoles", "CastWorkerPreview", "CastWorkerPreviewInstructed", "TeamAppIdentity"}
	cmds := map[string][]string{
		"chat read":   {"team", "chat", "read", "--app", "acme.com:eng-team"},
		"worker list": {"team", "worker", "list", "--app", "acme.com:eng-team"},
		"worker cast": {"team", "worker", "cast", "--app", "acme.com:eng-team", "--name", "Iris", "--role", "backend-engineer", "--dry-run"},
	}
	for _, tc := range []struct {
		code, message string
		exit          int
	}{
		{"APP_NOT_FOUND", appNotFoundMessage1847, exitcode.NotFound},
		{"FORBIDDEN", "not authorized to manage workers in this App", exitcode.Forbidden},
	} {
		for name, argv := range cmds {
			for _, j := range []bool{false, true} {
				t.Run(tc.code+"/"+name+map[bool]string{false: "/text", true: "/json"}[j], func(t *testing.T) {
					resp := map[string]string{}
					for _, op := range ops {
						resp[op] = appRefusal1847(tc.code, tc.message)
					}
					args := argv
					if j {
						args = append(append([]string{}, argv...), "--json")
					}
					writeTeamBinding(t)
					srv, _ := attnServer(t, resp)
					setTeamBindingServer(t, srv.URL)
					f, out := testFactory(t)
					stderr := f.IOStreams.ErrOut.(*strings.Builder)
					root := NewRootCmd(f)
					root.SetArgs(append([]string{"--server", srv.URL}, args...))
					err := root.Execute()
					if err == nil {
						t.Fatal("refusal succeeded")
					}
					if got := renderError(f, err); got != tc.exit {
						t.Fatalf("exit %d want %d (%v)", got, tc.exit, err)
					}
					if j {
						var dto struct {
							Error struct {
								Code    int    `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						if e := json.Unmarshal([]byte(out.String()), &dto); e != nil || dto.Error.Code != tc.exit || dto.Error.Message != tc.message {
							t.Errorf("envelope %v: %s", e, out.String())
						}
					} else if !strings.Contains(stderr.String(), tc.message) {
						t.Errorf("stderr %q lacks the server's text", stderr.String())
					}
				})
			}
		}
	}
}
