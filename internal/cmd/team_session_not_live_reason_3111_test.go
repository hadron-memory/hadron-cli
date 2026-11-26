package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3111: the server's SESSION_NOT_LIVE carries a fixed
// extensions.reason; the CLI shows its next step and adds `reason` to --json.
const sessionNotLiveServerText = "The session is not live."

var allReasons = []string{"DORMANT", "ENDED", "OTHER_SESSION_LIVE", "LIVENESS_UNKNOWN", "NOT_OWNER"}

func sessionNotLiveRaw(ext map[string]any) string {
	e := map[string]any{"code": "SESSION_NOT_LIVE"}
	for k, v := range ext {
		e[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": sessionNotLiveServerText, "extensions": e}}})
	return string(raw)
}

type notLiveCase struct {
	name string
	ext  map[string]any
	want string // "" = no reason expected
}

func notLiveCases(reasons []string) []notLiveCase {
	var cs []notLiveCase
	for _, r := range reasons {
		cs = append(cs, notLiveCase{r, map[string]any{"reason": r}, r})
	}
	cs = append(cs,
		notLiveCase{"none", nil, ""},
		notLiveCase{"unknown", map[string]any{"reason": "SOMETHING_NEW_RAW"}, ""},
		notLiveCase{"nonstring", map[string]any{"reason": 7}, ""})
	return cs
}

func envelopeOf(t *testing.T, out string) (msg string, reason any, hasReason bool) {
	t.Helper()
	var dto struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err, out)
	}
	r, ok := dto.Error["reason"]
	m, _ := dto.Error["message"].(string)
	return m, r, ok
}

// post and mark-read pass the server's refusal through api.MapError: all five
// codes can arrive, the server's message reaches the output, and --json carries
// the reason (absent for none/unknown), with no raw reason string echoed.
func TestSessionNotLiveReasonPostAndMarkRead3111(t *testing.T) {
	for _, surface := range []struct {
		name, op string
		args     []string
	}{
		{"post", "CreateTeamChatMessage", []string{"team", "chat", "post", "message"}},
		{"mark-read", "MarkOwnTeamChatRead", []string{"team", "chat", "mark-read", "--through", "2"}},
	} {
		for _, tc := range notLiveCases(allReasons) {
			t.Run(surface.name+"/"+tc.name, func(t *testing.T) {
				writeTeamBinding(t)
				r := chatReadResponses()
				r[surface.op] = sessionNotLiveRaw(tc.ext)
				srv, _ := attnServer(t, r)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append([]string{"--server", srv.URL, "--json"}, surface.args...))
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal succeeded")
				}
				// The exit class is today's and untouched by #3111.
				if code := renderError(f, err); code == exitcode.OK {
					t.Errorf("exit %d", code)
				}
				msg, reason, has := envelopeOf(t, out.String())
				if !strings.Contains(msg, sessionNotLiveServerText) {
					t.Errorf("server message lost: %s", out.String())
				}
				if tc.want == "" && has {
					t.Errorf("reason must be absent: %s", out.String())
				}
				if tc.want != "" && reason != tc.want {
					t.Errorf("reason %v want %s: %s", reason, tc.want, out.String())
				}
				if strings.Contains(out.String(), "SOMETHING_NEW_RAW") {
					t.Errorf("raw reason printed: %s", out.String())
				}
			})
		}
	}
}

func TestSessionNotLiveNextStepsFixed3111(t *testing.T) {
	want := map[string]string{
		"OTHER_SESSION_LIVE": "--force",
		"LIVENESS_UNKNOWN":   "session whoami --check",
		"DORMANT":            "new session",
		"ENDED":              "new session",
		"NOT_OWNER":          "not your session",
	}
	for r, sub := range want {
		if s := api.SessionNotLiveNextStep(r); !strings.Contains(s, sub) {
			t.Errorf("%s: %q", r, s)
		}
	}
	if s := api.SessionNotLiveNextStep("LIVENESS_UNKNOWN"); !strings.Contains(s, "do not force") {
		t.Errorf("must say do not force: %s", s)
	}
}
