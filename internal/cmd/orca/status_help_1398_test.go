package orca

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// cli#1398: the help of `orca`, `orca terminal` and `orca terminal status`
// must let a reader check reach, script the output and know what the command
// writes (the #1396 audit's O1-O3), with facts that match the command and the
// platform specs (gate folds D1-D4).

func statusLong(t *testing.T) string {
	t.Helper()
	return newCmdStatus(&cmdutil.Factory{}, Env{}).Long
}

func mustContain(t *testing.T, where, text string, want ...string) {
	t.Helper()
	text = strings.Join(strings.Fields(text), " ") // help is wrapped; phrases are not
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s help lacks %q", where, w)
		}
	}
}

func mustNotContain(t *testing.T, where, text string, bad ...string) {
	t.Helper()
	text = strings.Join(strings.Fields(text), " ")
	for _, b := range bad {
		if strings.Contains(text, b) {
			t.Errorf("%s help still says %q", where, b)
		}
	}
}

// O1: a reach check that needs no guessing, and what exit 10 covers.
func TestStatusHelpReachCheck(t *testing.T) {
	long := statusLong(t)
	mustContain(t, "status", long,
		"Checking that Orca is reachable",
		"hadron orca terminal status no-such-worker",
		"before it looks anything else up",
		"Exit 10 covers all of these",
		"orca` is not on PATH",
		"Orca app is not running",
		"did not answer in time",
		"exit 4",
	)
}

// The reach-check recipe in the help is true of the command.
func TestStatusReachCheckRecipeIsTrue(t *testing.T) {
	r := newRig(t)
	r.orca.down = true
	if code, _ := r.run(t, false, "no-such-worker"); code != exitcode.OrcaUnreachable {
		t.Fatalf("Orca down: exit %d, want 10", code)
	}
	if len(r.reads.calls) != 0 {
		t.Fatalf("Orca down must be judged before any server read, saw %v", r.reads.calls)
	}
	r = newRig(t)
	r.joshua(idleScreen)
	if code, _ := r.run(t, false, "no-such-worker"); code != exitcode.NotFound {
		t.Fatalf("Orca up, unknown name: exit %d, want 4", code)
	}
	if r.reads.calls["due"] != 0 || r.reads.calls["subs"] != 0 {
		t.Fatalf("the recipe must not read due wakes or tickers, saw %v", r.reads.calls)
	}
}

// O2: the shape is shown, its keys named, and --all's need for --json stated.
func TestStatusHelpOutputShape(t *testing.T) {
	long := statusLong(t)
	mustContain(t, "status", long,
		"terminal-status@1",
		"contract, worker, terminal, status, prompt, reason, lastTurnAt, checkedAt",
		"every key is always present",
		"One worker with --json prints one object",
		"--all with --json prints one array",
		"--all without --json prints one text line per worker",
	)
}

// The examples in the help are real: same keys as the command emits, values on
// the contract's closed lists.
func TestStatusHelpExamplesMatchTheContract(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	r.orca.terms = append(r.orca.terms, term("term_a", "/w/ada", "claude"))
	r.orca.screens["term_a"] = busyScreen

	_, one := r.run(t, true, "Joshua")
	_, all := r.run(t, true, "--all")
	var wantOne map[string]any
	var wantAll []map[string]any
	if err := json.Unmarshal([]byte(one), &wantOne); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(all), &wantAll); err != nil || len(wantAll) == 0 {
		t.Fatalf("--all output: %v\n%s", err, all)
	}
	keys := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}

	var gotOne, gotAll int
	for _, line := range strings.Split(statusLong(t), "\n") {
		line = strings.TrimSpace(line)
		var m map[string]any
		switch {
		case strings.HasPrefix(line, `{"contract"`):
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("help example is not JSON: %v\n%s", err, line)
			}
			gotOne++
			if keys(m) != keys(wantOne) {
				t.Errorf("help example keys %s, command emits %s", keys(m), keys(wantOne))
			}
			checkClosedLists(t, m)
		case strings.HasPrefix(line, `[{"contract"`):
			var arr []map[string]any
			if err := json.Unmarshal([]byte(line), &arr); err != nil {
				t.Fatalf("help --all example is not JSON: %v\n%s", err, line)
			}
			gotAll++
			for _, e := range arr {
				if keys(e) != keys(wantAll[0]) {
					t.Errorf("help --all entry keys %s, command emits %s", keys(e), keys(wantAll[0]))
				}
				checkClosedLists(t, e)
			}
		}
	}
	if gotOne != 1 || gotAll != 1 {
		t.Fatalf("help must show one single-worker example and one --all example, found %d and %d", gotOne, gotAll)
	}
	// Text mode, as the help describes it.
	if _, text := r.run(t, false, "--all"); !strings.Contains(text, "Joshua idle") {
		t.Fatalf("--all without --json is one text line per worker: %q", text)
	}
}

func checkClosedLists(t *testing.T, m map[string]any) {
	t.Helper()
	if m["contract"] != ts.Contract {
		t.Errorf("contract %v", m["contract"])
	}
	if s, _ := m["status"].(string); !ts.Status(s).Valid() {
		t.Errorf("status %v is not on the closed list", m["status"])
	}
	if p, _ := m["prompt"].(string); !ts.Prompt(p).Valid() {
		t.Errorf("prompt %v is not on the closed list", m["prompt"])
	}
	if r, ok := m["reason"].(string); ok && !ts.Reason(r).Valid() {
		t.Errorf("reason %v is not on the closed list", r)
	}
}

// O3 + D1: what the command writes, honestly.
func TestStatusHelpWhatItWrites(t *testing.T) {
	long := statusLong(t)
	mustContain(t, "status", long,
		"It types nothing and acknowledges nothing",
		"the one a deliverer makes",
		"the server may reserve a due wake against the App's daily wake budget",
		"the platform posts its daily notice to the team chat",
		"a read made with an App key is also recorded",
	)
	mustNotContain(t, "status", long,
		"records nothing",
		"changes nothing",
	)
}

// D2 + D3: the stale host-key sentence is gone; both --config reads named;
// the reach of attention:deliver stated.
func TestStatusHelpConfigReads(t *testing.T) {
	long := statusLong(t)
	mustNotContain(t, "status", long, "A host key may not read tickers")
	mustContain(t, "status", long,
		"host:operate",
		"attention:deliver",
		"both reads must pass",
		"fails closed",
		"workers its own host's registration lists",
		"a key bound to a session is refused",
		"recorded as a host-key use",
	)
}

// D4: the terms the page uses are defined, with the platform's meaning.
func TestStatusHelpDefinesItsTerms(t *testing.T) {
	long := statusLong(t)
	mustContain(t, "status", long,
		"Orca is",
		"ticker: a named wake schedule",
		"tick: a moment at which a ticker may wake",
		"wake: all the reasons a worker is due, merged into one",
		"App key: an App's API key",
		"host key: an App key that carries host:operate",
		"dark is this command's own classification",
		"not an alarm the platform raised",
	)
}

// T1/O4: the group pages say what Orca is, that they only read, and point at
// the reach check.
func TestOrcaGroupHelp(t *testing.T) {
	root := NewCmdOrca(&cmdutil.Factory{})
	mustContain(t, "orca", root.Long, "Orca is", "hadron orca terminal status no-such-worker")
	var term string
	for _, c := range root.Commands() {
		if c.Name() == "terminal" {
			term = c.Long
		}
	}
	mustContain(t, "orca terminal", term, "status", "Orca is", "reads only")
}
