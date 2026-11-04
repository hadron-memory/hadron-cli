package orca

import (
	"encoding/json"
	"os"
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
		"exit 4 (no such worker or terminal) means Orca answered",
		"Exits 2 and 3",
		"say nothing about Orca",
	)
	mustNotContain(t, "status", long, "any other exit means Orca answered")
}

// Exits 2 and 3 are refused before Orca is listed, so they say nothing about
// Orca: the help says so, and Orca is never called on those paths.
func TestStatusUsageErrorsNeverReachOrca(t *testing.T) {
	r := newRig(t)
	r.joshua(idleScreen)
	if code, _ := r.run(t, false); code != exitcode.Usage {
		t.Fatalf("no target: exit %d, want 2", code)
	}
	if code, _ := r.run(t, false, "--all", "Joshua"); code != exitcode.Usage {
		t.Fatalf("--all with a name: exit %d, want 2", code)
	}
	if len(r.orca.commands) != 0 {
		t.Fatalf("a usage error must not touch Orca, saw %v", r.orca.commands)
	}
	h := newHostRig(t)
	if code, _ := h.run(t, true, "--all", "--config", "/nonexistent/host.yaml"); code != exitcode.Usage && code != exitcode.AuthRequired {
		t.Fatalf("bad --config file: exit %d, want 2 or 3", code)
	}
	if len(h.bins) != 0 || len(h.orca.commands) != 0 {
		t.Fatalf("a bad --config must be refused before Orca is listed, saw %v %v", h.bins, h.orca.commands)
	}
}

// The agentic-usage page must not contradict the help about what the server
// records (Gabriel's P2-1): one answer for an operator auditing a cron run.
func TestAgenticUsageOrcaRecordingMatchesHelp(t *testing.T) {
	raw, err := os.ReadFile("../agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "## Orca terminal status")
	j := strings.Index(doc, "## Orca worker onboarding")
	if i < 0 || j < i {
		t.Fatal("Orca terminal status section not found")
	}
	sec := strings.Join(strings.Fields(doc[i:j]), " ")
	if strings.Contains(sec, "only server effect") {
		t.Error("the section says the due-wake read is the only server effect, but host-key reads are all recorded")
	}
	mustContain(t, "agentic-usage Orca section", sec,
		"recorded as a host-key use",
		"the due-wake read is not the only record",
		"exit 2 or 3 is refused before Orca is listed",
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
	if r.reads.calls["workers"] != 1 {
		t.Fatalf("an unknown name is looked up among the App's workers (the help says so), saw %v", r.reads.calls)
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
		`" (<reason>)" is left out when reason is null`,
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

// George's P2-2: every agentic-usage mention of the --all array must say it is
// JSON only with --json (text mode is one line per worker).
func TestAgenticUsageAllArrayNeedsJSON(t *testing.T) {
	raw, err := os.ReadFile("../agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	for _, bad := range []string{"--all = ONE JSON array", "or `--all` (one JSON array"} {
		if strings.Contains(doc, bad) {
			t.Errorf("agentic-usage states the --all array without --json: %q", bad)
		}
	}
	mustContain(t, "agentic-usage --all", doc,
		"--all --json = ONE JSON array",
		"`--all` (with `--json` one JSON array",
	)
}
