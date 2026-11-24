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
// read, cli#3020), and a refused apply never gets that far. A mode the server
// does not report fails closed with its own message whatever shape "not
// reported" takes on the wire, however the client decodes it: an empty string,
// null, a missing field or an unknown value (refused naming it); an App the
// read cannot find is its own refusal (cli#3020).
func TestRunHostUpReadsTheSweepModeOnlyForAManualApply2652(t *testing.T) {
	manual := hostUpOptions{host: "h1", apply: true, readyChecked: true, lockHeld: true, orcaBin: "/nonexistent/fixture-orca", timeout: time.Second}
	const notReported = "sweep mode is not reported, so --apply is refused and nothing issued"
	for _, tc := range []struct {
		name        string
		opts        hostUpOptions
		app         string // the GetAppSweepMode answer's "app" value, raw
		wantReads   int
		wantDesired int    // reads of the desired state: only a run that got past the gate makes one
		wantExit    int    // the refusal's exit code; 0 means no refusal
		wantRefusal string // a fragment of the refusal's message
	}{
		{"sweep timer", sweepHostUpOptions("h1", "", "/nonexistent/fixture-orca", "", true, time.Now()), `{"sweepMode":"OFF"}`, 0, 1, 0, ""},
		{"manual apply", manual, `{"sweepMode":"OFF"}`, 1, 0, exitcode.Conflict, "sweep mode is OFF"},
		{"manual apply, mode empty", manual, `{"sweepMode":""}`, 1, 0, exitcode.Conflict, notReported},
		{"manual apply, mode null", manual, `{"sweepMode":null}`, 1, 0, exitcode.Conflict, notReported},
		{"manual apply, mode field missing", manual, `{}`, 1, 0, exitcode.Conflict, notReported},
		{"manual apply, unknown mode", manual, `{"sweepMode":"BOGUS"}`, 1, 0, exitcode.Conflict, "sweep mode is BOGUS, so --apply is refused"},
		{"manual apply, App not found", manual, `null`, 1, 0, exitcode.NotFound, "could not read the App's sweep mode (App not found)"},
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
					fmt.Fprintf(w, `{"data":{"app":%s}}`, tc.app)
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
				t.Fatalf("desired-state reads = %d, want %d (err %v)", desired, tc.wantDesired, err)
			}
			if tc.wantExit == 0 {
				// The quiet pass reached the desired-state read above, which is what proves the gate was
				// passed. Later steps may still fail (the orca binary is a nonexistent fixture), so only a
				// refusal-class error is ruled out here.
				if got := exitcode.FromError(err); err != nil && (got == exitcode.Conflict || got == exitcode.NotFound) {
					t.Fatalf("the quiet pass must not be refused: exit %d: %v", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want a refusal (exit %d, %q), got none", tc.wantExit, tc.wantRefusal)
			}
			if got := exitcode.FromError(err); got != tc.wantExit {
				t.Fatalf("refusal exit = %d, want %d (err %v)", got, tc.wantExit, err)
			}
			if !strings.Contains(err.Error(), tc.wantRefusal) {
				t.Fatalf("refusal message %q does not contain %q", err.Error(), tc.wantRefusal)
			}
		})
	}
}
