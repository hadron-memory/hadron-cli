package team

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestRelaunchEventIdentifiesWorkerWithoutFakeOperation2060(t *testing.T) {
	var out bytes.Buffer
	f := &cmdutil.Factory{JSON: true, IOStreams: &output.IOStreams{Out: &out}}
	stamp := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	hostEventPrinter(f)(hostpoller.Event{Time: stamp, CheckedAt: stamp, Kind: "relaunch", Type: "worker.relaunch", Outcome: "applied", WorkerID: "w", SessionID: "still-open", TerminalHandle: "own-terminal", Reason: "TERMINAL_MISSING", Attempt: 1, LocalCause: "private setup failed", NoticeFailed: true, ForeignUIDSkipped: 7})
	var row map[string]any
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"workerId": "w", "sessionId": "still-open", "terminalHandle": "own-terminal", "reason": "TERMINAL_MISSING", "attempt": float64(1), "checkedAt": stamp.Format(time.RFC3339Nano), "operation": "", "localCause": "private setup failed", "noticeFailed": true, "foreignUidSkipped": float64(7)} {
		if row[k] != want {
			t.Fatalf("%s=%v want%v: %s", k, row[k], want, out.String())
		}
	}
}
func TestLegacyPollerEventsKeepTheirJSONShape2060(t *testing.T) {
	var out bytes.Buffer
	f := &cmdutil.Factory{JSON: true, IOStreams: &output.IOStreams{Out: &out}}
	hostEventPrinter(f)(hostpoller.Event{Kind: "registered"})
	var row map[string]any
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if len(row) != 6 {
		t.Fatalf("legacy shape changed: %s", out.String())
	}
}

func TestForeignUIDCountTextReceipt2195(t *testing.T) {
	var out bytes.Buffer
	f := &cmdutil.Factory{IOStreams: &output.IOStreams{Out: &out}}
	hostEventPrinter(f)(hostpoller.Event{Kind: "relaunch", Type: "worker.relaunch", Reason: "RELAUNCH_AGENT_PRESENT", ForeignUIDSkipped: 7})
	if !strings.Contains(out.String(), "foreignUidSkipped=7") {
		t.Fatalf("receipt lost census exclusions: %s", out.String())
	}
}
