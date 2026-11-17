package team

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
)

func TestHostUpCensusUnknownTerminal2193(t *testing.T) {
	known := "claude"
	for _, tc := range []struct {
		name          string
		terms         []teamrouter.Terminal
		proof         hostrelaunch.Observation
		err           error
		want, unknown int
	}{
		{name: "shell beside identified agent", terms: []teamrouter.Terminal{{AgentIdentity: &known}, {}}, proof: hostrelaunch.Observation{Proven: true, PIDs: []int{42}}, want: 1},
		{name: "bare shell", terms: []teamrouter.Terminal{{}}, proof: hostrelaunch.Observation{Proven: true}, want: 0},
		{name: "unknown holds agent", terms: []teamrouter.Terminal{{}}, proof: hostrelaunch.Observation{Proven: true, PIDs: []int{42}}, want: 1, unknown: 1},
		{name: "unavailable", terms: []teamrouter.Terminal{{}}, err: errors.New("unreadable"), want: 1, unknown: 1},
		{name: "unproven", terms: []teamrouter.Terminal{{}}, want: 1, unknown: 1},
		{name: "ambiguous agents", terms: []teamrouter.Terminal{{AgentIdentity: &known}, {}}, proof: hostrelaunch.Observation{Proven: true, PIDs: []int{42, 43}}, want: 2, unknown: 1},
		{name: "disconnected unknown unproven", terms: []teamrouter.Terminal{{Connected: false}}, want: 1, unknown: 1},
		{name: "another worktree", terms: []teamrouter.Terminal{{WorktreePath: "/another/worktree"}}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for i := range tc.terms {
				if tc.terms[i].WorktreePath == "" {
					tc.terms[i].WorktreePath = dir
				}
			}
			raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": tc.terms}})
			o := hostUpObserver{root: dir, registered: map[string]bool{}, runner: func(context.Context, []string) teamrouter.RunResult { return teamrouter.RunResult{Stdout: string(raw)} }, census: func(context.Context, string) (hostrelaunch.Observation, error) { return tc.proof, tc.err }}
			got, err := o.Observe(context.Background(), hostup.Worker{Setup: hostup.Setup{Worktree: "."}})
			if err != nil || got.AgentTerminals != tc.want || got.UnidentifiedTerminals != tc.unknown {
				t.Fatalf("got %+v err %v; want possible=%d unknown=%d", got, err, tc.want, tc.unknown)
			}
		})
	}
}
