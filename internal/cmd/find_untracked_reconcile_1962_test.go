package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1962 (cor:agt:120 D4, cor:agt:120:04): the reconcile pass at the start of
// every find-untracked adapter run. The mapping, from the tasks that changed
// since the CLI's cursor: a reassignment calls mark-tracked again, a release
// calls untrack with the RELEASED id, a cancel calls report only when the
// adapter's describe declares it, a finish does nothing. T is the server's
// opaque cursor, kept per server, App and adapter.

const describeAll1962 = `{"name":"fake-gh","contractVersion":1,"calls":["describe","list-untracked","mark-tracked","untrack","report"],"filters":[],"order":"oldest first"}`
const describeNoReport1962 = `{"name":"fake-gh","contractVersion":1,"calls":["describe","list-untracked","mark-tracked","untrack"],"filters":[],"order":"oldest first"}`

// fakeAdapter1962 is fakeAdapter1961 that also answers untrack and report.
// untrack is the shell for untrack (it may read $req), so a test can make it fail.
func fakeAdapter1962(t *testing.T, describe, untrack string) (path, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script adapter fixtures need a POSIX shell")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	path = filepath.Join(dir, "adapter")
	if untrack == "" {
		untrack = `echo '{}'`
	}
	script := "#!/bin/sh\nreq=$(cat)\nprintf '%s %s\\n' \"$1\" \"$req\" >> '" + log + "'\ncase \"$1\" in\n" +
		"describe) echo '" + describe + "';;\n" +
		"list-untracked) echo '{\"items\":[]}';;\n" +
		"mark-tracked) echo '{}';;\n" +
		"untrack) " + untrack + ";;\n" +
		"report) echo '{}';;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func change1962(kind, ext, cursor, task, handle, assigneeID, assigneeName string) string {
	assignee := "null"
	if assigneeID != "" {
		assignee = fmt.Sprintf(`{"id":%q,"name":%q}`, assigneeID, assigneeName)
	}
	return fmt.Sprintf(`{"cursor":%q,"kind":%q,"externalId":%q,"task":{"id":%q,"handle":%q,"portalUrl":"https://portal/t/%s","assignee":%s}}`,
		cursor, kind, ext, task, handle, task, assignee)
}

func feed1962(hasMore bool, next string, changes ...string) string {
	n := "null"
	if next != "" {
		n = fmt.Sprintf("%q", next)
	}
	return fmt.Sprintf(`{"data":{"agentTaskExternalIdChanges":{"changes":[%s],"hasMore":%v,"nextCursor":%s}}}`, strings.Join(changes, ","), hasMore, n)
}

func run1962(t *testing.T, path string, feed string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	return run1961(t, path, map[string]string{"IntakeExternalIdChanges": feed}, argv...)
}

func feedCalls1962(calls *[]attnCall) []attnCall { return opsOf1961(calls, "IntakeExternalIdChanges") }

const (
	itemR1 = "https://github.com/acme/app/issues/11"
	itemR2 = "https://github.com/acme/app/issues/12"
	itemR3 = "https://github.com/acme/app/issues/13"
	itemR4 = "https://github.com/acme/app/issues/14"
)

// The mapping, one change of each kind. The first run has no cursor, so the
// read omits `after` (the server starts at its oldest change) and never sends null.
func TestReconcileMapsEachChangeToItsAdapterCall1962(t *testing.T) {
	path, log := fakeAdapter1962(t, describeAll1962, "")
	feed := feed1962(false, "",
		change1962("REASSIGNED", itemR1, "c1", "t1", "AT1.1", "wkr9", "Gus"),
		change1962("RELEASED", itemR2, "c2", "t2", "AT1.2", "", ""),
		change1962("CANCELLED", itemR3, "c3", "t3", "AT1.3", "wkr1", "Iris"),
		change1962("FINISHED", itemR4, "c4", "t4", "AT1.4", "wkr1", "Iris"),
		change1962("ASSIGNED", itemR4, "c5", "t4", "AT1.4", "wkr1", "Iris"),
		change1962("HANDED_BACK", itemR4, "c6", "t4", "AT1.4", "", ""),
	)
	calls, out, _, err := run1962(t, path, feed, "--json")
	if err != nil {
		t.Fatal(err)
	}
	fc := feedCalls1962(calls)
	if len(fc) != 1 {
		t.Fatalf("one page: %d reads", len(fc))
	}
	if _, has := fc[0].Vars["after"]; has {
		t.Fatalf("the first run has no cursor: after must be omitted, not null: %v", fc[0].Vars)
	}
	marks := adapterLog1961(t, log, "mark-tracked")
	if len(marks) != 1 || !strings.Contains(marks[0], itemR1) || !strings.Contains(marks[0], `"id":"wkr9"`) || !strings.Contains(marks[0], `"name":"Gus"`) || !strings.Contains(marks[0], "https://portal/t/t1") {
		t.Fatalf("a reassignment re-marks for the new assignee with the server's portal URL: %v", marks)
	}
	if strings.Contains(marks[0], `"model"`) || strings.Contains(marks[0], `"role"`) {
		t.Fatalf("no claim-time model/role exists for a reassignment, so none is invented: %v", marks)
	}
	un := adapterLog1961(t, log, "untrack")
	if len(un) != 1 || !strings.Contains(un[0], itemR2) {
		t.Fatalf("a release untracks with the RELEASED id the feed names: %v", un)
	}
	rep := adapterLog1961(t, log, "report")
	if len(rep) != 1 || !strings.Contains(rep[0], itemR3) || !strings.Contains(rep[0], `"state":"CANCELLED"`) {
		t.Fatalf("a cancel reports when the adapter declares report: %v", rep)
	}
	for _, want := range []string{`"marked": 1`, `"untracked": 1`, `"reported": 1`, `"skipped": 3`, `"read": 6`, `"complete": true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("--json reconcile lacks %s: %s", want, out)
		}
	}
}

// A cancel's report is the adapter's choice: not declared, not called.
func TestReconcileReportOnlyWhenDeclared1962(t *testing.T) {
	path, log := fakeAdapter1962(t, describeNoReport1962, "")
	_, _, _, err := run1962(t, path, feed1962(false, "", change1962("CANCELLED", itemR3, "c3", "t3", "AT1.3", "wkr1", "Iris")))
	if err != nil {
		t.Fatal(err)
	}
	if got := adapterLog1961(t, log, "report"); len(got) != 0 {
		t.Fatalf("report must not be called when describe does not declare it: %v", got)
	}
}

// Paging: hasMore reads on with the server's nextCursor.
func TestReconcileFollowsPagesByTheServersCursor1962(t *testing.T) {
	path, log := fakeAdapter1962(t, describeAll1962, "")
	var n atomic.Int32
	r := map[string]string{"IntakeExternalIdChanges": feed1962(true, "page-2", change1962("RELEASED", itemR1, "c1", "t1", "AT1.1", "", ""))}
	t.Setenv(adapterEnvForTest, "")
	calls, _, _, err := runFindHook1961(t, r, func(op string, restage func(string)) {
		if op == "IntakeExternalIdChanges" && n.Add(1) == 2 {
			restage(feed1962(false, "", change1962("RELEASED", itemR2, "c2", "t2", "AT1.2", "", "")))
		}
	}, "--adapter", path)
	if err != nil {
		t.Fatal(err)
	}
	fc := feedCalls1962(calls)
	if len(fc) != 2 || fc[1].Vars["after"] != "page-2" {
		t.Fatalf("page 2 must be read after the server's cursor: %v", fc)
	}
	if got := adapterLog1961(t, log, "untrack"); len(got) != 2 {
		t.Fatalf("both pages' releases reach the adapter: %v", got)
	}
}

// A failed adapter call stops the pass AT that change: the cursor stays after
// the last answered one so the next run retries it. The list still runs, and
// the run ends non-zero.
func TestReconcileAdapterFailureHoldsTheCursorAndStillLists1962(t *testing.T) {
	path, log := fakeAdapter1962(t, describeAll1962, `echo boom >&2; exit 3`)
	feed := feed1962(false, "",
		change1962("REASSIGNED", itemR1, "c1", "t1", "AT1.1", "wkr9", "Gus"),
		change1962("RELEASED", itemR2, "c2", "t2", "AT1.2", "", ""),
		change1962("RELEASED", itemR3, "c3", "t3", "AT1.3", "", ""),
	)
	calls, out, _, err := run1962(t, path, feed, "--json")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("a failed write-back must end the run non-zero: %v", err)
	}
	if got := adapterLog1961(t, log, "untrack"); len(got) != 1 {
		t.Fatalf("the pass stops at the failing change, not past it: %d untrack calls", len(got))
	}
	if len(adapterLog1961(t, log, "list-untracked")) == 0 {
		t.Fatal("the list does not depend on the write-back and still runs")
	}
	if !strings.Contains(out, `"error"`) || strings.Contains(out, `"error": null`) {
		t.Fatalf("--json names why the pass stopped: %s", out)
	}
	_ = calls
}

// A server that predates the feed refuses by name; the run does not proceed
// pretending the write-back happened.
func TestReconcileServerWithoutTheFeedIsUsage1962(t *testing.T) {
	path, _ := fakeAdapter1962(t, describeAll1962, "")
	bad := `{"errors":[{"message":"Cannot query field \"agentTaskExternalIdChanges\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, _, _, err := run1962(t, path, bad)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "predates hadron-server#4084") {
		t.Fatalf("want usage naming #4084, got %v", err)
	}
}
