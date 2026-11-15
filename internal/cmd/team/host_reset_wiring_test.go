package team

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

type resetReader struct {
	rows    []orcaterm.Terminal
	screen  *orcaterm.Screen
	lists   int
	after   func(*resetReader)
	err     error
	screens int
}

func (r *resetReader) List(context.Context) ([]orcaterm.Terminal, error) {
	r.lists++
	if r.lists == 2 && r.after != nil {
		r.after(r)
	}
	return r.rows, r.err
}
func (r *resetReader) ReadScreen(context.Context, string) *orcaterm.Screen {
	r.screens++
	return r.screen
}

func TestResetIdleProof2057(t *testing.T) {
	for _, name := range []string{"idle", "claude idle", "unknown target", "unknown worker", "unknown pid", "unknown start", "unknown family", "missing directory", "wrong binding", "wrong worker binding", "unusable binding", "changed binding", "changed process", "unreadable list", "no terminal", "wrong directory", "empty handle", "unknown terminal", "duplicate", "wrong family", "disconnected", "read only", "changed terminal", "changed terminal family", "unreadable screen", "draft", "busy", "menu", "unknown screen"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			family := "codex"
			target := hostpoller.ResetIdleTarget{WorkerRef: "worker:jake", WorkerID: "w1", SessionID: "s1", Worktree: dir, Family: family, Process: hostpoller.ProcessIdentity{PID: 42, StartTime: "100"}}
			b := delivery.Binding{WorkerID: "w1", SessionID: "s1"}
			r := &resetReader{rows: []orcaterm.Terminal{{Handle: "term1", WorktreePath: dir, AgentIdentity: &family, Connected: true, Writable: true}}, screen: &orcaterm.Screen{Source: "screen", Tail: []string{"  Worked for 33s • 16:32", "› Ask Codex to do anything", "  GPT-6-Sol high · ~/orca/workspaces/test · Start worker session", "  ? for shortcuts"}}}
			calls := 0
			process := func(got hostpoller.ResetIdleTarget) error {
				calls++
				if got != target {
					t.Fatal("target changed")
				}
				if name == "changed process" && calls == 2 {
					return errors.New("PROCESS_CHANGED")
				}
				return nil
			}
			switch name {
			case "claude idle":
				family = "claude"
				target.Family = "claude"
				r.screen.Tail = []string{"⏺ Done.", "✻ Cogitated for 1m 3s", "────────────────────────────────────────────────────────────", "❯ ", "────────────────────────────────────────────────────────────", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"}
			case "wrong directory":
				r.rows[0].WorktreePath = t.TempDir()
			case "empty handle":
				r.rows[0].Handle = ""
			case "unknown terminal":
				r.rows[0].AgentIdentity = nil
			case "unknown target":
				target.SessionID = ""
			case "unknown worker":
				target.WorkerID = ""
			case "unknown pid":
				target.Process.PID = 0
			case "unknown start":
				target.Process.StartTime = ""
			case "unknown family":
				target.Family = "unknown"
			case "missing directory":
				target.Worktree = dir + "/missing"
			case "wrong worker binding":
				b.WorkerID = "other"
			case "wrong binding":
				b.SessionID = "other"
			case "unusable binding":
				b.Unusable = true
			case "changed binding":
				r.after = func(*resetReader) { b.SessionID = "new" }
			case "unreadable list":
				r.err = errors.New("secret detail")
			case "no terminal":
				r.rows = nil
			case "duplicate":
				r.rows = append(r.rows, r.rows[0])
			case "wrong family":
				other := "claude"
				r.rows[0].AgentIdentity = &other
			case "disconnected":
				r.rows[0].Connected = false
			case "read only":
				r.rows[0].Writable = false
			case "changed terminal family":
				r.after = func(r *resetReader) { other := "claude"; r.rows[0].AgentIdentity = &other }
			case "changed terminal":
				r.after = func(r *resetReader) { r.rows[0].Handle = "replacement" }
			case "unreadable screen":
				r.screen = nil
			case "draft":
				r.screen.Draft = "secret draft"
			case "busy":
				r.screen.Tail[0] = "  • Working (6s • esc to interrupt)"
			case "menu":
				r.screen.Tail = []string{"Would you like to run this command?", "› 1. Yes, proceed"}
			case "unknown screen":
				r.screen.Tail = []string{"secret unknown text"}
			}
			err := resetIdleProof(r, func(string) delivery.Binding { return b }, process)(context.Background(), target)
			if strings.HasPrefix(name, "unknown ") && name != "unknown screen" && name != "unknown terminal" {
				if err == nil || err.Error() != "TARGET_UNKNOWN" || calls != 0 || r.lists != 0 {
					t.Fatalf("invalid target reached reader: err=%v calls=%d lists=%d", err, calls, r.lists)
				}
			}
			if name == "wrong family" || name == "disconnected" || name == "read only" {
				if r.screens != 0 {
					t.Fatal("unsafe terminal was read")
				}
			}
			if name == "idle" || name == "claude idle" {
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 || r.lists != 2 {
					t.Fatal("missing recheck")
				}
			} else {
				want := "NOT_IDLE"
				switch name {
				case "unknown target", "unknown worker", "unknown pid", "unknown start", "unknown family":
					want = "TARGET_UNKNOWN"
				case "missing directory":
					want = "WORKTREE_UNREADABLE"
				case "wrong binding", "wrong worker binding", "unusable binding", "changed binding":
					want = "BINDING_CHANGED"
				case "changed process":
					want = "PROCESS_CHANGED"
				case "unreadable list":
					want = "TERMINAL_UNREADABLE"
				case "no terminal", "wrong directory", "empty handle", "unknown terminal", "duplicate":
					want = "TERMINAL_AMBIGUOUS"
				case "wrong family", "disconnected", "read only", "changed terminal", "changed terminal family":
					want = "TERMINAL_CHANGED"
				}
				if err == nil || err.Error() != want {
					t.Fatalf("got %v, want %s", err, want)
				}
			}
			if err != nil {
				for _, secret := range []string{"secret detail", "secret draft", "secret unknown text"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatal("screen leaked")
					}
				}
			}
		})
	}
}
