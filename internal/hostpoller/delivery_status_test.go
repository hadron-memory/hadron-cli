package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// cli#906 row 3: every delivery step reports how its read went, so status
// can tell a quiet poller from a stopped one.
func TestDeliveryStepReportsItsRead(t *testing.T) {
	for want, readErr := range map[string]error{"read": nil, "read-failed": errors.New("boom"), "off-by-scope": ErrDeliveryScope} {
		t.Run(want, func(t *testing.T) {
			p, _, _ := testPoller(t)
			src := &fakeBatches{err: readErr}
			p.EnableDelivery(src, &fakeDeliverer{})
			var got []string
			p.DeliveryStep = func(_ time.Time, read string) { got = append(got, read) }
			wantAll := []string{want}
			if want == "off-by-scope" {
				// One scope answer is not believed (cli#2663): it reports
				// read-failed; the second consecutive one is the verdict.
				p.deliverStep(context.Background())
				wantAll = []string{"read-failed", want}
			}
			p.deliverStep(context.Background())
			if fmt.Sprint(got) != fmt.Sprint(wantAll) {
				t.Fatalf("reported %v, want %v", got, wantAll)
			}
		})
	}
}
