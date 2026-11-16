package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// cli#2161 (C12): `team session reset-context` (Holger's name, #109658) is the
// shed plus an immediate relaunch: the checkpoint goes under checkpoints:,
// then the claim, then a relaunch stamp beside the binding for the poller,
// then the command ends its own agent (or says to exit).

func runResetContext(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "reset-context", "--server", url}, args...))
	err := root.Execute()
	return out.String(), err
}

type resetContextDTO struct {
	Status   string  `json:"status"`
	Node     *string `json:"node"`
	Claim    string  `json:"claim"`
	Relaunch string  `json:"relaunch"`
	Exit     string  `json:"exit"`
	Reason   *string `json:"reason"`
}

func TestResetContextWritesTheCheckpointClaimThenTheRelaunchStamp2161(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "") // not launched by Orca: nothing is signalled
	url, rig := shedServer(t, nil)
	before := time.Now().UTC().Add(-time.Second)
	out, err := runResetContext(t, url, "--json")
	if err != nil {
		t.Fatalf("reset-context: %v", err)
	}
	var d resetContextDTO
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.Status != "written" || d.Claim != "written" || d.Node == nil || d.Relaunch != "pending" || d.Exit != "exit-now" {
		t.Fatalf("want written/written/pending/exit-now, got %s (%v)", out, e)
	}
	var in struct {
		Input struct{ Loc string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	if !strings.HasPrefix(in.Input.Loc, "checkpoints:") {
		t.Fatalf("the checkpoint goes under checkpoints:, got %q", in.Input.Loc)
	}
	if last := rig.calls[len(rig.calls)-1]; last != "CreateResumeClaim" {
		t.Fatalf("the claim is still the last server call: %v", rig.calls)
	}
	raw, err := os.ReadFile(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile))
	if err != nil {
		t.Fatalf("no relaunch stamp beside the binding: %v", err)
	}
	var s delivery.RelaunchStamp
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	// The URN the CLI really writes is one the poller's reader accepts
	// (Jibril's delta: the validator must match production checkpoints).
	if !delivery.ValidCheckpointURN(s.Checkpoint) {
		t.Fatalf("the written checkpoint %q fails the stamp validator", s.Checkpoint)
	}
	if s.Version != 1 || s.WorkerID != "wkr1" || s.SessionID != "s-new" || s.Checkpoint != *d.Node || s.RequestedAt.Before(before) || s.RequestedAt.After(time.Now().UTC()) {
		t.Fatalf("stamp %+v", s)
	}
	info, _ := os.Stat(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("stamp mode %v", info.Mode().Perm())
	}
}

// A failed checkpoint asks for no relaunch: a fresh agent would have nothing
// to recover from, so no stamp is written and nothing is ended.
func TestResetContextWithoutACheckpointAsksForNoRelaunch2161(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, map[string]string{"CreateNode": `{"errors":[{"message":"boom"}]}`})
	out, err := runResetContext(t, url, "--json")
	if err != nil {
		t.Fatalf("reset-context: %v", err)
	}
	var d resetContextDTO
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.Status != "failed" || d.Relaunch != "none" || d.Exit != "none" {
		t.Fatalf("want failed/none/none, got %s (%v)", out, e)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile)); !os.IsNotExist(err) {
		t.Fatalf("a failed checkpoint left a stamp: %v", err)
	}
}

// `shed` keeps its behaviour until C4: no stamp, no exit.
func TestShedStillWritesNoRelaunchStamp2161(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	if _, _, err := runShed(t, url, "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile)); !os.IsNotExist(err) {
		t.Fatalf("shed wrote a relaunch stamp: %v", err)
	}
}

// Jibril's P3: a failed resume claim still asks for the relaunch. Recovery
// does not need the claim (the relaunch keeps the binding, which session
// recover reads), and the stamp carries the checkpoint the new agent's first
// prompt names, so nothing is lost.
func TestResetContextWithAFailedClaimStillRelaunchesWithTheCheckpoint2161(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, map[string]string{"CreateResumeClaim": `{"errors":[{"message":"boom"}]}`})
	out, err := runResetContext(t, url, "--json")
	if err != nil {
		t.Fatalf("reset-context: %v", err)
	}
	var d resetContextDTO
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.Status != "written" || d.Claim != "failed" || d.Relaunch != "pending" || d.Node == nil {
		t.Fatalf("want written/failed/pending, got %s (%v)", out, e)
	}
	raw, err := os.ReadFile(filepath.Join(os.Getenv(team.GitDirEnv), delivery.RelaunchStampFile))
	if err != nil {
		t.Fatal(err)
	}
	var s delivery.RelaunchStamp
	if err := json.Unmarshal(raw, &s); err != nil || s.Checkpoint != *d.Node || !delivery.ValidCheckpointURN(s.Checkpoint) {
		t.Fatalf("stamp %s (%v)", raw, err)
	}
}
