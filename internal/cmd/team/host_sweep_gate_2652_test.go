package team

import (
	"testing"
	"time"
)

// cli#2652: `team host up --apply` refuses an App whose server sweep mode is not
// APPLY; the one-minute sweep timer must not, since an OFF App is its normal
// quiet pass. This pins that the timer's options carry the exemption.
func TestSweepPassIsExemptFromTheApplySweepModeRefusal2652(t *testing.T) {
	o := sweepHostUpOptions("orca1", "/w", "orca", "/c.yaml", false, time.Now())
	if !o.apply || !o.sweepRun || !o.lockHeld {
		t.Fatalf("sweep options = %+v, want apply, sweepRun and lockHeld", o)
	}
}
