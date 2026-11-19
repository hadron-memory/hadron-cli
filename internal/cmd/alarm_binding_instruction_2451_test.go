package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// The token store is resolved after the binding snapshot and before its final
// check. Swapping there deterministically pins the no-read alarm path (#116737).
func TestAlarmInstructionChangedBinding2451(t *testing.T) {
	writeTeamBinding(t)
	r := stallResponses()
	r["AlarmAcknowledgeInstructed"] = alarmStallCases2451()[0].response
	srv, calls := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "")
	changed := false
	f.TokenStoreFn = func() store.Store {
		path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(b), `"s-new"`, `"s-probe"`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		changed = true
		return memStore{strings.TrimPrefix(srv.URL, "http://"): "hdr_user_test"}
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "alarm", "ack", "alarm1", "--instruction", "live-chat:opaque"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := oneCall2422(t, calls, "AlarmAcknowledgeInstructed")
	if !changed || got.Session != "" || got.Vars["instruction"] == nil {
		t.Fatalf("changed=%v call=%+v", changed, got)
	}
	if !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "changed") {
		t.Fatal("binding changed before snapshot or no diagnostic")
	}
}
