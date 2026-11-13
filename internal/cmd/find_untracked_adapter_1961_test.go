package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1961 (cor:agt:120, cor:agt:120:04): find-untracked on a task intake
// ADAPTER, through the compiled command tree against a fake GraphQL server and
// a deterministic fake-adapter executable. The FIT's seven rules are pinned
// here by name.

const (
	itemA1961 = "https://github.com/acme/app/issues/1"
	itemB1961 = "https://github.com/acme/app/issues/2"
	itemC1961 = "https://github.com/acme/app/issues/3"
)

const describeBody1961 = `{"name":"fake-gh","contractVersion":1,"calls":[],"filters":[],"order":"oldest first"}`

func item1961(id, title string) string {
	return fmt.Sprintf(`{"externalId":%q,"title":%q}`, id, title)
}

// fakeAdapter1961 writes the adapter and returns its path and request log. The
// log has one line per call: "<call> <request json>". list is the shell that
// answers list-untracked (it may read $req); mark answers mark-tracked.
func fakeAdapter1961(t *testing.T, describe, list, mark string) (path, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script adapter fixtures need a POSIX shell")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	path = filepath.Join(dir, "adapter")
	if describe == "" {
		describe = describeBody1961
	}
	if mark == "" {
		mark = `echo '{}'`
	}
	script := "#!/bin/sh\nreq=$(cat)\nprintf '%s %s\\n' \"$1\" \"$req\" >> '" + log + "'\ncase \"$1\" in\n" +
		"describe) echo '" + describe + "';;\n" +
		"list-untracked) " + list + ";;\n" +
		"mark-tracked) " + mark + ";;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func adapterLog1961(t *testing.T, log, call string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if rest, ok := strings.CutPrefix(line, call+" "); ok {
			out = append(out, rest)
		}
	}
	return out
}

func held1961(ids ...string) string {
	var rows []string
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf(`{"id":"held-%d","externalId":%q}`, len(rows), id))
	}
	return `{"data":{"agentTasks":[` + strings.Join(rows, ",") + `]}}`
}

func claimed1961(id, ext, assigneeID, assigneeName, portal string) string {
	p := "null"
	if portal != "" {
		p = fmt.Sprintf("%q", portal)
	}
	return fmt.Sprintf(`{"data":{"createAgentTask":{"id":%q,"handle":"AT0.%s","title":"t","status":"PENDING","externalId":%q,"portalUrl":%s,"assignee":{"id":%q,"kind":"WORKER","name":%q,"urn":"hrn:worker:x"}}}}`,
		id, id, ext, p, assigneeID, assigneeName)
}

const taken1961 = `{"errors":[{"message":"This App already has a task for that external id.","extensions":{"code":"TASK_EXTERNAL_ID_TAKEN"}}]}`
const started1961 = `{"data":{"startAgentTask":{"id":"x","status":"STARTED"}}}`

// run1961 runs find-untracked --adapter <path>. onClaim, when set, answers the
// Nth IntakeClaimTask by restaging the response as the call arrives.
func run1961(t *testing.T, adapter string, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	t.Setenv(adapterEnvForTest, "")
	return runFind1524(t, responses, append([]string{"--adapter", adapter}, argv...)...)
}

const adapterEnvForTest = "HADRON_INTAKE_ADAPTER"

func opsOf1961(calls *[]attnCall, op string) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == op {
			got = append(got, c)
		}
	}
	return got
}

func inputOf1961(t *testing.T, c attnCall) map[string]any {
	t.Helper()
	in, _ := c.Vars["input"].(map[string]any)
	if in == nil {
		t.Fatalf("no input in %v", c.Vars)
	}
	return in
}

func hadronOps1961(calls *[]attnCall) []string {
	var ops []string
	for _, c := range *calls {
		switch c.Op {
		case "IntakeTasksByExternalIds", "IntakeClaimTask", "IntakeStartTask":
			ops = append(ops, c.Op)
		}
	}
	return ops
}

// ---- list ----------------------------------------------------------------

func TestAdapterListDropsOnlyWhatHadronHolds1961(t *testing.T) {
	list := `echo '{"items":[` + item1961(itemA1961, "one") + `,` + item1961(itemB1961, "two") + `,` + item1961(itemC1961, "three") + `]}'`
	path, log := fakeAdapter1961(t, "", list, "")
	calls, out, _, err := run1961(t, path, map[string]string{"IntakeTasksByExternalIds": held1961(itemB1961)}, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Adapter  string
		Items    []struct{ ExternalId, Title string }
		Complete bool
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if dto.Adapter != "fake-gh" || len(dto.Items) != 2 || dto.Items[0].ExternalId != itemA1961 || dto.Items[1].ExternalId != itemC1961 || !dto.Complete {
		t.Fatalf("got %+v", dto)
	}
	if got := hadronOps1961(calls); len(got) != 1 || got[0] != "IntakeTasksByExternalIds" {
		t.Fatalf("a list reads once and writes nothing: %v", got)
	}
	ids, _ := opsOf1961(calls, "IntakeTasksByExternalIds")[0].Vars["externalIds"].([]any)
	if len(ids) != 3 || ids[0] != itemA1961 {
		t.Fatalf("every listed id is checked, as written: %v", ids)
	}
	if n := len(adapterLog1961(t, log, "mark-tracked")); n != 0 {
		t.Fatalf("a list marks nothing: %d", n)
	}
}

// Rule 2: the tracker's mark never makes a claim. The adapter lists an item
// Hadron does not hold even though the adapter "knows" it; it is claimable.
// And an item is dropped ONLY because Hadron holds it.
func TestAdapterItemIsClaimableUnlessHadronHoldsIt1961(t *testing.T) {
	path, _ := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "one")+`]}'`, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	calls, _, _, err := run1961(t, path, r, "--track")
	if err != nil {
		t.Fatal(err)
	}
	if len(opsOf1961(calls, "IntakeClaimTask")) != 1 {
		t.Fatalf("an item Hadron does not hold must be claimed: %v", hadronOps1961(calls))
	}
}

func TestAdapterListLimitStopsPagingAtN1961(t *testing.T) {
	list := `case "$req" in *'"cursor":"p2"'*) echo '{"items":[` + item1961(itemC1961, "c") + `]}';; *) echo '{"items":[` + item1961(itemA1961, "a") + `,` + item1961(itemB1961, "b") + `],"nextCursor":"p2"}';; esac`
	path, log := fakeAdapter1961(t, "", list, "")
	_, out, _, err := run1961(t, path, map[string]string{"IntakeTasksByExternalIds": held1961()}, "--json", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, itemC1961) || len(adapterLog1961(t, log, "list-untracked")) != 1 {
		t.Fatalf("the second page must not be asked for once 2 are held: %s / %v", out, adapterLog1961(t, log, "list-untracked"))
	}
}

// ---- the claim -----------------------------------------------------------

func TestAdapterTrackClaimsThenMarks1961(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "Fix the thing")+`]}'`, "")
	r := map[string]string{
		"IntakeTasksByExternalIds": held1961(),
		"IntakeClaimTask":          claimed1961("t1", itemA1961, "wkr1", "Iris", "https://portal.test/app/apps/a1/tasks/t1"),
		"IntakeStartTask":          started1961,
	}
	calls, out, _, err := run1961(t, path, r, "--track", "--json")
	if err != nil {
		t.Fatal(err)
	}
	in := inputOf1961(t, opsOf1961(calls, "IntakeClaimTask")[0])
	if in["externalId"] != itemA1961 || in["source"] != itemA1961 {
		t.Fatalf("the external id is the adapter's URL as written: %v", in)
	}
	if a, _ := in["assignee"].(map[string]any); a["worker"] != "wkr1" {
		t.Fatalf("with no --for the bound worker holds the claim: %v", in["assignee"])
	}
	for _, banned := range []string{"tags", "labels", "properties", "description"} {
		if v, ok := in[banned]; ok && v != nil {
			t.Errorf("no tracker label or extra field reaches Hadron: %s in %v", banned, in)
		}
	}
	if len(opsOf1961(calls, "IntakeStartTask")) != 1 {
		t.Fatal("a claim the caller holds is started")
	}
	marks := adapterLog1961(t, log, "mark-tracked")
	if len(marks) != 1 {
		t.Fatalf("one mark per won claim: %v", marks)
	}
	for _, want := range []string{`"externalId":"` + itemA1961 + `"`, `"id":"t1"`, `"portalUrl":"https://portal.test/app/apps/a1/tasks/t1"`,
		`"id":"wkr1"`, `"name":"Iris"`, `"role":"backend-engineer"`} {
		if !strings.Contains(marks[0], want) {
			t.Errorf("mark-tracked lacks %s: %s", want, marks[0])
		}
	}
	var dto struct {
		Tasks []struct {
			ID, Status string
			Marked     bool
		}
		Lost     []string
		Complete bool
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Tasks) != 1 || dto.Tasks[0].Status != "STARTED" || !dto.Tasks[0].Marked || !dto.Complete || len(dto.Lost) != 0 {
		t.Fatalf("got %s", out)
	}
}

// Rule 6: a null portal URL is passed as none, never built.
func TestAdapterNullPortalURLIsNotBuilt1961(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	if _, _, _, err := run1961(t, path, r, "--track"); err != nil {
		t.Fatal(err)
	}
	if m := adapterLog1961(t, log, "mark-tracked"); len(m) != 1 || strings.Contains(m[0], "portalUrl") {
		t.Fatalf("no portal URL may be invented: %v", m)
	}
}

// Rules 1 and 4: TASK_EXTERNAL_ID_TAKEN means this caller lost. The loser does
// not look up the holder, does not adopt or mark it, and does not count it
// toward --limit; the walk goes on to the next page.
func TestAdapterLostRaceIsNotCountedAndNeverMarked1961(t *testing.T) {
	list := `case "$req" in *'"cursor":"p2"'*) echo '{"items":[` + item1961(itemB1961, "b") + `]}';; *) echo '{"items":[` + item1961(itemA1961, "a") + `],"nextCursor":"p2"}';; esac`
	path, log := fakeAdapter1961(t, "", list, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": taken1961, "IntakeStartTask": started1961}
	var n atomic.Int32
	t.Setenv(adapterEnvForTest, "")
	// The first create (A) is lost; the second (B) is won.
	calls, out, errOut, err := runFindHook1961(t, r, func(op string, restage func(string)) {
		if op == "IntakeClaimTask" && n.Add(1) == 2 {
			restage(claimed1961("t2", itemB1961, "wkr1", "Iris", ""))
		}
	}, "--adapter", path, "--track", "--limit", "1", "--json")
	if err != nil {
		t.Fatalf("a lost race is not a failure: %v", err)
	}
	if got := len(opsOf1961(calls, "IntakeClaimTask")); got != 2 {
		t.Fatalf("a lost create must not count toward --limit: %d creates", got)
	}
	marks := adapterLog1961(t, log, "mark-tracked")
	if len(marks) != 1 || strings.Contains(marks[0], itemA1961) || !strings.Contains(marks[0], itemB1961) {
		t.Fatalf("only the winner marks: %v", marks)
	}
	var dto struct {
		Tasks []struct{ ExternalId string }
		Lost  []string
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Tasks) != 1 || dto.Tasks[0].ExternalId != itemB1961 || len(dto.Lost) != 1 || dto.Lost[0] != itemA1961 {
		t.Fatalf("got %s", out)
	}
	if !strings.Contains(errOut, "another caller claimed") {
		t.Errorf("the loss is noted on stderr: %q", errOut)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskList" || c.Op == "AgentTaskGet" || strings.Contains(c.Op, "Reassign") {
			t.Errorf("the loser must not look up or adopt the holder: %s", c.Op)
		}
	}
}

// Rule 3: no normalisation. An id with a trailing space, upper-case, a query
// and a fragment is sent byte for byte; only the server judges it.
func TestAdapterExternalIDIsSentExactlyAsWritten1961(t *testing.T) {
	odd := "HTTPS://GitHub.com/Acme/App/issues/7/?q=A%2fB#Frag "
	oddJSON, _ := json.Marshal(odd)
	path, log := fakeAdapter1961(t, "", `echo '{"items":[{"externalId":`+string(oddJSON)+`,"title":"odd"}]}'`, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", odd, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	calls, _, _, err := run1961(t, path, r, "--track")
	if err != nil {
		t.Fatal(err)
	}
	if in := inputOf1961(t, opsOf1961(calls, "IntakeClaimTask")[0]); in["externalId"] != odd {
		t.Fatalf("externalId = %q, want %q", in["externalId"], odd)
	}
	ids, _ := opsOf1961(calls, "IntakeTasksByExternalIds")[0].Vars["externalIds"].([]any)
	if len(ids) != 1 || ids[0] != odd {
		t.Fatalf("the check compares the same string: %v", ids)
	}
	if m := adapterLog1961(t, log, "mark-tracked"); len(m) != 1 || !strings.Contains(m[0], `#Frag `) {
		t.Fatalf("mark-tracked gets the id the claim wrote: %v", m)
	}
}

// --for X: X's items, X holds the claim, --assignee overrides. The caller is
// not X, so the server will not let the caller start it: it stays PENDING and
// no start is attempted.
func TestAdapterForAndAssigneeOverride1961(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkrM", "Marco", ""), "IntakeStartTask": started1961}
	calls, _, errOut, err := run1961(t, path, r, "--track", "--for", "Marco")
	if err != nil {
		t.Fatal(err)
	}
	if req := adapterLog1961(t, log, "list-untracked")[0]; !strings.Contains(req, `"for":"Marco"`) {
		t.Fatalf("the adapter is asked for Marco's items: %s", req)
	}
	if a, _ := inputOf1961(t, opsOf1961(calls, "IntakeClaimTask")[0])["assignee"].(map[string]any); a["worker"] != "Marco" {
		t.Fatalf("--for sets the default assignee: %v", a)
	}
	if len(opsOf1961(calls, "IntakeStartTask")) != 0 {
		t.Fatal("only the assignee may start a task; a claim held for someone else is not started")
	}
	if !strings.Contains(errOut, "PENDING") {
		t.Errorf("a pending claim is said to be pending: %q", errOut)
	}
	if m := adapterLog1961(t, log, "mark-tracked")[0]; strings.Contains(m, `"role"`) || strings.Contains(m, `"model"`) {
		t.Errorf("a worker detail the CLI does not know is omitted, not borrowed from the caller: %s", m)
	}

	path2, _ := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	calls2, _, _, err := run1961(t, path2, r, "--track", "--for", "Marco", "--assignee", "worker:Uma")
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := inputOf1961(t, opsOf1961(calls2, "IntakeClaimTask")[0])["assignee"].(map[string]any); a["worker"] != "Uma" {
		t.Fatalf("--assignee overrides --for: %v", a)
	}
}

// A refusal of the create other than the race stops the walk, prints what was
// already claimed, and exits with the refusal's code.
func TestAdapterCreateRefusalStopsAfterPrintingWhatWasClaimed1961(t *testing.T) {
	path, _ := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`,`+item1961(itemB1961, "b")+`]}'`, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	var n atomic.Int32
	t.Setenv(adapterEnvForTest, "")
	calls, out, _, err := runFindHook1961(t, r, func(op string, restage func(string)) {
		if op == "IntakeClaimTask" && n.Add(1) == 2 {
			restage(`{"errors":[{"message":"bad assignee","extensions":{"code":"TASK_ASSIGNEE_INVALID"}}]}`)
		}
	}, "--adapter", path, "--track", "--limit", "5", "--json")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("exit = %d: %v", exitCodeFor(err), err)
	}
	if !strings.Contains(out, `"id": "t1"`) && !strings.Contains(out, `"id":"t1"`) {
		t.Fatalf("what was claimed is printed before the failure: %q", out)
	}
	if len(opsOf1961(calls, "IntakeClaimTask")) != 2 {
		t.Fatal("the walk stops at the refusal")
	}
}

// A failed mark leaves the claim standing, says so, and stops claiming more.
func TestAdapterFailedMarkLeavesTheClaimStanding1961(t *testing.T) {
	path, _ := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`,`+item1961(itemB1961, "b")+`]}'`, `echo nope >&2; exit 4`)
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	calls, out, _, err := run1961(t, path, r, "--track", "--limit", "5", "--json")
	if exitCodeFor(err) != exitcode.Error {
		t.Fatalf("exit = %d: %v", exitCodeFor(err), err)
	}
	if len(opsOf1961(calls, "IntakeClaimTask")) != 1 {
		t.Fatal("no more claims once the mark fails")
	}
	if !strings.Contains(out, `"marked": false`) && !strings.Contains(out, `"marked":false`) {
		t.Fatalf("the claim is reported unmarked: %s", out)
	}
}

// ---- the runner's refusals ------------------------------------------------

func TestAdapterContractSkewIsItsOwnExitAndAsksNothing1961(t *testing.T) {
	path, log := fakeAdapter1961(t, `{"name":"fake-gh","contractVersion":2}`, `echo '{"items":[]}'`, "")
	calls, out, _, err := run1961(t, path, map[string]string{"IntakeTasksByExternalIds": held1961()}, "--track")
	if exitCodeFor(err) != exitcode.AdapterSkew || out != "" {
		t.Fatalf("exit = %d out %q: %v", exitCodeFor(err), out, err)
	}
	if len(adapterLog1961(t, log, "list-untracked")) != 0 || len(hadronOps1961(calls)) != 0 {
		t.Fatal("a skewed adapter is asked for nothing and Hadron is not touched")
	}
}

func TestAdapterHostileAnswersRefuseSafelyAndClaimNothing1961(t *testing.T) {
	for name, list := range map[string]string{
		"oversized":     `head -c 3000000 /dev/zero | tr '\0' 'a'`,
		"malformed":     `echo '{"items":[{"externalId":'`,
		"cursor loop":   `echo '{"items":[` + item1961(itemA1961, "a") + `],"nextCursor":"same"}'`,
		"not an object": `echo '[]'`,
	} {
		t.Run(name, func(t *testing.T) {
			path, _ := fakeAdapter1961(t, "", list, "")
			r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
			calls, _, _, err := run1961(t, path, r, "--track", "--limit", "5")
			if exitCodeFor(err) != exitcode.Error {
				t.Fatalf("exit = %d: %v", exitCodeFor(err), err)
			}
			if name == "cursor loop" {
				return // the first page's items may legitimately have been claimed before the loop was seen
			}
			if len(opsOf1961(calls, "IntakeClaimTask")) != 0 {
				t.Fatal("nothing is claimed from an answer that was refused")
			}
		})
	}
}

func TestAdapterTerminalEscapesInItemTextNeverReachHadronOrTheTerminal1961(t *testing.T) {
	list := `printf '{"items":[{"externalId":"` + itemA1961 + `","title":"\\u001b[2Jevil\\u202e"}]}'`
	path, _ := fakeAdapter1961(t, "", list, "")
	r := map[string]string{"IntakeTasksByExternalIds": held1961(), "IntakeClaimTask": claimed1961("t1", itemA1961, "wkr1", "Iris", ""), "IntakeStartTask": started1961}
	calls, out, _, err := run1961(t, path, r, "--track")
	if err != nil {
		t.Fatal(err)
	}
	title, _ := inputOf1961(t, opsOf1961(calls, "IntakeClaimTask")[0])["title"].(string)
	if strings.ContainsRune(title, 0x1b) || strings.ContainsRune(title, 0x202e) || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("control and bidi characters must be stripped: title %q out %q", title, out)
	}
}

// ---- flag selection ------------------------------------------------------

func TestAdapterFlagsRefusedLocallyWithZeroRequests1961(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[]}'`, "")
	for name, argv := range map[string][]string{
		"--skip (rule 5: no offset)":      {"--adapter", path, "--skip", "3"},
		"--team":                          {"--adapter", path, "--team", "Marco"},
		"--no-team":                       {"--adapter", path, "--no-team"},
		"--ready":                         {"--adapter", path, "--ready", "only"},
		"--repo":                          {"--adapter", path, "--repo", "a/b"},
		"--assign-to-me":                  {"--adapter", path, "--assign-to-me"},
		"--assignee without --track":      {"--adapter", path, "--assignee", "worker:Uma"},
		"--owner without --track":         {"--adapter", path, "--owner", "user:holger"},
		"--adapter and --integration":     {"--adapter", path, "--integration", "github"},
		"--limit 0":                       {"--adapter", path, "--limit", "0"},
		"--limit 101":                     {"--adapter", path, "--limit", "101"},
		"empty --adapter":                 {"--adapter", ""},
		"empty --for":                     {"--adapter", path, "--for", ""},
		"malformed --assignee":            {"--adapter", path, "--track", "--assignee", "Uma"},
		"--track without an adapter":      {"--integration", "github", "--track"},
		"--for without an adapter":        {"--integration", "github", "--for", "Marco"},
		"neither adapter nor integration": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(adapterEnvForTest, "")
			calls, out, _, err := runFind1524(t, claimOK1596(), argv...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if len(*calls) != 0 || out != "" {
				t.Errorf("zero requests and no output expected: %v %q", *calls, out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Errorf("the adapter must not be run: %s", b)
			}
		})
	}
}

func TestAdapterFromEnvironmentAndIntegrationWins1961(t *testing.T) {
	path, _ := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	t.Setenv(adapterEnvForTest, path)
	calls, _, _, err := runFind1524(t, map[string]string{"IntakeTasksByExternalIds": held1961()}, "--json")
	if err != nil || len(opsOf1961(calls, "IntakeTasksByExternalIds")) != 1 {
		t.Fatalf("$HADRON_INTAKE_ADAPTER selects the adapter: %v %v", err, hadronOps1961(calls))
	}
	// An explicit --integration is the compat path, even with the variable set.
	calls, _, _, err = runFind1524(t, claimOK1596(), "--integration", "github")
	if err != nil || len(opsOf1961(calls, "UntrackedTaskItems")) != 1 {
		t.Fatalf("--integration keeps the server path working: %v %v", err, *calls)
	}
}

func TestAdapterSkewMessageNamesBothVersions1961(t *testing.T) {
	path, _ := fakeAdapter1961(t, `{"name":"fake-gh","contractVersion":7}`, `echo '{"items":[]}'`, "")
	_, _, _, err := run1961(t, path, map[string]string{}, "--json")
	if err == nil || !strings.Contains(err.Error(), "version 7") || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("%v", err)
	}
}

// runFindHook1961 is runFind1524 with a hook run as each operation arrives; the
// hook may restage that operation's response (how a test makes the Nth create
// lose the race or be refused).
func runFindHook1961(t *testing.T, responses map[string]string, hook func(op string, restage func(string)), argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, responses, func(op string) {
		hook(op, func(resp string) { setAttnResponse(responses, op, resp) })
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "team", "task", "find-untracked"}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// A failure between the create and the start (gate addendum 2): the task
// exists and is PENDING, the CLI reports it, the mark still happens (the claim
// stands), and a retry finds the external id held, so it creates nothing.
func TestAdapterStartFailureAfterCreateIsReportedAndRetryDedupes1961(t *testing.T) {
	path, log := fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	r := map[string]string{
		"IntakeTasksByExternalIds": held1961(),
		"IntakeClaimTask":          claimed1961("t1", itemA1961, "wkr1", "Iris", ""),
		"IntakeStartTask":          `{"errors":[{"message":"cannot start","extensions":{"code":"TASK_INVALID_TRANSITION"}}]}`,
	}
	_, out, errOut, err := run1961(t, path, r, "--track", "--json")
	if err != nil {
		t.Fatalf("the claim stands: %v", err)
	}
	var dto struct {
		Tasks []struct {
			ID, Status string
			Marked     bool
			StartError *string
		}
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Tasks) != 1 || dto.Tasks[0].Status != "PENDING" || dto.Tasks[0].StartError == nil || !dto.Tasks[0].Marked {
		t.Fatalf("got %s", out)
	}
	if !strings.Contains(errOut, "could not be started") || len(adapterLog1961(t, log, "mark-tracked")) != 1 {
		t.Fatalf("the failure is reported and the claim still marked: %q", errOut)
	}
	// The retry: Hadron now holds the id, so nothing is created.
	r["IntakeTasksByExternalIds"] = held1961(itemA1961)
	calls, _, _, err := run1961(t, path, r, "--track", "--json")
	if err != nil || len(opsOf1961(calls, "IntakeClaimTask")) != 0 {
		t.Fatalf("a retry must not create again: %v %v", err, hadronOps1961(calls))
	}
}
