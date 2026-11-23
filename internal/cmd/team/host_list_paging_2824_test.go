package team

// hadron-cli#2824: `team host list --limit/--offset` over the server's paged
// hostRegistrationsPage (server #2213 S3). The pins, from the issue's
// acceptance as amended by the FIT:
//
//   - No flags pages to exhaustion by 20 through hasMore, printing exactly
//     what the command printed on the unpaged field (cor:api:120:04: a client
//     doesn't ask for more than 20 unless its user did).
//   - Either flag selects ONE call and a single page that says it is one page,
//     with a continuation hint when more rows exist.
//   - An empty page names the total ("no hosts on this page (total N)") — an
//     offset past the end and --limit 0 are real empty pages, a failed read is
//     a non-zero exit that never prints that line.
//   - --limit passes through as given: over 200 is served by the server as
//     200, never refused in the client.
//   --json is the unchanged array in the default drain; the {items,total,
// hasMore,offset,limit,served,nextOffset} envelope exists only in
// single-page mode.
//
// Fold pins (blind F1/F2, local F1): a null row is skipped in the drain, not
// a panic; a server without hostRegistrationsPage is read whole through
// hostRegistrations by default (the flags are refused by name); drift
// mid-drain refuses with no partial table.
//
// hadron-cli#2971 (follow-ups of #2939's review), the `_2971` tests:
//
//   - A carried --server is quoted as one shell word: a URL with a query is
//     pinned as text and read back through a real sh.
//   - A skipped null row is NAMED in the page line (or the empty-page line),
//     so the rows shown and the --offset the continue line advances by no
//     longer disagree unexplained (cor:api:120:04); --json says the same with
//     served and nextOffset, nextOffset present as null when there is none.
//   - The help states what the continue line carries and where it is right.
//   - Pins the review found missing on behavior that already held: the
//     minimal continue line (nothing given but --offset) and a last page
//     that prints no continue line.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type hostListCall struct {
	op   string
	vars map[string]json.RawMessage
}

// hostListPageFake serves the paged query (and the text mode's App label
// read) from a queue per operation, recording every call. An unstubbed
// operation is an error; the unpaged HostRegistrations is stubbed only by
// the old-server fold tests, where the default form legitimately falls back
// to it (#2824).
func hostListPageFake(t *testing.T, bodies map[string][]string) (string, func() []hostListCall) {
	t.Helper()
	t.Setenv("HADRON_TOKEN", "synthetic")
	var mu sync.Mutex
	calls := []hostListCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, hostListCall{body.OperationName, body.Variables})
		q := bodies[body.OperationName]
		resp := `{"errors":[{"message":"unexpected operation"}]}`
		if len(q) > 0 {
			resp, bodies[body.OperationName] = q[0], q[1:]
		} else {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []hostListCall { mu.Lock(); defer mu.Unlock(); return append([]hostListCall{}, calls...) }
}

// hostListRun executes `team host list` against the fake server.
func hostListRun(t *testing.T, server string, args ...string) (string, error) {
	t.Helper()
	out, _, err := hostListRunBoth(t, server, args...)
	return out, err
}

// hostListRunBoth is hostListRun that also returns stderr, for the notes the
// text mode prints there (the old-server fallback disclosure).
func hostListRunBoth(t *testing.T, server string, args ...string) (string, string, error) {
	t.Helper()
	return hostListRunFlags(t, server, "hrn:app:acme.com:ops", true, args...)
}

// hostListRunFlags runs `team host list` with the --app flag value given ("" means the caller did not
// pass --app: the App comes from the configured context instead) and the --server flag optional:
// serverFlag false reaches the fake through HADRON_SERVER instead (cli#2939).
func hostListRunFlags(t *testing.T, server, appFlag string, serverFlag bool, args ...string) (string, string, error) {
	t.Helper()
	if serverFlag {
		return hostListRunGiven(t, server, appFlag, args...)
	}
	t.Setenv("HADRON_SERVER", server)
	return hostListRunGiven(t, "", appFlag, args...)
}

// hostListRunGiven runs `team host list` with --server and --app carrying exactly the values given
// ("" = the flag was not passed). The --server value need not be the fake's own URL as long as the
// fake still answers: cli#2971 quotes a URL that carries a query.
func hostListRunGiven(t *testing.T, serverFlag, appFlag string, args ...string) (string, string, error) {
	t.Helper()
	// The run's config home is this helper's own temp dir, set here rather than trusted to the
	// caller having built a fake first (cli#2971): the helper writes a config file under it below,
	// so it must never be the developer's own configuration directory.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if appFlag == "" {
		// No --app: the App comes from the configured context (a config file in the temp
		// XDG_CONFIG_HOME set above), so the run does not depend on a worktree binding or the
		// developer's own config.
		dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("app = \"hrn:app:acme.com:ops\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := cmdutil.NewFactory()
	f.ServerFlag = serverFlag
	f.AppFlag = appFlag
	io, out, errOut := output.Test()
	f.IOStreams = io
	root := &cobra.Command{Use: "host", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
	root.AddCommand(newCmdTeamHostLs(f))
	root.SetArgs(append([]string{"list"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// pageCalls keeps only the paged-read calls — the text mode also reads the
// App's label (TeamAppIdentity), which is not part of the paging contract.
func pageCalls(cs []hostListCall) []hostListCall {
	out := []hostListCall{}
	for _, c := range cs {
		if c.op == "HostRegistrationsPage" {
			out = append(out, c)
		}
	}
	return out
}

// hostPageBody renders one hostRegistrationsPage answer.
func hostPageBody(items []string, total int, hasMore bool) string {
	return fmt.Sprintf(`{"data":{"hostRegistrationsPage":{"items":[%s],"total":%d,"hasMore":%t}}}`,
		strings.Join(items, ","), total, hasMore)
}

// hostWholeBody renders one whole-set hostRegistrations answer (the field
// every server has).
func hostWholeBody(items []string) string {
	return fmt.Sprintf(`{"data":{"hostRegistrations":[%s]}}`, strings.Join(items, ","))
}

// hostPageMissingField is an old server's rejection of the paged document
// itself, before execution: hadron-server #2213 S3 predates the field.
const hostPageMissingField = `{"errors":[{"message":"Cannot query field \"hostRegistrationsPage\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

const hostListItem = `{"hostId":"%s","label":"%s","pollIntervalSeconds":30,
	"registeredAt":"2026-11-01T00:00:00Z","refreshedAt":"2026-11-21T00:00:00Z","live":true,
	"workers":[{"workerId":"w1","worker":{"id":"w1","urn":"hrn:worker:acme.com:ops:jonas","name":"Jonas"},"family":"glm","worktrees":["wt1"]}]}`

const hostListLabelBody = `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`

func hostListHost(id string) string {
	return fmt.Sprintf(hostListItem, id, id+" label")
}

// Default (no flags): pages by 20 through hasMore until exhausted, and prints
// the complete set exactly as before — no page framing.
func TestHostListDefaultDrainsByTwenty_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {
			hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 3, true),
			hostPageBody([]string{hostListHost("orca3")}, 3, false),
		},
		"TeamAppIdentity": {hostListLabelBody},
	})
	out, err := hostListRun(t, server)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	cs := pageCalls(calls())
	if len(cs) != 2 {
		t.Fatalf("the drain must page HostRegistrationsPage, got %+v", cs)
	}
	if string(cs[0].vars["limit"]) != "20" || string(cs[0].vars["offset"]) != "0" {
		t.Errorf("first page must ask limit 20 offset 0, got %s/%s", cs[0].vars["limit"], cs[0].vars["offset"])
	}
	if string(cs[1].vars["limit"]) != "20" || string(cs[1].vars["offset"]) != "2" {
		t.Errorf("second page must advance by the rows served (offset 2), got %s/%s", cs[1].vars["limit"], cs[1].vars["offset"])
	}
	for _, id := range []string{"orca1", "orca2", "orca3"} {
		if !strings.Contains(out, id) {
			t.Errorf("host %s missing from the drain output:\n%s", id, out)
		}
	}
	for _, banned := range []string{"page:", "continue:"} {
		if strings.Contains(out, banned) {
			t.Errorf("the complete set must not carry single-page framing (%q):\n%s", banned, out)
		}
	}
}

// Default JSON is the unchanged bare array — the envelope exists only in
// single-page mode.
func TestHostListDefaultJSONStaysAnArray_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1")}, 1, false)},
	})
	out, err := hostListRun(t, server, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("default --json must stay a bare array, got %s", out)
	}
	if len(rows) != 1 || rows[0]["hostId"] != "orca1" {
		t.Fatalf("unexpected rows: %s", out)
	}
	// --json never runs the human branch, so the App label read must not fire.
	for _, c := range calls() {
		if c.op == "TeamAppIdentity" {
			t.Errorf("--json must not pay the App label read")
		}
	}
}

// Default drain over an empty App keeps its old answer: "no hosts registered"
// and [] in JSON — never the single-page empty-page line.
func TestHostListDefaultEmptyKeepsRegisteredLine_2824(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody(nil, 0, false)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "no hosts registered") {
		t.Fatalf("the drained empty set keeps its line, got:\n%s", out)
	}
	if strings.Contains(out, "no hosts on this page") {
		t.Fatalf("a complete empty set is not a page:\n%s", out)
	}
}

// Either flag selects exactly ONE call with the stated limit/offset, and the
// text says it is one page of the total, from its offset.
func TestHostListSinglePageOneCallAndFraming_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca3")}, 3, false)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server, "--limit", "2", "--offset", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	cs := pageCalls(calls())
	if len(cs) != 1 {
		t.Fatalf("a single-page read must make exactly one call, got %+v", cs)
	}
	if string(cs[0].vars["limit"]) != "2" || string(cs[0].vars["offset"]) != "2" {
		t.Errorf("the call must carry the stated limit/offset, got %s/%s", cs[0].vars["limit"], cs[0].vars["offset"])
	}
	if !strings.Contains(out, "page: 1 of 3 from offset 2") {
		t.Errorf("single-page framing missing:\n%s", out)
	}
	if strings.Contains(out, "continue:") {
		t.Errorf("a final page must not hint a continuation:\n%s", out)
	}
	if strings.Contains(out, "orca1") {
		t.Errorf("rows outside the page must not print:\n%s", out)
	}
}

// A page with more rows behind it names the --offset that continues the list.
func TestHostListSinglePageContinueHint_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 3, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server, "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(pageCalls(calls())) != 1 {
		t.Fatalf("hasMore must not make the CLI fetch the next page itself, got %+v", calls())
	}
	if !strings.Contains(out, "page: 2 of 3 from offset 0") {
		t.Errorf("framing must count the rows served:\n%s", out)
	}
	if !strings.Contains(out, "continue: hadron team host list --server "+server+" --app hrn:app:acme.com:ops --limit 2 --offset 2") {
		t.Errorf("the continuation hint must advance by rows served and carry the --app and --limit the caller gave (cli#2939):\n%s", out)
	}
}

// --offset alone (an explicit 0) still selects one page; the CLI names its
// own page size (20), never an unlabelled default.
func TestHostListOffsetAloneIsOnePage_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1")}, 5, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server, "--offset", "0")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	cs := pageCalls(calls())
	if len(cs) != 1 {
		t.Fatalf("an explicit --offset selects single-page mode, got %+v", cs)
	}
	if string(cs[0].vars["limit"]) != "20" || string(cs[0].vars["offset"]) != "0" {
		t.Errorf("offset-only must ask limit 20 offset 0, got %s/%s", cs[0].vars["limit"], cs[0].vars["offset"])
	}
	if !strings.Contains(out, "page: 1 of 5 from offset 0") {
		t.Errorf("framing missing:\n%s", out)
	}
}

// Single-page --json is the envelope: items, total, hasMore, offset, limit.
func TestHostListSinglePageJSONEnvelope_2824(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1")}, 3, true)},
	})
	out, err := hostListRun(t, server, "--json", "--limit", "1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var env struct {
		Items   []map[string]any `json:"items"`
		Total   int              `json:"total"`
		HasMore bool             `json:"hasMore"`
		Offset  int              `json:"offset"`
		Limit   int              `json:"limit"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("single-page --json must be the envelope, got %s", out)
	}
	if len(env.Items) != 1 || env.Total != 3 || !env.HasMore || env.Offset != 0 || env.Limit != 1 {
		t.Fatalf("unexpected envelope: %s", out)
	}
}

// --limit 0 asks for the count only: one call, the empty-page line carries
// the total, and the JSON envelope holds an empty items array, not null.
func TestHostListLimitZeroCountsOnly_2824(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		server, calls := hostListPageFake(t, map[string][]string{
			"HostRegistrationsPage": {hostPageBody(nil, 5, false)},
			"TeamAppIdentity":       {hostListLabelBody},
		})
		args := []string{"--limit", "0"}
		if jsonMode {
			args = append(args, "--json")
		}
		out, err := hostListRun(t, server, args...)
		if err != nil {
			t.Fatalf("execute (json=%v): %v", jsonMode, err)
		}
		cs := pageCalls(calls())
		if len(cs) != 1 || string(cs[0].vars["limit"]) != "0" {
			t.Fatalf("--limit 0 must be one call with limit 0, got %+v", cs)
		}
		if !jsonMode {
			if !strings.Contains(out, "no hosts on this page (total 5)") {
				t.Errorf("--limit 0 prints the total only, got:\n%s", out)
			}
			continue
		}
		var env struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("not JSON: %s", out)
		}
		if env.Total != 5 || env.Items == nil || len(env.Items) != 0 {
			t.Errorf("count-only envelope must be items [] total 5, got %s", out)
		}
	}
}

// The two real empty-page causes — an offset past the end and --limit 0 —
// print the total; the line never stands in for a failed read.
func TestHostListEmptyPageNamesTheTotal_2824(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody(nil, 5, false)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server, "--offset", "99")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "no hosts on this page (total 5)") {
		t.Fatalf("an offset past the end must name the total, got:\n%s", out)
	}
	if strings.Contains(out, "no hosts registered") {
		t.Fatalf("one empty page is not the complete set:\n%s", out)
	}
}

// A refused read is a non-zero exit and never prints the empty-page line.
func TestHostListFailedReadIsNeverAnEmptyPage_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {`{"errors":[{"message":"nope","extensions":{"code":"APP_NOT_FOUND"}}]}`},
	})
	out, err := hostListRun(t, server, "--limit", "2")
	if err == nil {
		t.Fatal("a refused read must fail the command")
	}
	if exitcode.FromError(err) == 0 {
		t.Fatalf("a refused read must exit non-zero, got %v", err)
	}
	if strings.Contains(out, "no hosts on this page") {
		t.Errorf("a failed read never prints the empty-page line:\n%s", out)
	}
	if len(pageCalls(calls())) != 1 {
		t.Fatalf("a failed single page must not retry or drain, got %+v", calls())
	}
}

// --limit passes through as given: a limit over the server's 200 cap is
// served by the server (as 200 and fewer rows), never refused in the client.
func TestHostListLimitOverCapPassesThrough_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 500, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, err := hostListRun(t, server, "--limit", "500")
	if err != nil {
		t.Fatalf("the client must not refuse a large --limit: %v", err)
	}
	cs := pageCalls(calls())
	if len(cs) != 1 || string(cs[0].vars["limit"]) != "500" {
		t.Fatalf("--limit 500 must pass through as given, got %+v", cs)
	}
	for _, id := range []string{"orca1", "orca2"} {
		if !strings.Contains(out, id) {
			t.Errorf("the rows actually served must print (%s):\n%s", id, out)
		}
	}
}

// Negative values are refused client-side, before any network call.
func TestHostListNegativeFlagsRefused_2824(t *testing.T) {
	for _, args := range [][]string{{"--limit", "-1"}, {"--offset", "-3"}} {
		server, calls := hostListPageFake(t, map[string][]string{})
		_, err := hostListRun(t, server, args...)
		if err == nil || exitcode.FromError(err) != exitcode.Usage {
			t.Fatalf("%v should be a usage error, got %v", args, err)
		}
		if n := len(calls()); n != 0 {
			t.Errorf("%v must be refused before any call, got %d calls", args, n)
		}
	}
}

// A null row in a paged answer is skipped in the drain, not a panic: the
// identity closure CollectHasMore keys rows through runs before the
// consumer's own nil skip (blind F1, local F1 — both modes must agree, and
// the single-page path already skips nulls).
func TestHostListDefaultSkipsNullRows_2824(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {
			hostPageBody([]string{"null", hostListHost("orca1")}, 2, true),
			hostPageBody([]string{hostListHost("orca2")}, 2, false),
		},
		"TeamAppIdentity": {hostListLabelBody},
	})
	out, err := hostListRun(t, server)
	if err != nil {
		t.Fatalf("a null row must be skipped, not fatal: %v", err)
	}
	for _, id := range []string{"orca1", "orca2"} {
		if !strings.Contains(out, id) {
			t.Errorf("host %s missing from the drain output:\n%s", id, out)
		}
	}
}

// The whole-set read is one answer, not offset pages: drift between pages
// (the total moved) refuses the whole command with no partial table — the
// drain never prints half an inventory (blind F3: the drift checks live in
// api.CollectHasMore; this pins them at the command).
func TestHostListDefaultDriftRefusesNoPartial_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {
			hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 3, true),
			hostPageBody([]string{hostListHost("orca3")}, 4, false),
		},
		"TeamAppIdentity": {hostListLabelBody},
	})
	out, err := hostListRun(t, server)
	if err == nil || exitcode.FromError(err) == 0 {
		t.Fatalf("drift mid-drain must fail the command, got %v", err)
	}
	if !strings.Contains(err.Error(), "changed while paging") {
		t.Errorf("the refusal must name the drift, got %v", err)
	}
	if strings.Contains(out, "orca1") {
		t.Errorf("a failed drain must not print a partial table:\n%s", out)
	}
	if ops := calls(); len(ops) != 2 {
		t.Fatalf("the drain stops at the drifting page, got %+v", ops)
	}
}

// A server too old for hostRegistrationsPage keeps the default form: the
// whole list is read through hostRegistrations in one call, disclosed on
// stderr in text mode; --json keeps the bare array and stays silent.
func TestHostListOldServerFallsBackWhole_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageMissingField},
		"HostRegistrations":     {hostWholeBody([]string{hostListHost("orca1"), hostListHost("orca2")})},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, stderr, err := hostListRunBoth(t, server)
	if err != nil {
		t.Fatalf("the old server keeps the default read: %v", err)
	}
	for _, id := range []string{"orca1", "orca2"} {
		if !strings.Contains(out, id) {
			t.Errorf("host %s missing from the whole-set output:\n%s", id, out)
		}
	}
	for _, banned := range []string{"page:", "continue:"} {
		if strings.Contains(out, banned) {
			t.Errorf("the whole set is not a page (%q):\n%s", banned, out)
		}
	}
	if !strings.Contains(stderr, "hostRegistrationsPage") {
		t.Errorf("text mode must disclose the fallback on stderr, got %q", stderr)
	}
	var ops []string
	for _, c := range calls() {
		ops = append(ops, c.op)
	}
	if len(ops) != 3 || ops[0] != "HostRegistrationsPage" || ops[1] != "HostRegistrations" || ops[2] != "TeamAppIdentity" {
		t.Fatalf("one paged attempt, one whole read, then the label: %v", ops)
	}

	jsonServer, jsonCalls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageMissingField},
		"HostRegistrations":     {hostWholeBody([]string{hostListHost("orca1")})},
	})
	jsonOut, jsonStderr, err := hostListRunBoth(t, jsonServer, "--json")
	if err != nil {
		t.Fatalf("the old server keeps the default JSON read: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("default --json must stay a bare array, got %s", jsonOut)
	}
	if len(rows) != 1 || rows[0]["hostId"] != "orca1" {
		t.Fatalf("unexpected rows: %s", jsonOut)
	}
	if jsonStderr != "" {
		t.Errorf("--json must not print notes to stderr, got %q", jsonStderr)
	}
	for _, c := range jsonCalls() {
		if c.op == "TeamAppIdentity" {
			t.Errorf("--json must not pay the App label read")
		}
	}
}

// The flags cannot fall back: the unpaged field has no offset to slice, so an
// old server refuses them by name instead of the raw validation error — and
// never falls back to a whole read for a page.
func TestHostListOldServerSinglePageRefused_2824(t *testing.T) {
	server, calls := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageMissingField},
	})
	out, err := hostListRun(t, server, "--limit", "2")
	if err == nil || exitcode.FromError(err) == 0 {
		t.Fatalf("a page an old server cannot serve must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "hostRegistrationsPage") || !strings.Contains(err.Error(), "--limit/--offset") {
		t.Errorf("the refusal must name the missing field and the flags, got %v", err)
	}
	if strings.Contains(out, "no hosts on this page") {
		t.Errorf("a refused read never prints the empty-page line:\n%s", out)
	}
	for _, c := range calls() {
		if c.op == "HostRegistrations" {
			t.Errorf("a single page must not fall back to the whole-set read: %+v", c)
		}
	}
	if n := len(pageCalls(calls())); n != 1 {
		t.Fatalf("the refusal is one paged attempt, no retry, got %d", n)
	}
}

// cli#2939 (FIT AT0.9807.1): the single-page continue hint carries what the caller gave and nothing
// else, and advances by the rows the server SERVED. A hint that drops a given --limit continues in
// 20-row pages; one that drops a given --app reads the default App's list when copied; one that adds
// a flag the caller did not give widens the ask.
func TestHostListContinueHintCarriesWhatWasGiven_2939(t *testing.T) {
	two := []string{hostListHost("orca1"), hostListHost("orca2")}
	for _, tc := range []struct {
		name       string
		app        string
		serverFlag bool
		args       []string
		want       func(server string) string
		not        []string
	}{
		{"limit and offset, app and server", "hrn:app:acme.com:ops", true, []string{"--limit", "2", "--offset", "4"},
			func(s string) string {
				return "continue: hadron team host list --server " + s + " --app hrn:app:acme.com:ops --limit 2 --offset 6"
			}, nil},
		{"offset alone carries no limit", "hrn:app:acme.com:ops", true, []string{"--offset", "4"},
			func(s string) string {
				return "continue: hadron team host list --server " + s + " --app hrn:app:acme.com:ops --offset 6"
			}, []string{"--limit"}},
		{"no --app given carries no --app", "", true, []string{"--limit", "2"},
			func(s string) string {
				return "continue: hadron team host list --server " + s + " --limit 2 --offset 2"
			}, []string{"--app"}},
		{"no --server given carries no --server", "hrn:app:acme.com:ops", false, []string{"--limit", "2"},
			func(string) string {
				return "continue: hadron team host list --app hrn:app:acme.com:ops --limit 2 --offset 2"
			}, []string{"--server"}},
		// The server caps a page at 200 whatever is asked: the hint carries the value as given and
		// widens nothing.
		{"a limit over the cap is carried as given", "hrn:app:acme.com:ops", false, []string{"--limit", "500"},
			func(string) string {
				return "continue: hadron team host list --app hrn:app:acme.com:ops --limit 500 --offset 2"
			}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := hostListPageFake(t, map[string][]string{
				"HostRegistrationsPage": {hostPageBody(two, 9, true)},
				"TeamAppIdentity":       {hostListLabelBody},
			})
			out, _, err := hostListRunFlags(t, server, tc.app, tc.serverFlag, tc.args...)
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if want := tc.want(server); !strings.Contains(out, want) {
				t.Fatalf("hint must be %q:\n%s", want, out)
			}
			var hint string
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "continue:") {
					hint = l
				}
			}
			for _, n := range tc.not {
				if strings.Contains(hint, n) {
					t.Fatalf("the hint must not carry %q the caller did not give: %q", n, hint)
				}
			}
		})
	}
}

// --limit 0 asks for the count alone: the page is empty, so there is nothing to continue from and no
// hint is printed (a hint at offset+0 would never advance).
func TestHostListLimitZeroPrintsNoContinueHint_2939(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{}, 5, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunFlags(t, server, "hrn:app:acme.com:ops", true, "--limit", "0")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no hosts on this page (total 5)") || strings.Contains(out, "continue:") {
		t.Fatalf("a count-only page prints the total and no continue hint:\n%s", out)
	}
}

// A page of only null items with more rows behind it renders as an empty page but the server SERVED
// rows, so the hint is still printed and advances past them (else the list could not be continued).
// The empty-page line's naming of the skipped rows is pinned once, by
// TestHostListAllNullPageNamesTheSkippedRows_2971 (cli#3024 dropped the duplicate here).
func TestHostListAPageOfOnlyNullsStillContinues_2939(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{"null", "null"}, 6, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunFlags(t, server, "hrn:app:acme.com:ops", true, "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "--limit 2 --offset 2") {
		t.Fatalf("an all-null page still continues past the served rows:\n%s", out)
	}
}

// A null item in a single page is a server contract fault: it is skipped (silently, a recorded
// choice) and never panics, and the continue hint advances by the rows the server SERVED (3), not
// the rows rendered (2), so the null's slot is not re-read.
func TestHostListSinglePageSkipsANullRowAndContinuesPastIt_2939(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), "null", hostListHost("orca3")}, 8, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunFlags(t, server, "hrn:app:acme.com:ops", true, "--limit", "3")
	if err != nil {
		t.Fatalf("a null row must be skipped, not fail: %v\n%s", err, out)
	}
	for _, want := range []string{"orca1", "orca3", "page: 2 of 8 from offset 0", "--limit 3 --offset 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--offset 2") {
		t.Fatalf("the hint must advance past the null's slot (3 served), not by the 2 rendered:\n%s", out)
	}
}

// hostListHintCommand returns the continue hint's command text (past "continue: "), the line a
// user copies into their shell (cli#2971).
func hostListHintCommand(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "continue: ") {
			return strings.TrimPrefix(l, "continue: ")
		}
	}
	t.Fatalf("no continue hint in output:\n%s", out)
	return ""
}

// hostListShellWords is the argv a real sh splits a command line into, one NUL-ended word each:
// the hint is read back through the shell a user would paste it into, not just compared as text.
// HOME is set so an unquoted $HOME would expand, never stand as itself.
func hostListShellWords(t *testing.T, line string) []string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh to split the continue hint")
	}
	cmd := exec.Command(sh, "-c", `printf '%s\0' `+line)
	cmd.Env = []string{"HOME=/expanded-home", "PATH=/nonexistent"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh could not split the hint %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// The carried --server is quoted as one shell word (cli#2971 item 1): a URL with a query is the
// realistic quoted case — pasted unquoted, the shell cuts it at the & and runs the rest as a new
// command. Pinned as text and read back through a real sh, so dropping cmdutil.ShellQuote fails
// here rather than in a user's terminal. The carried --app is a canonical hrn:app: ref (nothing
// to quote) and gets no pin of its own.
func TestHostListCarriedServerIsOneShellWord_2971(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 9, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	// The query rides on the --server value itself: the client still reaches the fake (every path
	// answers), and the hint must carry the URL whole.
	queryServer := server + "/?a=b&c=d"
	out, _, err := hostListRunGiven(t, queryServer, "hrn:app:acme.com:ops", "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// As text: the & forces single quotes, cmdutil.ShellQuote's shape for a metacharacter-bearing
	// word.
	if want := "--server '" + queryServer + "'"; !strings.Contains(out, want) {
		t.Fatalf("the hint must carry the --server quoted as one word (%q):\n%s", want, out)
	}
	// Through sh: the whole hint parses back to the argv that re-runs this read.
	got := hostListShellWords(t, hostListHintCommand(t, out))
	want := []string{"hadron", "team", "host", "list", "--server", queryServer,
		"--app", "hrn:app:acme.com:ops", "--limit", "2", "--offset", "2"}
	if !slices.Equal(got, want) {
		t.Fatalf("sh must read the hint back as %+v, got %+v", want, got)
	}
}

// A skipped null row is NAMED in the page line (cli#2971 item 2, cor:api:120:04): the rows shown
// (2) and the offset the continue hint advances by (3) disagree exactly when a null is skipped,
// so the page line accounts for the row instead of leaving the gap unexplained.
func TestHostListSkippedNullIsNamedInThePageLine_2971(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), "null", hostListHost("orca3")}, 8, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunBoth(t, server, "--limit", "3")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "page: 2 of 8 from offset 0 (1 null row skipped)") {
		t.Fatalf("the page line must name the skipped null row:\n%s", out)
	}
	if !strings.Contains(out, "--limit 3 --offset 3") {
		t.Fatalf("the hint still advances past the null's slot:\n%s", out)
	}
}

// An all-null page names its skipped rows in the empty-page line too (cli#2971 item 2): the same
// disagreement exists there — the server served rows the line does not show.
func TestHostListAllNullPageNamesTheSkippedRows_2971(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{"null", "null"}, 6, true)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunBoth(t, server, "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no hosts on this page (total 6, 2 null rows skipped)") {
		t.Fatalf("the empty-page line must name the skipped null rows:\n%s", out)
	}
}

// The single-page --json envelope states the same account (cli#2971 item 2, cor:api:120:04):
// served counts the rows the server returned, nulls included, and nextOffset is the continue
// point — present as null when no further page follows (a last page, or a count-only page with
// nothing served to continue from).
func TestHostListPageJSONServedAndNextOffset_2971(t *testing.T) {
	for _, tc := range []struct {
		name     string
		items    []string
		total    int
		hasMore  bool
		args     []string
		rendered int
		served   string
		next     string
	}{
		{"a null row is served and counted", []string{hostListHost("orca1"), "null", hostListHost("orca3")}, 8, true,
			[]string{"--limit", "3"}, 2, "3", "3"},
		{"the last page has no next offset", []string{hostListHost("orca9")}, 9, false,
			[]string{"--limit", "3", "--offset", "8"}, 1, "1", "null"},
		{"a count-only page has none either", nil, 5, true,
			[]string{"--limit", "0"}, 0, "0", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := hostListPageFake(t, map[string][]string{
				"HostRegistrationsPage": {hostPageBody(tc.items, tc.total, tc.hasMore)},
			})
			out, err := hostListRun(t, server, append(tc.args, "--json")...)
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			var env map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("single-page --json must be the envelope, got %s", out)
			}
			if string(env["served"]) != tc.served {
				t.Fatalf("served must be %s (rows the server returned, nulls included), got %s", tc.served, env["served"])
			}
			// nextOffset is PRESENT as null when there is none, never dropped from the envelope.
			next, ok := env["nextOffset"]
			if !ok {
				t.Fatalf("nextOffset must be present in the envelope, got %s", out)
			}
			if string(next) != tc.next {
				t.Fatalf("nextOffset must be %s, got %s", tc.next, next)
			}
			var items []json.RawMessage
			if err := json.Unmarshal(env["items"], &items); err != nil {
				t.Fatalf("items must be an array, got %s", out)
			}
			if len(items) != tc.rendered {
				t.Fatalf("items must hold the %d rendered rows, got %d", tc.rendered, len(items))
			}
		})
	}
}

// The help states what the continue line carries and where it is right (cli#2971 item 3,
// cor:agt:070:01): only --server and --app of the run's context are carried, and an App resolved
// from the worktree binding makes the hint right only from that directory.
func TestHostListHelpStatesWhatTheHintCarries_2971(t *testing.T) {
	cmd := newCmdTeamHostLs(cmdutil.NewFactory())
	// Whitespace-normalized so the pins do not depend on where the help wraps.
	help := strings.Join(strings.Fields(cmd.Long), " ")
	for _, want := range []string{
		"only --server and --app are carried",
		"an App resolved from the worktree binding makes the hint right only from that directory",
		// The envelope the help names is the one with the continue point in it.
		"{items,total,hasMore,offset,limit,served,nextOffset}",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("the help must state %q:\n%s", want, cmd.Long)
		}
	}
}

// Pins the review found missing on behavior that already held (cli#2971 item 4): the minimal
// continue line — nothing given but --offset, so the hint carries no flag value at all — and a
// last page (hasMore false) that prints no continue line.
func TestHostListMinimalHintAndHintlessLastPage_2971(t *testing.T) {
	t.Run("nothing given but offset", func(t *testing.T) {
		server, _ := hostListPageFake(t, map[string][]string{
			"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1"), hostListHost("orca2")}, 9, true)},
			"TeamAppIdentity":       {hostListLabelBody},
		})
		// No --server flag (HADRON_SERVER carries the fake), no --app (the configured
		// context), no --limit: the hint names nothing but the advanced --offset.
		out, _, err := hostListRunFlags(t, server, "", false, "--offset", "0")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, out)
		}
		if got := hostListHintCommand(t, out); got != "hadron team host list --offset 2" {
			t.Fatalf("the minimal hint must carry only the advanced offset, got %q:\n%s", got, out)
		}
	})
	t.Run("last page prints no hint", func(t *testing.T) {
		server, _ := hostListPageFake(t, map[string][]string{
			"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca9")}, 9, false)},
			"TeamAppIdentity":       {hostListLabelBody},
		})
		// The real last page of the nine (cli#3024): offset 8 serves the 9th row and
		// hasMore is false — not the former offset-0 artifice of 1 row served out of 9.
		out, _, err := hostListRunBoth(t, server, "--limit", "3", "--offset", "8")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, out)
		}
		if !strings.Contains(out, "page: 1 of 9 from offset 8") || strings.Contains(out, "continue:") {
			t.Fatalf("a last page keeps its page line and prints no continue hint:\n%s", out)
		}
	})
}

// An inconsistent last page, DELIBERATE (cli#3024): hasMore false while offset+served falls
// short of the total — a server contract fault the former last-page fixtures covered only by
// accident (offset 0 serving 1 of 9). The CLI relays the envelope rather than second-guessing
// it: the page line states its own numbers, and no continue hint is printed (nextOffset is
// null; the CLI does not invent a continuation the server never offered).
func TestHostListInconsistentLastPageIsRelayedNotFixed_3024(t *testing.T) {
	server, _ := hostListPageFake(t, map[string][]string{
		"HostRegistrationsPage": {hostPageBody([]string{hostListHost("orca1")}, 9, false)},
		"TeamAppIdentity":       {hostListLabelBody},
	})
	out, _, err := hostListRunBoth(t, server, "--limit", "3")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "page: 1 of 9 from offset 0") || strings.Contains(out, "continue:") {
		t.Fatalf("an inconsistent last page keeps its page line and prints no hint:\n%s", out)
	}
}

// The text hint and the --json envelope agree on the continue point (cli#3024): NextOffset is
// computed once, so the hint's --offset and the envelope's nextOffset cannot drift apart —
// neither on a page that served null rows (the continue point advances by rows served, not
// rows shown) nor on a last page, where the envelope says null and the text prints no hint.
func TestHostListHintAgreesWithEnvelopeNextOffset_3024(t *testing.T) {
	for _, tc := range []struct {
		name    string
		items   []string
		total   int
		hasMore bool
		args    []string
	}{
		{"null rows advance both", []string{hostListHost("orca1"), "null", hostListHost("orca3")}, 8, true,
			[]string{"--limit", "3"}},
		{"the last page continues nowhere", []string{hostListHost("orca9")}, 9, false,
			[]string{"--limit", "3", "--offset", "8"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Two bodies per operation: the fixture is read twice, once per mode.
			page := hostPageBody(tc.items, tc.total, tc.hasMore)
			server, _ := hostListPageFake(t, map[string][]string{
				"HostRegistrationsPage": {page, page},
				"TeamAppIdentity":       {hostListLabelBody, hostListLabelBody},
			})
			// One fixture read both ways: text for the hint, --json for nextOffset.
			text, _, err := hostListRunBoth(t, server, tc.args...)
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, text)
			}
			asJSON, _, err := hostListRunBoth(t, server, append(tc.args, "--json")...)
			if err != nil {
				t.Fatalf("execute --json: %v\n%s", err, asJSON)
			}
			var env struct {
				NextOffset *int `json:"nextOffset"`
			}
			if err := json.Unmarshal([]byte(asJSON), &env); err != nil {
				t.Fatalf("single-page --json must be the envelope, got %s", asJSON)
			}
			hint := ""
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(l, "continue: ") {
					hint = strings.TrimPrefix(l, "continue: ")
				}
			}
			if env.NextOffset == nil {
				if hint != "" {
					t.Fatalf("a null nextOffset must print no hint, got %q:\n%s", hint, text)
				}
				return
			}
			want := fmt.Sprintf("--offset %d", *env.NextOffset)
			if !strings.HasSuffix(hint, want) {
				t.Fatalf("the hint must end %q (the envelope's nextOffset), got %q:\n%s", want, hint, text)
			}
		})
	}
}
