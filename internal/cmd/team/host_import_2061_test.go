package team

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
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
	alreadySet map[string]bool
	readErr    map[string]error
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
		LeaveAloneSet: func(_ context.Context, id string) (bool, error) {
			r.log = append(r.log, "read:"+id)
			return r.alreadySet[id], r.readErr[id]
		},
		Refused:  func(err error) bool { return strings.HasPrefix(err.Error(), "REFUSED") },
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
	want := []string{"read:w1", "leave:w1", "assign:w1:a:claude", "read:w5", "leave:w5", "assign:w5:e:claude"}
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
	r := &recWrites2061{failAssign: map[string]error{"w1": errors.New("REFUSED HOST_ASSIGNMENT_NOT_PERMITTED")}}
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
	if len(r.log) != 2 || entries[1].Applied != nil || entries[2].Applied != nil {
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
		LeaveAloneSet: func(context.Context, string) (bool, error) { return false, nil },
		LeaveAlone:    func(context.Context, string) error { return nil },
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

// Rodolfo P3: a mark the import did not make is never overwritten, and a
// rollback must not clear it.
func TestApplyKeepsAnExistingLeaveAloneMark(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a")}
	r := &recWrites2061{alreadySet: map[string]bool{"w1": true}}
	if err := applyHostImport(context.Background(), entries, r.writes()); err != nil {
		t.Fatal(err)
	}
	for _, l := range r.log {
		if strings.HasPrefix(l, "leave:") {
			t.Fatalf("overwrote an existing mark: %v", r.log)
		}
	}
	if a := entries[0].Applied; !a.LeaveAlone || !a.LeaveAloneKept || !a.Assigned {
		t.Fatalf("applied = %+v", a)
	}
}

func TestApplyNeverWritesWhenTheMarkCannotBeRead(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a")}
	r := &recWrites2061{readErr: map[string]error{"w1": errors.New("REFUSED nope")}}
	if err := applyHostImport(context.Background(), entries, r.writes()); !errors.Is(err, errApplyIncomplete) {
		t.Fatalf("err = %v", err)
	}
	for _, l := range r.log {
		if strings.HasPrefix(l, "leave:") || strings.HasPrefix(l, "assign:") {
			t.Fatalf("wrote without knowing the mark: %v", r.log)
		}
	}
	if entries[0].Applied.Unknown {
		t.Fatal("a read wrote nothing: not an unknown outcome")
	}
}

// Joaquin P2: a lost answer can follow a commit, so only a definite refusal is
// "not written"; anything else is an unknown outcome the report says so about.
func TestApplyDistinguishesARefusalFromAnUnknownOutcome(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a"), entry2061("B", "w2", "b")}
	r := &recWrites2061{failAssign: map[string]error{"w1": errors.New("REFUSED nope"), "w2": errors.New("connection reset")}}
	_ = applyHostImport(context.Background(), entries, r.writes())
	if entries[0].Applied.Unknown {
		t.Fatalf("a coded refusal is definite: %+v", entries[0].Applied)
	}
	if !entries[1].Applied.Unknown || entries[1].Applied.Error == nil || !strings.Contains(*entries[1].Applied.Error, "outcome unknown") {
		t.Fatalf("a transport failure may follow a commit: %+v", entries[1].Applied)
	}
}

// opClient answers each operation from a fixed JSON body, so the production
// writer's own confirmation check can be driven with null/empty/mismatched answers.
type opClient2061 struct{ by map[string]string }

func (c opClient2061) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	body, ok := c.by[req.OpName]
	if !ok {
		return errors.New("unexpected op " + req.OpName)
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		var list gqlerror.List
		if err := json.Unmarshal(env.Errors, &list); err != nil {
			return err
		}
		return list
	}
	return json.Unmarshal(env.Data, resp.Data)
}

func TestHostImportWriterRequiresTheServerToConfirmEachWrite(t *testing.T) {
	good := map[string]string{
		"SetWorkerLeaveAlone": `{"data":{"setWorkerLeaveAlone":{"workerId":"w1","leaveAlone":true}}}`,
		"TeamHostAssign":      `{"data":{"assignHostWorker":{"id":"a","hostId":"orca1","workerId":"w1","workdir":"wd","family":"claude","createdAt":"x","updatedAt":"x"}}}`,
		"GetWorkerLeaveAlone": `{"data":{"worker":{"id":"w1","leaveAlone":false}}}`,
	}
	ctx := context.Background()
	w := hostImportWriter(opClient2061{good}, "capp1", "orca1")
	if err := w.LeaveAlone(ctx, "w1"); err != nil {
		t.Fatalf("a confirmed mark: %v", err)
	}
	if err := w.Assign(ctx, "w1", "wd", "claude"); err != nil {
		t.Fatalf("a confirmed assignment: %v", err)
	}
	if set, err := w.LeaveAloneSet(ctx, "w1"); err != nil || set {
		t.Fatalf("read = %v %v", set, err)
	}
	bad := map[string]map[string]string{
		"SetWorkerLeaveAlone": {
			"null":      `{"data":{"setWorkerLeaveAlone":null}}`,
			"not set":   `{"data":{"setWorkerLeaveAlone":{"workerId":"w1","leaveAlone":false}}}`,
			"other one": `{"data":{"setWorkerLeaveAlone":{"workerId":"w9","leaveAlone":true}}}`,
		},
		"TeamHostAssign": {
			"null":         `{"data":{"assignHostWorker":null}}`,
			"other worker": `{"data":{"assignHostWorker":{"id":"a","hostId":"orca1","workerId":"w9","workdir":"wd","family":"claude","createdAt":"x","updatedAt":"x"}}}`,
			"other host":   `{"data":{"assignHostWorker":{"id":"a","hostId":"orca2","workerId":"w1","workdir":"wd","family":"claude","createdAt":"x","updatedAt":"x"}}}`,
			"other dir":    `{"data":{"assignHostWorker":{"id":"a","hostId":"orca1","workerId":"w1","workdir":"zz","family":"claude","createdAt":"x","updatedAt":"x"}}}`,
			"other family": `{"data":{"assignHostWorker":{"id":"a","hostId":"orca1","workerId":"w1","workdir":"wd","family":"codex","createdAt":"x","updatedAt":"x"}}}`,
		},
	}
	for op, cases := range bad {
		for name, body := range cases {
			m := map[string]string{}
			for k, v := range good {
				m[k] = v
			}
			m[op] = body
			ww := hostImportWriter(opClient2061{m}, "capp1", "orca1")
			var err error
			if op == "SetWorkerLeaveAlone" {
				err = ww.LeaveAlone(ctx, "w1")
			} else {
				err = ww.Assign(ctx, "w1", "wd", "claude")
			}
			if err == nil {
				t.Errorf("%s/%s: an unconfirmed answer must not count as written", op, name)
			}
			if err != nil && ww.Refused(err) {
				t.Errorf("%s/%s: an unconfirmed answer is an unknown outcome, not a refusal", op, name)
			}
		}
	}
	for name, body := range map[string]string{
		"null worker":     `{"data":{"worker":null}}`,
		"null leaveAlone": `{"data":{"worker":{"id":"w1","leaveAlone":null}}}`,
		"other worker":    `{"data":{"worker":{"id":"w9","leaveAlone":false}}}`,
		"missing worker":  `{"data":{}}`,
	} {
		m := map[string]string{"GetWorkerLeaveAlone": body}
		if set, err := hostImportWriter(opClient2061{m}, "c", "h").LeaveAloneSet(ctx, "w1"); err == nil {
			t.Errorf("%s: an unknown answer must not read as 'not marked' (read %v)", name, set)
		}
	}
}

// Joaquin P3 / Rodolfo P3: the production classifiers, with real mapped errors.
func TestHostImportWriterClassifiesRealErrors(t *testing.T) {
	w := hostImportWriter(opClient2061{}, "c", "h")
	gql := func(code string) error {
		return api.MapErrorKeepingCode(gqlerror.List{{Message: "m", Extensions: map[string]any{"code": code}}})
	}
	for code, want := range map[string]bool{"FORBIDDEN": true, "UNAUTHENTICATED": true, "HOST_ASSIGNMENT_EXISTS": false, "BAD_HOST_ASSIGNMENT_ARGS": false, "WORKER_NOT_FOUND": false} {
		if got := w.Systemic(gql(code)); got != want {
			t.Errorf("Systemic(%s) = %v, want %v", code, got, want)
		}
	}
	if !w.Systemic(api.MapErrorKeepingCode(&net.OpError{Op: "dial", Err: errors.New("connection refused")})) {
		t.Error("an unreachable server repeats for every entry: systemic")
	}
	if !w.Refused(gql("FORBIDDEN")) || w.Refused(gql("INTERNAL_SERVER_ERROR")) || w.Refused(errors.New("timeout")) {
		t.Error("only a coded, non-internal answer is a definite refusal")
	}
}

// countClient wraps opClient2061 and counts how often each operation is sent.
type countClient2061 struct {
	inner opClient2061
	n     map[string]int
}

func (c countClient2061) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.n[req.OpName]++
	return c.inner.MakeRequest(ctx, req, resp)
}

// Joaquin P3: real (mapped) systemic errors through an apply with several
// create entries: later workers receive no mutation at all.
func TestApplyWithRealSystemicErrorsStopsBeforeLaterWorkers(t *testing.T) {
	for name, body := range map[string]string{
		"forbidden":       `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}]}`,
		"unauthenticated": `{"errors":[{"message":"no","extensions":{"code":"UNAUTHENTICATED"}}]}`,
	} {
		n := map[string]int{}
		client := countClient2061{opClient2061{map[string]string{
			"GetWorkerLeaveAlone": `{"data":{"worker":{"id":"w1","leaveAlone":false}}}`,
			"SetWorkerLeaveAlone": body,
			"TeamHostAssign":      `{"data":{"assignHostWorker":null}}`,
		}}, n}
		entries := []importEntryDTO{entry2061("A", "w1", "a"), entry2061("B", "w1", "b"), entry2061("C", "w1", "c")}
		err := applyHostImport(context.Background(), entries, hostImportWriter(client, "capp1", "orca1"))
		if !errors.Is(err, errApplyIncomplete) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if n["SetWorkerLeaveAlone"] != 1 || n["TeamHostAssign"] != 0 || entries[1].Applied != nil || entries[2].Applied != nil {
			t.Errorf("%s: a systemic refusal must stop the run at the first write: %v", name, n)
		}
	}
}

// A read failure wrote nothing: not an unknown outcome, even when it stops the run.
func TestApplyReadFailureIsNeverUnknownEvenWhenSystemic(t *testing.T) {
	entries := []importEntryDTO{entry2061("A", "w1", "a")}
	r := &recWrites2061{readErr: map[string]error{"w1": errors.New("systemic")}}
	_ = applyHostImport(context.Background(), entries, r.writes())
	a := entries[0].Applied
	if a.Unknown || a.Error == nil || strings.Contains(*a.Error, "outcome unknown") {
		t.Fatalf("applied = %+v: a read cannot have written anything", a)
	}
}

// cli#2116: an explicit id/urn beats another worker's NAME, in either roster order.
func TestMatchRosterExplicitIDOrURNBeatsANameCollision(t *testing.T) {
	owner := importRoster{ID: "A", Name: "Owner", URN: "hrn:worker:t:t:owner"}
	for name, ref := range map[string]string{"id": "A", "urn": "hrn:worker:t:t:owner"} {
		decoy := importRoster{ID: "B", Name: ref, URN: "hrn:worker:t:t:decoy"}
		for order, roster := range map[string][]importRoster{"decoy first": {decoy, owner}, "owner first": {owner, decoy}} {
			got := matchRoster(ref, roster)
			if got == nil || got.ID != "A" {
				t.Errorf("%s/%s: matched %+v, want the owner", name, order, got)
			}
		}
	}
	// A plain name still matches, case-insensitively, when no id/urn does.
	if got := matchRoster("owner", []importRoster{owner}); got == nil || got.ID != "A" {
		t.Errorf("name fallback: %+v", got)
	}
	if got := matchRoster("nobody", []importRoster{owner}); got != nil {
		t.Errorf("no match must be nil: %+v", got)
	}
}

func TestPlanHostImportTargetsTheExplicitWorkerNotTheNameCollision(t *testing.T) {
	roster := []importRoster{{ID: "B", Name: "A", URN: "u-b"}, {ID: "A", Name: "Owner", URN: "u-a"}}
	entries, _ := planHostImport(cfg2061(map[string]hostpoller.WorkerConfig{
		"A": {Family: "claude", Launch: []string{"c"}, Worktrees: []string{"w-1"}},
	}), roster, nil, true)
	if len(entries) != 1 || entries[0].workerID != "A" || entries[0].Action != importCreate {
		t.Fatalf("entries = %+v: the create must target worker A, not the one NAMED A", entries)
	}
}
