package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
	"github.com/hadron-memory/hadron-cli/pkg/intakecontract"
)

// cli#3062 (cor:agt:120:04): a team with no intake adapter gets a user-facing
// how-to page that the find-untracked help names, and the page's sample
// adapter is real working code: it is extracted from the page and run through
// the compiled command tree, so the page cannot drift from what the runner
// accepts.

const howToPath3062 = "../../docs/how-to/write-an-intake-adapter.md"
const howToRef3062 = "docs/how-to/write-an-intake-adapter.md"

func readHowTo3062(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(howToPath3062)
	if err != nil {
		t.Fatalf("the intake-adapter how-to page must exist at %s: %v", howToRef3062, err)
	}
	return string(b)
}

func TestFindUntrackedHelpNamesTheHowToPage3062(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "task", "find-untracked", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(help, howToRef3062) {
		t.Errorf("the help must name the how-to page %s", howToRef3062)
	}
	if strings.Contains(help, "docs/plans/1961-intake-adapter-runner.md") {
		t.Errorf("the help must point users at the how-to page, not the reviewers' plan doc")
	}
	for _, keep := range []string{"Ask whoever runs your intake", "hadron agentic-usage"} {
		if !strings.Contains(help, keep) {
			t.Errorf("the help must keep %q", keep)
		}
	}

	page := readHowTo3062(t)
	for _, call := range []string{"describe", "list-untracked", "mark-tracked", "untrack", "report", "get"} {
		if !strings.Contains(page, "`"+call+"`") {
			t.Errorf("the page must name the call `%s`", call)
		}
	}
}

func TestAgenticUsagePointsAtTheHowToPage3062(t *testing.T) {
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "## App properties, task externalId and find-untracked")
	if i < 0 {
		t.Fatal("the find-untracked section is gone")
	}
	section := s[i:]
	if j := strings.Index(section[3:], "\n## "); j >= 0 {
		section = section[:j+3]
	}
	if !strings.Contains(section, howToRef3062) {
		t.Errorf("the find-untracked section of agentic-usage must point at %s", howToRef3062)
	}
}

// sampleBlock3062 returns the fenced block that follows the marker comment
// `<!-- sample: <name> -->` on the page.
func sampleBlock3062(t *testing.T, page, name string) string {
	t.Helper()
	re := regexp.MustCompile("(?s)<!-- sample: " + regexp.QuoteMeta(name) + " -->\\s*```[a-z]*\\n(.*?)\\n```")
	m := re.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page carries no sample block marked %q", name)
	}
	return m[1]
}

// buildSample3062 extracts the sample adapter and its items file from the page,
// builds the adapter, and puts the items file beside it.
func buildSample3062(t *testing.T) string {
	t.Helper()
	page := readHowTo3062(t)
	src := sampleBlock3062(t, page, "main.go")
	items := sampleBlock3062(t, page, "items.json")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "intake-adapter")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "main.go")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the page's sample adapter does not build: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "items.json"), []byte(items), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestHowToSampleAdapterLists3062(t *testing.T) {
	bin := buildSample3062(t)
	held := "https://tracker.example/acme/app/issues/2"
	calls, out, stderr, err := run1961(t, bin, map[string]string{"IntakeTasksByExternalIds": held1961(held)}, "--json")
	if err != nil {
		t.Fatalf("the sample must list through the real runner: %v\n%s", err, stderr)
	}
	var dto struct {
		Adapter       string
		Items         []struct{ ExternalId, Title string }
		Complete      bool
		StoppedReason *string
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if dto.Adapter != "sample" || !dto.Complete || dto.StoppedReason != nil {
		t.Fatalf("describe and an exhausted list: got %+v", dto)
	}
	if len(dto.Items) != 2 || dto.Items[0].ExternalId != "https://tracker.example/acme/app/issues/1" || dto.Items[1].ExternalId != "https://tracker.example/acme/app/issues/3" {
		t.Fatalf("the sample's items, less the one Hadron holds: %+v", dto.Items)
	}
	checks := opsOf1961(calls, "IntakeTasksByExternalIds")
	if len(checks) != 1 {
		t.Fatalf("one batch check against the App's tasks, got %d", len(checks))
	}
	ids, _ := checks[0].Vars["externalIds"].([]any)
	if len(ids) != 3 {
		t.Fatalf("every item the sample lists is checked against the App: %v", ids)
	}

	// --for asks for one worker's items; the sample filters on its own field.
	_, out, stderr, err = run1961(t, bin, map[string]string{"IntakeTasksByExternalIds": held1961()}, "--json", "--for", "Marco")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	dto.Items = nil
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 || dto.Items[0].ExternalId != "https://tracker.example/acme/app/issues/1" {
		t.Fatalf("--for Marco lists only Marco's item: %+v", dto.Items)
	}

}

// The command asks an adapter for at least 20 items a page (pageHint), so a
// command-tree run over three items never pages. This test drives the runner
// directly with a page size of 1: the sample's cursor must advance, end, and
// never repeat (the runner refuses a repeat).
func TestHowToSampleAdapterPages3062(t *testing.T) {
	bin := buildSample3062(t)
	r := &intake.Runner{Path: bin, Timeout: 10 * time.Second}
	var got []string
	pages := 0
	if err := r.Walk(context.Background(), "", 1, func(p intake.Page) (bool, error) {
		pages++
		for _, it := range p.Items {
			got = append(got, it.ExternalID)
		}
		return false, nil
	}); err != nil {
		t.Fatalf("the sample's cursor must page through the runner: %v", err)
	}
	if pages != 3 || len(got) != 3 || got[2] != "https://tracker.example/acme/app/issues/3" {
		t.Fatalf("three pages of one item each: pages=%d items=%v", pages, got)
	}
}

// The behavioural claims the page makes about the runner (exit 15 on a wrong
// describe version, exit 2 for a describe missing a call, exit 1 with
// marked:false on a mark-tracked refusal, the 60 s timeout, the HADRON_*
// environment scrub, the repeated-cursor refusal, the reconcile cursor) are
// not pinned here: they are pinned where the runner lives, in
// internal/intake/intake_test.go, find_untracked_adapter_1961_test.go and
// find_untracked_reconcile_1962_test.go. TestRunnerEnforcesTheExportedRules
// ties the runner to pkg/intakecontract. This file pins the page: that its
// sample builds and runs, and that its stated numbers are the contract's.

// The page states the runner's bounds and exit codes as numbers. They are
// the published package's constants (pkg/intakecontract, which the page
// names for Go adapter authors): if a constant changes, the page must too.
func TestHowToPageNumbersAreThePublishedContract3062(t *testing.T) {
	page := strings.Join(strings.Fields(readHowTo3062(t)), " ")
	for want, ok := range map[string]bool{
		"`\"contractVersion\": 1`":                               intakecontract.ContractVersion == 1,
		"stdout is capped at 1 MiB per call":                     intakecontract.MaxOutputBytes == 1<<20,
		"A page holds at most 100 items":                         intakecontract.MaxItemsPerPage == 100,
		"a list at most 50 pages":                                intakecontract.MaxPages == 50,
		"An external id is at most 512 characters":               intakecontract.ServerExternalIDMax == 512,
		"the adapter's name at most 1024 bytes":                  intakecontract.MaxTextLen == 1024,
		"cursor at most 4096 bytes":                              intakecontract.MaxCursorLen == 4096,
		"with exit status **20**":                                intakecontract.ExitNotMine == 20,
		"github.com/hadron-memory/hadron-cli/pkg/intakecontract": true,
	} {
		stated := strings.Contains(page, want)
		if !stated {
			t.Errorf("the page must state %q", want)
		}
		if stated && !ok {
			t.Errorf("the page states %q but pkg/intakecontract disagrees", want)
		}
	}
}

func TestHowToSampleAdapterTracks3062(t *testing.T) {
	bin := buildSample3062(t)
	item := "https://tracker.example/acme/app/issues/1"
	r := map[string]string{
		"IntakeTasksByExternalIds": held1961(),
		"IntakeClaimTask":          claimed1961("t1", item, "wkr1", "Marco", "https://portal.test/app/apps/a1/tasks/t1", "STARTED"),
	}
	calls, out, stderr, err := run1961(t, bin, r, "--track", "--json")
	if err != nil {
		t.Fatalf("the sample must take a --track claim through the real runner: %v\n%s", err, stderr)
	}
	var dto struct {
		Tasks []struct {
			ExternalId string
			Marked     bool
			MarkError  *string
		}
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(dto.Tasks) != 1 || dto.Tasks[0].ExternalId != item || !dto.Tasks[0].Marked || dto.Tasks[0].MarkError != nil {
		t.Fatalf("one claim, marked by the sample's mark-tracked: %+v", dto)
	}
	if n := len(opsOf1961(calls, "IntakeClaimTask")); n != 1 {
		t.Fatalf("one create is the claim: %d", n)
	}
}

// The sample refuses a request that speaks another contract version, on every
// call: exit 2 and the reason on stderr. Deleting that check from the page must
// fail here.
func TestHowToSampleAdapterRefusesOtherContractVersion3062(t *testing.T) {
	bin := buildSample3062(t)
	for _, call := range []string{"describe", "list-untracked", "mark-tracked"} {
		t.Run(call, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, call)
			cmd.Stdin = strings.NewReader(`{"contractVersion": 2, "externalId": "x"}`)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 2 {
				t.Fatalf("a contractVersion 2 request must exit 2, got %v", err)
			}
			if !strings.Contains(stderr.String(), "speaks contract version 1") {
				t.Fatalf("stderr must say the adapter speaks contract version 1: %q", stderr.String())
			}
		})
	}
}

// The page says what `limit` the CLI sends to list-untracked: the user's
// --limit raised to 20, so --limit 1 and no --limit both send 20 and --limit 50
// sends 50 (above 100 the flag is refused before any call, pinned in
// TestAdapterFlagsRefusedLocallyWithZeroRequests1961's --limit 101 row).
func TestHowToPageLimitWordingMatchesWhatTheCLISends3062(t *testing.T) {
	for argv, want := range map[string]float64{"": 20, "--limit 1": 20, "--limit 20": 20, "--limit 50": 50, "--limit 100": 100} {
		path, log := fakeAdapter1961(t, "", `echo '{"items":[]}'`, "")
		args := []string{"--json"}
		if argv != "" {
			args = append(args, strings.Fields(argv)...)
		}
		if _, _, stderr, err := run1961(t, path, map[string]string{"IntakeTasksByExternalIds": held1961()}, args...); err != nil {
			t.Fatalf("%q: %v\n%s", argv, err, stderr)
		}
		reqs := adapterLog1961(t, log, "list-untracked")
		if len(reqs) != 1 {
			t.Fatalf("%q: one list-untracked call, got %d", argv, len(reqs))
		}
		var req struct{ Limit float64 }
		if err := json.Unmarshal([]byte(reqs[0]), &req); err != nil || req.Limit != want {
			t.Errorf("%q must send limit %v, got %v (%v)", argv, want, req.Limit, err)
		}
	}
	page := strings.Join(strings.Fields(readHowTo3062(t)), " ")
	for _, want := range []string{"raised to 20 if it is lower", "or a run with no `--limit` at all, asks for 20", "refuses a `--limit` above 100"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page must say %q", want)
		}
	}
}

// The page's two exit-2 messages: an adapter that does not answer a call this
// run needs, and --track with no one to hold the claim.
func TestHowToPageExitTwoMessages3062(t *testing.T) {
	// The page says "exits 2" in prose; the assertions below use exitcode.Usage.
	// Tie the two, so a changed constant fails here instead of leaving the page stale.
	if exitcode.Usage != 2 {
		t.Fatalf("the page says exit 2, but exitcode.Usage is %d: update the page", exitcode.Usage)
	}
	describe := `{"name":"fake","contractVersion":1,"calls":["describe","list-untracked"],"filters":[],"order":"x"}`
	path, _ := fakeAdapter1961(t, describe, `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	_, _, _, err := run1961(t, path, map[string]string{"IntakeTasksByExternalIds": held1961()}, "--track")
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "does not answer mark-tracked") {
		t.Fatalf("a describe without mark-tracked, on --track: exit 2 naming the call, got %d %v", exitCodeFor(err), err)
	}

	// No worktree binding and no --for/--assignee: nobody to hold the claim.
	path, _ = fakeAdapter1961(t, "", `echo '{"items":[`+item1961(itemA1961, "a")+`]}'`, "")
	t.Setenv(adapterEnvForTest, "")
	srv, _ := attnServer(t, withEmptyChanges1962(map[string]string{"IntakeTasksByExternalIds": held1961()}))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "find-untracked", "--adapter", path, "--track"})
	err = root.Execute()
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "pass --for <worker> or --assignee <party>") {
		t.Fatalf("--track with no one to hold the claim: exit 2 asking for --for or --assignee, got %d %v", exitCodeFor(err), err)
	}
	page := strings.Join(strings.Fields(readHowTo3062(t)), " ")
	for _, want := range []string{"says which call the adapter does not answer", "asks you to pass `--for` or `--assignee`"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page must say %q", want)
		}
	}
}
