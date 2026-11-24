package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/intake"
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
		Reconcile     map[string]any
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
	ids, _ := opsOf1961(calls, "IntakeTasksByExternalIds")[0].Vars["externalIds"].([]any)
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

	// The runner asks for at least 20 a page, so three items never page
	// above. Walk the sample at one item a page through the runner itself:
	// its cursor must advance, end, and never repeat.
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
