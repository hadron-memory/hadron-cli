package team

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2652: `team host up --apply` refuses an App whose server sweep mode is not
// APPLY; the one-minute sweep timer must not, since an OFF App is its normal
// quiet pass. This pins that the timer's options carry the exemption.
func TestSweepPassIsExemptFromTheApplySweepModeRefusal2652(t *testing.T) {
	o := sweepHostUpOptions("orca1", "/w", "orca", "/c.yaml", false, time.Now())
	if !o.apply || !o.skipSweepModeGate || !o.lockHeld {
		t.Fatalf("sweep options = %+v, want apply, skipSweepModeGate and lockHeld", o)
	}
}

// The timer's run, through runHostUp itself: an OFF App is a quiet pass and the
// mode is never read, while the same App refused for a manual --apply. The pass
// is also proved to get PAST the gate's position (the desired state is then
// read, cli#3020), and a refused apply never gets that far; an App that does
// not report its sweep mode is refused with its own message (fail closed,
// cli#3020).
func TestRunHostUpReadsTheSweepModeOnlyForAManualApply2652(t *testing.T) {
	manual := hostUpOptions{host: "h1", apply: true, readyChecked: true, lockHeld: true, orcaBin: "/nonexistent/fixture-orca", timeout: time.Second}
	for _, tc := range []struct {
		name        string
		opts        hostUpOptions
		mode        string // the App's sweepMode as the server answers it
		wantReads   int
		wantDesired int    // reads of the desired state: only a run that got past the gate makes one
		wantRefusal string // the refusal's message fragment; "" means no refusal
	}{
		{"sweep timer", sweepHostUpOptions("h1", "", "/nonexistent/fixture-orca", "", true, time.Now()), "OFF", 0, 1, ""},
		{"manual apply", manual, "OFF", 1, 0, "sweep mode is OFF"},
		{"manual apply, mode not reported", manual, "", 1, 0, "the App's sweep mode is not reported, so --apply is refused and nothing issued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, desired := 0, 0
			f, _, dir := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Op string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				switch q.Op {
				case "GetAppSweepMode":
					reads++
					fmt.Fprintf(w, `{"data":{"app":{"sweepMode":%q}}}`, tc.mode)
				case "HostDesiredState":
					desired++
					fmt.Fprint(w, `{"data":{"hostDesiredState":{"hostId":"h1","sweepMode":"OFF","workers":[]}}}`)
				case "TeamHostSetup":
					fmt.Fprint(w, `{"data":{"hostSetup":{"source":"HOST","assignments":[]}}}`)
				case "HostRegistrations":
					fmt.Fprint(w, `{"data":{"hostRegistrations":[]}}`)
				default:
					fmt.Fprint(w, `{"data":null}`)
				}
			})
			f.AppFlag = sweepApp2153
			tc.opts.root = dir
			c := newCmdTeamHostSweep(f)
			c.SetContext(context.Background())
			err := runHostUp(c, f, tc.opts)
			if reads != tc.wantReads {
				t.Fatalf("mode reads = %d, want %d (err %v)", reads, tc.wantReads, err)
			}
			if desired != tc.wantDesired {
				t.Fatalf("desired-state reads = %d, want %d: the run %s the gate's position (err %v)", desired, tc.wantDesired, map[bool]string{true: "did not get past", false: "got past"}[tc.wantDesired > 0], err)
			}
			refused := err != nil && exitcode.FromError(err) == exitcode.Conflict && tc.wantRefusal != "" && strings.Contains(err.Error(), tc.wantRefusal)
			if refused != (tc.wantRefusal != "") {
				t.Fatalf("refused = %v, want refusal %q (err %v)", refused, tc.wantRefusal, err)
			}
			if tc.wantRefusal == "" && err != nil && exitcode.FromError(err) == exitcode.Conflict {
				t.Fatalf("the quiet pass must not be refused: %v", err)
			}
		})
	}
}
