package team

import (
	"context"
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

func cfg2061(workers map[string]hostpoller.WorkerConfig) *hostpoller.Config {
	return &hostpoller.Config{Workers: workers}
}

func TestPlanHostImportActions(t *testing.T) {
	cfg := cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris":    {Family: "claude", Launch: []string{"/opt/bin/claude", "--secret-flag", "TOKEN"}, Worktrees: []string{"iris-1"}},
		"Jonas":   {Family: "openai", Launch: []string{"codex"}, Worktrees: []string{"jonas-1"}},
		"Kit":     {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"kit-1"}},
		"Lena":    {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"a", "b"}},
		"Mo":      {Family: "claude", Launch: []string{"claude"}},
		"Nora":    {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"../escape"}},
		"Ghost":   {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"g-1"}},
		"w-by-id": {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"byid-1"}},
	})
	roster := []importRoster{
		{ID: "w1", Name: "Iris", URN: "hrn:worker:t:t:iris"}, {ID: "w2", Name: "Jonas"}, {ID: "w3", Name: "Kit"},
		{ID: "w4", Name: "Lena"}, {ID: "w5", Name: "Mo"}, {ID: "w6", Name: "Nora"}, {ID: "w7", Name: "Byid"},
		{ID: "w8", Name: "Unlisted"},
	}
	existing := []importExisting1{{WorkerID: "w1", Workdir: "iris-1", Family: "claude"}, {WorkerID: "w3", Workdir: "other", Family: "claude"}}
	// "w-by-id" is matched by id, not name.
	roster[6].ID = "w-by-id"
	entries, missing := planHostImport(cfg, roster, existing, true)
	got := map[string]importEntryDTO{}
	for _, e := range entries {
		got[e.Ref] = e
	}
	want := map[string]string{"Iris": importExisting, "Jonas": importCreate, "Kit": importDiffers, "Lena": importSkip, "Mo": importSkip, "Nora": importSkip, "Ghost": importSkip, "w-by-id": importCreate}
	for ref, action := range want {
		if got[ref].Action != action {
			t.Errorf("%s: action %q, want %q (%+v)", ref, got[ref].Action, action, got[ref])
		}
	}
	if got["Jonas"].Family != "codex" {
		t.Errorf("provider alias not folded: %q", got["Jonas"].Family)
	}
	if got["Kit"].Existing == nil || *got["Kit"].Existing != "other/claude" || got["Kit"].Reason == nil {
		t.Errorf("differs must say what the server holds: %+v", got["Kit"])
	}
	if len(missing) != 1 || missing[0] != "Unlisted" {
		t.Errorf("missing = %v", missing)
	}
	for _, e := range entries {
		if e.ProfileStatus != "unmapped" || e.Profile != nil {
			t.Errorf("%s: a launch line was mapped before profiles exist: %+v", e.Ref, e)
		}
	}
	if l := got["Iris"].Launch; l.Program != "claude" || l.Args != 2 {
		t.Errorf("launch shape %+v", l)
	}
}

func TestPlanHostImportUsesProfileSeamWhenItMaps(t *testing.T) {
	old := launcherProfileFor
	defer func() { launcherProfileFor = old }()
	launcherProfileFor = func(argv []string) (string, bool) { return "claude-default", argv[0] == "claude" }
	entries, _ := planHostImport(cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris":  {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"i"}},
		"Jonas": {Family: "codex", Launch: []string{"weird-wrapper"}, Worktrees: []string{"j"}},
	}), []importRoster{{ID: "1", Name: "Iris"}, {ID: "2", Name: "Jonas"}}, nil, true)
	if entries[0].Profile == nil || *entries[0].Profile != "claude-default" || entries[0].ProfileStatus != "mapped" {
		t.Errorf("mapped line: %+v", entries[0])
	}
	if entries[1].Profile != nil || entries[1].ProfileStatus != "unmapped" {
		t.Errorf("an unknown line must stay unmapped, never guessed: %+v", entries[1])
	}
}

func TestApplyHostImportRefusesWithoutLeaveAloneAndWritesNothing(t *testing.T) {
	wd := "w"
	entries := []importEntryDTO{{Ref: "Iris", Action: importCreate, Workdir: &wd, Family: "claude"}}
	assigned := 0
	err := applyHostImport(context.Background(), entries, hostImportWrites{Assign: func(context.Context, string, string, string) error { assigned++; return nil }})
	if !errors.Is(err, errLeaveAloneUnavailable) || assigned != 0 {
		t.Fatalf("err=%v assigned=%d: an assign without a leave-alone write must not happen", err, assigned)
	}
}

func TestApplyHostImportOrderAndIdempotence(t *testing.T) {
	wd := func(s string) *string { return &s }
	entries := []importEntryDTO{
		{Ref: "A", Action: importCreate, Workdir: wd("a"), Family: "claude"},
		{Ref: "B", Action: importExisting, Workdir: wd("b"), Family: "claude"},
		{Ref: "C", Action: importDiffers, Workdir: wd("c"), Family: "claude"},
		{Ref: "D", Action: importSkip},
	}
	var log []string
	w := hostImportWrites{
		LeaveAlone: func(_ context.Context, ref string) error { log = append(log, "leave:"+ref); return nil },
		Assign: func(_ context.Context, ref, d, f string) error {
			log = append(log, "assign:"+ref+":"+d+":"+f)
			return nil
		},
	}
	if err := applyHostImport(context.Background(), entries, w); err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0] != "leave:A" || log[1] != "assign:A:a:claude" {
		t.Fatalf("only create entries are written, leave-alone first: %v", log)
	}
	// A re-run plans from the server's state: the created entry is now "unchanged".
	entries[0].Action = importExisting
	log = nil
	if err := applyHostImport(context.Background(), entries, w); err != nil || len(log) != 0 {
		t.Fatalf("a second apply must write nothing: %v %v", err, log)
	}
}

func TestApplyHostImportStopsAtFirstFailureBeforeAssigning(t *testing.T) {
	wd := "w"
	entries := []importEntryDTO{{Ref: "A", Action: importCreate, Workdir: &wd, Family: "claude"}}
	assigned := false
	err := applyHostImport(context.Background(), entries, hostImportWrites{
		LeaveAlone: func(context.Context, string) error { return errors.New("boom") },
		Assign:     func(context.Context, string, string, string) error { assigned = true; return nil },
	})
	if err == nil || assigned {
		t.Fatalf("a failed leave-alone must stop before the assignment: err=%v assigned=%v", err, assigned)
	}
}

func TestPlanHostImportWithoutServerHostSetupIsUncheckedNotCreate(t *testing.T) {
	entries, _ := planHostImport(cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris": {Family: "claude", Launch: []string{"claude"}, Worktrees: []string{"i"}},
		"Mo":   {Family: "claude", Launch: []string{"claude"}},
	}), []importRoster{{ID: "1", Name: "Iris"}, {ID: "2", Name: "Mo"}}, nil, false)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Ref] = e.Action
	}
	if got["Iris"] != importUnchecked || got["Mo"] != importSkip {
		t.Fatalf("%v: with no server data a valid entry is unchecked (never create), a bad one is still skip", got)
	}
}

// Jamir P2: a name and its id/URN in one file are one worker.
func TestPlanHostImportDuplicateAliasesOfOneWorker(t *testing.T) {
	roster := []importRoster{{ID: "w1", Name: "Iris", URN: "hrn:worker:t:t:iris"}}
	same, _ := planHostImport(cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris":                {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
		"hrn:worker:t:t:iris": {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
	}), roster, nil, true)
	c := map[string]int{}
	for _, e := range same {
		c[e.Action]++
	}
	if c[importCreate] != 1 || c[importSkip] != 1 {
		t.Fatalf("identical aliases must be written once: %+v", same)
	}
	conflict, _ := planHostImport(cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris":                {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
		"hrn:worker:t:t:iris": {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-2"}},
		"w1":                  {Family: "codex", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
	}), roster, nil, true)
	for _, e := range conflict {
		if e.Action != importConflict || e.Reason == nil {
			t.Errorf("%s: a conflicting alias must read conflict with a reason, got %+v", e.Ref, e)
		}
	}
	if countActions(conflict)[importCreate] != 0 {
		t.Fatalf("nothing in a conflict may be a create: %+v", conflict)
	}
}

func TestAppMismatchWarning(t *testing.T) {
	if w := appMismatchWarning("hrn:app:t:team", "Team (hrn:app:t:team) (from --app)"); w != "" {
		t.Errorf("matching App warned: %s", w)
	}
	if w := appMismatchWarning("", "anything"); w != "" {
		t.Errorf("a file without an app key has nothing to compare: %s", w)
	}
	if w := appMismatchWarning("hrn:app:t:other", "Team (hrn:app:t:team) (from --app)"); w == "" {
		t.Error("a different App must warn")
	}
}

// Jamir P3: the idempotence claim is exercised by replanning from the server's
// state after the apply, not by editing the plan by hand.
func TestApplyThenReplanWritesNothingTheSecondTime(t *testing.T) {
	cfg := cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris": {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
		"Kit":  {Family: "codex", Launch: []string{"c"}, Worktrees: []string{"kit-1"}},
	})
	roster := []importRoster{{ID: "w1", Name: "Iris"}, {ID: "w2", Name: "Kit"}}
	server := []importExisting1{}
	w := hostImportWrites{
		LeaveAlone: func(context.Context, string) error { return nil },
		Assign: func(_ context.Context, ref, wd, fam string) error {
			id := map[string]string{"Iris": "w1", "Kit": "w2"}[ref]
			server = append(server, importExisting1{WorkerID: id, Workdir: wd, Family: fam})
			return nil
		},
	}
	entries, _ := planHostImport(cfg, roster, server, true)
	if err := applyHostImport(context.Background(), entries, w); err != nil || len(server) != 2 {
		t.Fatalf("first apply: err=%v server=%v", err, server)
	}
	again, _ := planHostImport(cfg, roster, server, true)
	if c := countActions(again); c[importCreate] != 0 || c[importExisting] != 2 {
		t.Fatalf("replanned from the server's state: %+v", c)
	}
	before := len(server)
	if err := applyHostImport(context.Background(), again, w); err != nil || len(server) != before {
		t.Fatalf("second apply wrote: err=%v", err)
	}
}

func TestApplyAssignFailureAfterLeaveAloneLeavesLaterEntriesUntouched(t *testing.T) {
	wd := func(s string) *string { return &s }
	entries := []importEntryDTO{
		{Ref: "A", Action: importCreate, Workdir: wd("a"), Family: "claude"},
		{Ref: "B", Action: importCreate, Workdir: wd("b"), Family: "claude"},
	}
	var log []string
	err := applyHostImport(context.Background(), entries, hostImportWrites{
		LeaveAlone: func(_ context.Context, r string) error { log = append(log, "leave:"+r); return nil },
		Assign: func(_ context.Context, r, _, _ string) error {
			log = append(log, "assign:"+r)
			return errors.New("boom")
		},
	})
	if err == nil || len(log) != 2 || log[0] != "leave:A" || log[1] != "assign:A" {
		t.Fatalf("err=%v log=%v: stop at the failed assign; B must be untouched (A stays leave-alone, which is the safe state)", err, log)
	}
}
