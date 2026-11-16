package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2191: an Orca terminal in the worker's worktree whose agentIdentity is
// null or empty counts as PRESENT (Wolfgang's ruling, chat #110419). Unknown is
// never absent; positive absence needs the process side, which is cli#2193.

// terminals2191 writes the worktree and the terminals Orca lists in it, one per
// identity; an identity of nil is a JSON null, "-" omits the field entirely.
func (fx *hostUpFixture1389) terminals2191(name string, identities ...*string) {
	fx.t.Helper()
	dir := fx.dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fx.t.Fatal(err)
	}
	rows := []map[string]any{}
	for i, id := range identities {
		row := map[string]any{"handle": "term" + string(rune('a'+i)), "worktreePath": dir, "connected": true, "writable": true}
		switch {
		case id == nil:
			row["agentIdentity"] = nil
		case *id != "-":
			row["agentIdentity"] = *id
		}
		rows = append(rows, row)
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": rows}})
	if err := os.WriteFile(filepath.Join(dir, ".terminals.json"), raw, 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

func id2191(s string) *string { return &s }

func TestTeamHostUpUnidentifiedTerminalCountsAsPresent2191(t *testing.T) {
	type state struct {
		name, desired, reason, session string
	}
	noSession := state{"no session", "RUNNING", "LONG_LIVED", ""}
	session := state{"open session", "RUNNING", "LONG_LIVED", "s-jonas"}
	stopped := state{"desired stopped", "STOPPED", "NO_OPEN_TASK", ""}
	stoppedSession := state{"desired stopped, open session", "STOPPED", "NO_OPEN_TASK", "s-jonas"}

	type row struct {
		st          state
		identities  []*string
		planned     []string
		skipped     []string
		unknownNote bool // the detail names the unidentified terminal
	}
	var rows []row
	// The ruling's table: null and empty identity x {no session, session, Stopped}.
	for _, unknown := range []*string{nil, id2191(""), id2191("-")} {
		rows = append(rows,
			row{noSession, []*string{unknown}, nil, []string{"Jonas - TERMINAL_WITHOUT_SESSION"}, true},
			row{session, []*string{unknown}, nil, []string{"Jonas - IN_SYNC"}, true},
			row{stopped, []*string{unknown}, nil, []string{"Jonas - STOP_PENDING"}, true},
			row{stoppedSession, []*string{unknown}, nil, []string{"Jonas - STOP_PENDING"}, true},
			// An agent beside a leftover shell: two terminals, never a start,
			// and the detail says one of them is unidentified.
			row{session, []*string{id2191("claude"), unknown}, nil, []string{"Jonas - DUPLICATE_AGENTS"}, true},
		)
	}
	// The other direction, unchanged: a known identity is present, and a
	// worktree with no terminal at all is still missing.
	for _, known := range []string{"claude", "codex"} {
		rows = append(rows,
			row{noSession, []*string{id2191(known)}, nil, []string{"Jonas - TERMINAL_WITHOUT_SESSION"}, false},
			row{session, []*string{id2191(known)}, nil, []string{"Jonas - IN_SYNC"}, false},
			row{stopped, []*string{id2191(known)}, nil, []string{"Jonas - STOP_PENDING"}, false},
			row{session, []*string{id2191(known), id2191(known)}, nil, []string{"Jonas - DUPLICATE_AGENTS"}, false},
		)
	}
	rows = append(rows,
		row{noSession, nil, []string{"Jonas worker.start NO_SESSION"}, nil, false},
		row{session, nil, nil, []string{"Jonas worker.relaunch RELAUNCH_UNPROVEN"}, false},
		row{stopped, nil, nil, []string{"Jonas - IN_SYNC"}, false},
	)

	for _, r := range rows {
		label := r.st.name + " /"
		for _, id := range r.identities {
			switch {
			case id == nil:
				label += " null"
			case *id == "-":
				label += " absent"
			case *id == "":
				label += ` ""`
			default:
				label += " " + *id
			}
		}
		if len(r.identities) == 0 {
			label += " no terminal"
		}
		t.Run(label, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.terminals2191("Jonas", r.identities...)
			fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: r.st.desired, Reason: r.st.reason, Session: r.st.session})
			dto, raw, err := fx.run()
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned, r.planned...)
			wantItems1389(t, "skipped", dto.Skipped, r.skipped...)
			wantItems1389(t, "failed", dto.Failed)
			detail := ""
			if len(dto.Skipped) == 1 && dto.Skipped[0].Detail != nil {
				detail = *dto.Skipped[0].Detail
			}
			if got := strings.Contains(detail, "no agent identity"); got != r.unknownNote {
				t.Fatalf("detail %q names an unidentified terminal = %v, want %v", detail, got, r.unknownNote)
			}
		})
	}
}

// The APPLY leg: a worker between sessions whose only terminal is unidentified
// is never given a second agent, and a stopped one is never reported in sync.
func TestTeamHostUpApplyNeverStartsBesideAnUnidentifiedTerminal2191(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.terminals2191("Jonas", nil)
	fx.terminals2191("Iris", id2191(""))
	fx.writeDesired("APPLY",
		hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "HOLDS_TASK"},
		hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "STOPPED", Reason: "NO_OPEN_TASK"})
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied)
	wantItems1389(t, "skipped", dto.Skipped, "Iris - STOP_PENDING", "Jonas - TERMINAL_WITHOUT_SESSION")
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations beside an unidentified terminal: %v", m)
	}
}
