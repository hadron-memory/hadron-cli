package team

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1660 ask 1(b) at the command: a corrupt record is named by file,
// the run exits 7 (Unavailable), and nothing is counted as reported.
func TestReportStopsNamesACorruptRecordAndExitsSeven_1660(t *testing.T) {
	cfg := hostConfig(t, "")
	dir := delivery.StopOutboxDir(filepath.Dir(cfg))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 32) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := reportStopsRun(t, "--config", cfg, "--reporter-worker", "Hugo")
	if err == nil || exitcode.FromError(err) != exitcode.Unavailable {
		t.Fatalf("err %v, exit %d, want %d", err, exitcode.FromError(err), exitcode.Unavailable)
	}
	if !strings.HasPrefix(out, "failed "+name) {
		t.Fatalf("the corrupt file is not named: %q", out)
	}
	dry, err := reportStopsRun(t, "--config", cfg, "--dry-run")
	if err != nil || !strings.Contains(dry, "unreadable records: 1") {
		t.Fatalf("dry run %q err %v", dry, err)
	}
}

// Retention at the command: --retain-reported is a flag, and the run says
// what it removed.
func TestReportStopsHelpDocumentsRetention_1660(t *testing.T) {
	long := newCmdTeamHostReportStops(nil).Long
	for _, want := range []string{"--retain-reported", "720h", "never removed"} {
		if !strings.Contains(long, want) {
			t.Fatalf("help lacks %q", want)
		}
	}
	if newCmdTeamHostReportStops(nil).Flags().Lookup("retain-reported") == nil {
		t.Fatal("no --retain-reported flag")
	}
}

// Review P3 (Anne): the flag reaches the reporter and the run says what it removed.
func TestReportStopsRetainReportedRemovesAndSays_1660(t *testing.T) {
	cfg := hostConfig(t, "")
	dir := delivery.StopOutboxDir(filepath.Dir(cfg))
	box := &delivery.StopOutbox{Dir: dir}
	if _, err := box.Drop(delivery.StopDrop{EventID: "old", Worker: "Gil", HeldAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("old"))
	base := hex.EncodeToString(h[:])[:32]
	marker := `{"eventId":"old","seq":3,"reportedAt":"` + time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(dir, base+".reported"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := reportStopsRun(t, "--config", cfg, "--reporter-worker", "Hugo", "--retain-reported", "1h")
	if err != nil || !strings.Contains(out, "removed 1 reported record(s) older than 1h0m0s") {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, base+".json")); !os.IsNotExist(err) {
		t.Fatal("the record was not removed")
	}
}
