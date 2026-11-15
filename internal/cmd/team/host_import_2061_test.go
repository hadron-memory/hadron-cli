package team

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

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

// fakeAppClient answers TeamAppIdentity per ref, so identities (not text) are compared.
type fakeAppClient struct{ ids map[string]string }

func (c fakeAppClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	ref, _ := req.Variables.(interface{ GetAppRef() string })
	var r string
	if ref != nil {
		r = ref.GetAppRef()
	}
	id, ok := c.ids[r]
	body := `{"app":null}`
	if ok {
		body = `{"app":{"id":"` + id + `","urn":"` + r + `","name":"n"}}`
	}
	return json.Unmarshal([]byte(body), resp.Data)
}

// Jabari F1: identity, not text. hrn:app:t:team is a prefix of hrn:app:t:team-two.
func TestImportAppWarningComparesIdentitiesNotText(t *testing.T) {
	c := fakeAppClient{ids: map[string]string{"hrn:app:t:team": "capp1", "hrn:app:t:team-two": "capp10", "capp1": "capp1", "capp10": "capp10"}}
	ctx := context.Background()
	if w := importAppWarning(ctx, c, "hrn:app:t:team", "capp1"); w != "" {
		t.Errorf("the same App by two spellings warned: %s", w)
	}
	if w := importAppWarning(ctx, c, "hrn:app:t:team", "capp10"); w == "" {
		t.Error("a different App whose name contains the file's text must warn")
	}
	if w := importAppWarning(ctx, c, "hrn:app:t:ghost", "capp1"); w == "" {
		t.Error("an App the server does not resolve must warn")
	}
	if w := importAppWarning(ctx, c, "", "capp1"); w != "" {
		t.Errorf("a file without an app key has nothing to compare: %s", w)
	}
}

// Jamir P3: the idempotence claim is exercised by replanning from the server's
// state after the apply, not by editing the plan by hand.
func entry2061(ref, id, wd string) importEntryDTO {
	w := wd
	return importEntryDTO{Ref: ref, Action: importCreate, Workdir: &w, Family: "claude", workerID: id}
}

type recWrites2061 struct {
	log        []string
	failLeave  map[string]error
	failAssign map[string]error
}

func (r *recWrites2061) writes() hostImportWrites {
	return hostImportWrites{
		LeaveAlone: func(_ context.Context, id string) error {
			r.log = append(r.log, "leave:"+id)
			return r.failLeave[id]
		},
		Assign: func(_ context.Context, id, wd, fam string) error {
			r.log = append(r.log, "assign:"+id+":"+wd+":"+fam)
			return r.failAssign[id]
		},
		Systemic: func(err error) bool { return err.Error() == "systemic" },
	}
}

func TestApplyWritesLeaveAloneBeforeAssignPerWorkerOnlyForCreates(t *testing.T) {
	d := "d"
	entries := []importEntryDTO{
		entry2061("A", "w1", "a"),
		{Ref: "B", Action: importExisting, Workdir: &d, Family: "claude", workerID: "w2"},
		{Ref: "C", Action: importDiffers, Workdir: &d, Family: "claude", workerID: "w3"},
		{Ref: "D", Action: importConflict, workerID: "w4"},
		entry2061("E", "w5", "e"),
	}
	r := &recWrites2061{}
	if err := applyHostImport(context.Background(), entries, r.writes()); err != nil {
		t.Fatal(err)
	}
	want := []string{"leave:w1", "assign:w1:a:claude", "leave:w5", "assign:w5:e:claude"}
	if len(r.log) != len(want) {
		t.Fatalf("log = %v, want %v", r.log, want)
	}
	for i := range want {
		if r.log[i] != want[i] {
			t.Fatalf("log = %v, want %v", r.log, want)
		}
	}
	if a := entries[0].Applied; a == nil || !a.LeaveAlone || !a.Assigned || a.Error != nil {
		t.Fatalf("applied = %+v", a)
	}
	if entries[1].Applied != nil || entries[2].Applied != nil || entries[3].Applied != nil {
		t.Fatal("only create entries are written")
	}
}

// The half-state of a refusal is always the SAFE one: marked leave-alone, not assigned.
func TestApplyAssignRefusalLeavesAMarkedUnassignedWorkerAndOthersStillRun(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a"), entry2061("B", "w2", "b")}
	r := &recWrites2061{failAssign: map[string]error{"w1": errors.New("HOST_ASSIGNMENT_NOT_PERMITTED")}}
	err := applyHostImport(context.Background(), entries, r.writes())
	if !errors.Is(err, errApplyIncomplete) {
		t.Fatalf("err = %v, want the incomplete error", err)
	}
	a, b := entries[0].Applied, entries[1].Applied
	if !a.LeaveAlone || a.Assigned || a.Error == nil {
		t.Fatalf("A = %+v: marked, not assigned, with the reason", a)
	}
	if !b.LeaveAlone || !b.Assigned || b.Error != nil {
		t.Fatalf("B = %+v: a refusal on A must not stop B", b)
	}
}

func TestApplyLeaveAloneFailureNeverAssigns(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a")}
	r := &recWrites2061{failLeave: map[string]error{"w1": errors.New("nope")}}
	if err := applyHostImport(context.Background(), entries, r.writes()); !errors.Is(err, errApplyIncomplete) {
		t.Fatalf("err = %v", err)
	}
	for _, l := range r.log {
		if strings.HasPrefix(l, "assign:") {
			t.Fatalf("assigned a worker that could not be marked leave-alone: %v", r.log)
		}
	}
	if entries[0].Applied.LeaveAlone || entries[0].Applied.Assigned {
		t.Fatalf("applied = %+v", entries[0].Applied)
	}
}

func TestApplySystemicFailureStopsTheRun(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a"), entry2061("B", "w2", "b"), entry2061("C", "w3", "c")}
	r := &recWrites2061{failLeave: map[string]error{"w1": errors.New("systemic")}}
	_ = applyHostImport(context.Background(), entries, r.writes())
	if len(r.log) != 1 || entries[1].Applied != nil || entries[2].Applied != nil {
		t.Fatalf("a failure that repeats for every entry must stop the run: %v", r.log)
	}
}

func TestApplyThenReplanWritesNothingTheSecondTime(t *testing.T) {
	cfg := cfg2061(map[string]hostpoller.WorkerConfig{
		"Iris": {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"iris-1"}},
		"Kit":  {Family: "codex", Launch: []string{"c"}, Worktrees: []string{"kit-1"}},
	})
	roster := []importRoster{{ID: "w1", Name: "Iris"}, {ID: "w2", Name: "Kit"}}
	server := []importExisting1{}
	w := hostImportWrites{
		LeaveAlone: func(context.Context, string) error { return nil },
		Assign: func(_ context.Context, id, wd, fam string) error {
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
	calls := 0
	w.LeaveAlone = func(context.Context, string) error { calls++; return nil }
	if err := applyHostImport(context.Background(), again, w); err != nil || len(server) != before || calls != 0 {
		t.Fatalf("second apply wrote: err=%v leave-alone calls=%d", err, calls)
	}
}
