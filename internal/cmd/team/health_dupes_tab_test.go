package team

// #2037 N3: tabBareNow — the read-adjacent recheck before a close — has a
// branch for every way a tab can stop being provably bare between the sweep's
// first listing and the close. These pin each branch directly, through a
// sweep client over a fake runner (no processes, no files): the end-to-end
// recheck guards (a screen that flips, a pane that appears, a failing re-list,
// a tab that leaves) live in health_dupes_2027_test.go beside the world.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// tabBareBody is one `terminal read --screen --json` answer.
func tabBareBody(tailLine string) string {
	return fmt.Sprintf(`{"ok":true,"result":{"terminal":{"source":"screen","tail":[%q],"draft":null}}}`, tailLine)
}

const tabBarePrompt = "orca@orca1:~$" // matches the classifier's shell-prompt rule
const tabBareBusy = "  ok  internal/cmd/team"

// tabBareList is one `terminal list --json` answer: tabs of terminals, all in
// worktree.
func tabBareList(t *testing.T, worktree string, tabs map[string][]string) string {
	t.Helper()
	rows := ""
	for tab, handles := range tabs {
		for _, h := range handles {
			if rows != "" {
				rows += ","
			}
			rows += fmt.Sprintf(`{"handle":%q,"worktreePath":%q,"tabId":%q,"connected":true,"writable":true}`, h, worktree, tab)
		}
	}
	return `{"ok":true,"result":{"terminals":[` + rows + `]}}`
}

// tabBareClient wraps a fake runner answering list and read from the given
// bodies; failList fails the list call outright.
func tabBareClient(list string, screens map[string]string, failList bool) *orcaterm.Client {
	return orcaterm.NewSweepClient(func(_ context.Context, argv []string) orcaterm.Result {
		switch argv[1] {
		case "list":
			if failList {
				return orcaterm.Result{Code: 1, Stderr: "orca unreachable"}
			}
			return orcaterm.Result{Stdout: list}
		case "read":
			body, ok := screens[argv[3]]
			if !ok {
				return orcaterm.Result{Code: 1, Stderr: "no such terminal"}
			}
			return orcaterm.Result{Stdout: body}
		}
		return orcaterm.Result{Code: 1, Stderr: "refused"}
	})
}

func TestTabBareNowAnswersALiveHandleForABareTab(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient(
		tabBareList(t, wt, map[string][]string{"tab-bare": {"term_bare"}}),
		map[string]string{"term_bare": tabBareBody(tabBarePrompt)},
		false,
	)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "tab tab-bare")
	if !bare || handle != "term_bare" || decline != "" {
		t.Fatalf("a bare tab must answer its fresh handle: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesATabThatTurned(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient(
		tabBareList(t, wt, map[string][]string{"tab-bare": {"term_bare"}}),
		map[string]string{"term_bare": tabBareBody(tabBareBusy)},
		false,
	)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "tab tab-bare")
	if bare || handle != "" || decline != "close-not-bare" {
		t.Fatalf("a turned tab must decline as close-not-bare: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesATabWithOneUnreadablePane(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient(
		tabBareList(t, wt, map[string][]string{"tab-bare": {"term_bare", "term_split"}}),
		map[string]string{
			"term_bare":  tabBareBody(tabBarePrompt),
			"term_split": "", // an unreadable pane is no evidence of bare
		},
		false,
	)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "tab tab-bare")
	if bare || decline != "close-not-bare" {
		t.Fatalf("a pane the recheck cannot read must decline: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesAGoneTab(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient(
		tabBareList(t, wt, map[string][]string{"tab-other": {"term_other"}}),
		map[string]string{"term_other": tabBareBody(tabBarePrompt)},
		false,
	)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "tab tab-bare")
	if bare || handle != "" || decline != "close-gone" {
		t.Fatalf("a tab missing from the fresh listing must decline as close-gone: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesWhenTheRecheckCannotList(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient("", nil, true)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "tab tab-bare")
	if bare || handle != "" || decline != "close-recheck-failed" {
		t.Fatalf("a failing re-list must decline as close-recheck-failed: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesWhenTheWorktreeCannotBeResolved(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "not-there")
	client := tabBareClient("", nil, false)
	handle, bare, decline := tabBareNow(context.Background(), client, gone, "tab tab-bare")
	if bare || handle != "" || decline != "close-recheck-failed" {
		t.Fatalf("an unresolvable worktree must decline as close-recheck-failed: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}

func TestTabBareNowDeclinesASubjectThatIsNotATab(t *testing.T) {
	wt := t.TempDir()
	client := tabBareClient("", nil, false)
	handle, bare, decline := tabBareNow(context.Background(), client, wt, "agent 7")
	if bare || handle != "" || decline != "close-recheck-failed" {
		t.Fatalf("a non-tab subject must decline, never close by it: handle=%q bare=%v decline=%q", handle, bare, decline)
	}
}
