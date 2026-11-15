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
	entries, missing := planHostImport(cfg, roster, existing)
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
	}), []importRoster{{ID: "1", Name: "Iris"}, {ID: "2", Name: "Jonas"}}, nil)
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
