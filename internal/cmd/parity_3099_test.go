package cmd

// cli#3099 parity pins:
//
//   - `run ls --sort duration`: AppRunSortField.duration is part of the
//     server's enum (server#5035, closes server#855). The CLI's client-side
//     list must accept it, not refuse it before anything is sent.
//   - ALARM_NOT_ACKNOWLEDGEABLE: a team.dark alarm has no chain and is never
//     acknowledged (server#4531, cor:agt:050:34). The ack refusal must carry a
//     documented exit code, and the agentic contract must say which one.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
)

// TestRunLsSortDurationIsSent_3099 pins that --sort duration is accepted and
// reaches the server as sortBy=duration, so a script can sort runs by how long
// they took.
func TestRunLsSortDurationIsSent_3099(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AppRuns": `{"data":{"appRuns":{"total":1,"items":[` + appRunJSON + `]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--app", "capp100000000000000000000", "--sort", "duration", "--dir", "desc", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--sort duration is a server sort field and must be accepted, got %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AppRuns"], &vars)
	if vars["sortBy"] != "duration" {
		t.Errorf("--sort duration should reach the server as sortBy=duration, got %v", vars["sortBy"])
	}
}

// TestRunLsSortRefusalNamesDuration_3099 pins that the refusal for an unknown
// --sort value names duration among the accepted fields.
func TestRunLsSortRefusalNamesDuration_3099(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--app", "capp100000000000000000000", "--sort", "bogus", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("the --sort refusal must name duration among the accepted fields, got %v", err)
	}
}

// TestAgenticUsageDocumentsAlarmNotAcknowledgeable_3099 pins that the embedded
// agent contract documents ALARM_NOT_ACKNOWLEDGEABLE with its exit code, on the
// `team alarm ack` entry: an agent that tries to ack a team.dark alarm must be
// able to learn from the contract why it cannot. (The MapError test covers the
// actual mapping.)
func TestAgenticUsageDocumentsAlarmNotAcknowledgeable_3099(t *testing.T) {
	var entry string
	for _, line := range strings.Split(agentic.Doc(), "\n") {
		if strings.HasPrefix(line, "hadron team alarm ack ") {
			entry = line
			break
		}
	}
	if entry == "" {
		t.Fatal("agentic-usage has no `hadron team alarm ack` entry")
	}
	for _, want := range []string{"ALARM_NOT_ACKNOWLEDGEABLE, exit 2", "team.dark"} {
		if !strings.Contains(entry, want) {
			t.Errorf("the `team alarm ack` entry must say %q (a team.dark alarm is never acknowledgeable)", want)
		}
	}
}
