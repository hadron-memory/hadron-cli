package team

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2617 P3-4: pin the outputs and the file the `team host` pages promise, so
// the page and the command cannot drift apart unnoticed.

func hostRunLong2617(t *testing.T) string {
	t.Helper()
	return newCmdTeamHostRun(&cmdutil.Factory{}).Long
}

// The two event lines the run page shows as an example are exactly what the
// event printer writes for those events (it prints type before operation id).
func TestHostRunHelpEventLinesMatchThePrinter2617(t *testing.T) {
	io, out, _ := output.Test()
	emit := hostEventPrinter(&cmdutil.Factory{IOStreams: io})
	at := time.Date(2026, 11, 20, 9, 12, 1, 0, time.UTC)
	emit(hostpoller.Event{Time: at, Kind: "collected", Operation: "01H…", Type: "worktree.create"})
	emit(hostpoller.Event{Time: at, Kind: "reported", Operation: "01H…", Type: "worktree.create", Outcome: hostpoller.Succeeded, Detail: "hadron-cli/jonas-2"})
	long := hostRunLong2617(t)
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if !strings.Contains(long, "  "+line+"\n") {
			t.Errorf("the run page's example events do not show the printed line %q", line)
		}
	}
}

// The minimal host.yaml of the run page's QUICKSTART passes the real parser
// and validator.
func TestHostRunQuickstartYAMLLoads2617(t *testing.T) {
	long := hostRunLong2617(t)
	i := strings.Index(long, "3. Write host.yaml")
	j := strings.Index(long, "4. Try one poll")
	if i < 0 || j < i {
		t.Fatal("the QUICKSTART steps moved; update this pin")
	}
	var yaml []string
	for _, l := range strings.Split(long[i:j], "\n")[1:] {
		if strings.HasPrefix(l, "   ") {
			yaml = append(yaml, strings.TrimPrefix(l, "   "))
		}
	}
	doc := strings.Join(yaml, "\n")
	doc = strings.ReplaceAll(doc, "hrn:app:<root>:<slug>", "hrn:app:acme.com:eng-team")
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hdr_host_fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc = strings.ReplaceAll(doc, "/etc/hadron/host.key", key)
	doc = strings.ReplaceAll(doc, "/srv/worktrees", dir)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(doc+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := hostpoller.LoadConfig(path)
	if err != nil {
		t.Fatalf("the QUICKSTART host.yaml does not load:\n%s\n%v", doc, err)
	}
	if len(cfg.Workers) != 1 {
		t.Errorf("the QUICKSTART host.yaml should define one worker, got %d", len(cfg.Workers))
	}
}

// The availability success line the page quotes is the line the command
// prints, for the set and the clear case alike.
func TestHostAvailabilityHelpQuotesThePrintedLine2617(t *testing.T) {
	long := newCmdTeamHostAvailability(&cmdutil.Factory{}).Long
	flat := strings.Join(strings.Fields(long), " ")
	for _, line := range []string{
		hostAvailabilityLine("Iris", "unavailable", "HOST"),
		hostAvailabilityLine("Iris", "available", "HOST"),
	} {
		if !strings.Contains(flat, line) {
			t.Errorf("the availability page does not show the printed line %s", line)
		}
	}
}

// The sweep-runs page names the columns the table prints, in order.
func TestSweepRunsHelpNamesThePrintedColumns2617(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"hostSweepRuns":[{"id":"1","hostId":"orca1","runId":"run-1","startedAt":"2026-11-20T09:00:00Z","endedAt":"2026-11-20T09:00:05Z","mode":"APPLY","planned":3,"applied":2,"skipped":1,"failed":0,"recordedAt":"2026-11-20T09:00:06Z","lines":[]}]}}`)
	}))
	defer srv.Close()
	io, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL, AppFlag: "hrn:app:example.com:team"}
	t.Setenv("HADRON_TOKEN", "reader-key")
	c := newCmdTeamHostSweepRuns(f)
	c.SetArgs([]string{"--host", "orca1"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	var header []string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "RUN") {
			header = strings.Fields(l)
		}
	}
	if len(header) == 0 {
		t.Fatalf("no table header in the output:\n%s", out.String())
	}
	flat := strings.Join(strings.Fields(c.Long), " ")
	if want := "columns " + strings.Join(header, ", ") + ")"; !strings.Contains(flat, want) {
		t.Errorf("the page does not name the printed columns %q", want)
	}
}
