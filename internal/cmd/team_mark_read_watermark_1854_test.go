package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#1854 (Petra's decision (a), 2026-11-20): `team chat mark-read --through H`
// also advances the binding's local ChatSeenSeq watermark to H when H is at or
// above it, never backwards, so the session-log hint cannot lag an explicit
// mark. Only the App's team chat is the watermark's subject (state.go), so an
// explicit --channel, and a failed server mark, leave it alone.

func bindingWithWatermark1854(t *testing.T, seen *int) string {
	t.Helper()
	writeTeamBinding(t)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	if seen != nil {
		raw := strings.Replace(bindingFixture, `"startedAt"`, `"chatSeenSeq":`+itoa1854(*seen)+`,"startedAt"`, 1)
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func itoa1854(n int) string { b, _ := json.Marshal(n); return string(b) }

func watermark1854(t *testing.T, path string) *int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		ChatSeenSeq *int `json:"chatSeenSeq"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	return b.ChatSeenSeq
}

func TestTeamChatMarkReadAdvancesTheLocalWatermark1854(t *testing.T) {
	ptr := func(n int) *int { return &n }
	for _, tc := range []struct {
		name    string
		local   *int
		through string
		args    []string
		server  string // the MarkOwnTeamChatRead response
		want    *int
	}{
		{"local below H advances to H", ptr(50), "1878", nil, markReadJSON, ptr(1878)},
		{"never-read binding records H", nil, "1878", nil, markReadJSON, ptr(1878)},
		{"through 0 never turns never-read into read-an-empty-chat", nil, "0", nil, markReadJSON, nil},
		{"local equal to H is unchanged", ptr(1878), "1878", nil, markReadJSON, ptr(1878)},
		{"local above H is unchanged", ptr(2000), "1878", nil, markReadJSON, ptr(2000)},
		{"a failed server mark leaves it untouched", ptr(50), "1878", nil, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`, ptr(50)},
		{"an explicit --channel is not the team-chat watermark", ptr(50), "1878", []string{"--channel", "chats:dm-ada"}, markReadJSON, ptr(50)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := bindingWithWatermark1854(t, tc.local)
			srv, _ := attnServer(t, map[string]string{
				"TeamDefaultChannel":  `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
				"MarkOwnTeamChatRead": tc.server,
			})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"team", "chat", "mark-read", "--through", tc.through, "--json", "--server", srv.URL}, tc.args...)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Logf("execute: %v", err)
			}
			got := watermark1854(t, path)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("local watermark = %v, want %v", deref1854(got), deref1854(tc.want))
			}
		})
	}
}

func deref1854(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
