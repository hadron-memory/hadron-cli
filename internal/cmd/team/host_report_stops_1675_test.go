package team

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportStopsCachedDryRun1675(t *testing.T) {
	cfg := hostConfig(t, "")
	dir := delivery.StopOutboxDir(filepath.Dir(cfg))
	box := &delivery.StopOutbox{Dir: dir}
	if _, err := box.Drop(delivery.StopDrop{EventID: "event", Worker: "worker\x1b\nforged", HeldAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("event"))
	base := hex.EncodeToString(h[:])[:32]
	write := func(suffix, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, base+suffix), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".sending", `{"acceptedSeq":17}`)
	write(".lastfail", `{"v":1,"eventId":"event","seq":17,"reason":"readback-mismatch","field":"author,body","lastObservedAt":"2026-11-06T15:00:00.123Z"}`)
	out, err := reportStopsRun(t, "--config", cfg, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	expected := "needs a person? event worker  forged #17: last run observed readback-mismatch (author, body) at 2026-11-06T15:00:00.123Z\nas last observed by report-stops, not a current check or a final diagnosis\n"
	if !strings.Contains(out, expected) || strings.Contains(out, "\x1b") {
		t.Fatalf("human %q", out)
	}
	out, err = reportStopsRun(t, "--config", cfg, "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var status delivery.PendingReport
	if err = json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.NeedsPerson != 1 || status.RecordsTotal != 1 || status.RecordsTruncated || len(status.Records) != 1 || status.Records[0].LastObservedAt.Format(time.RFC3339Nano) != "2026-11-06T15:00:00.123Z" {
		t.Fatalf("json %s", out)
	}
	if _, err = os.Stat(filepath.Join(dir, ".reporter.lock")); !os.IsNotExist(err) {
		t.Fatal("dryrun locked")
	}
}

type singleWrite1675 struct {
	bytes.Buffer
	writes int
}

func (w *singleWrite1675) Write(p []byte) (int, error) { w.writes++; return w.Buffer.Write(p) }
func TestReportStopsDryRunOneObject1675(t *testing.T) {
	cfg := hostConfig(t, "")
	io, _, _ := output.Test()
	w := &singleWrite1675{}
	io.Out = w
	f := &cmdutil.Factory{IOStreams: io, JSON: true}
	cmd := newCmdTeamHostReportStops(f)
	cmd.SetArgs([]string{"--config", cfg, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if w.writes != 1 {
		t.Fatalf("writes%d", w.writes)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 6 || string(raw["records"]) != "[]" || string(raw["needsPerson"]) != "0" || string(raw["recordsTruncated"]) != "false" {
		t.Fatalf("shape %s", w.Bytes())
	}
}
