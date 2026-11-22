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
// mode is never read, while the same App refused for a manual --apply.
func TestRunHostUpReadsTheSweepModeOnlyForAManualApply2652(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       hostUpOptions
		wantReads  int
		wantRefuse bool
	}{
		{"sweep timer", sweepHostUpOptions("h1", "", "/nonexistent/fixture-orca", "", true, time.Now()), 0, false},
		{"manual apply", hostUpOptions{host: "h1", apply: true, readyChecked: true, lockHeld: true, orcaBin: "/nonexistent/fixture-orca", timeout: time.Second}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			f, _, dir := sweepFixture2153(t, func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Op string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				switch q.Op {
				case "GetAppSweepMode":
					reads++
					fmt.Fprint(w, `{"data":{"app":{"sweepMode":"OFF"}}}`)
				case "HostDesiredState":
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
			refused := err != nil && exitcode.FromError(err) == exitcode.Conflict && strings.Contains(err.Error(), "sweep mode is OFF")
			if refused != tc.wantRefuse {
				t.Fatalf("refused = %v, want %v (err %v)", refused, tc.wantRefuse, err)
			}
		})
	}
}
