package hostpoller

import (
	"context"
	"errors"
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
			p.deliverStep(context.Background())
			if len(got) != 1 || got[0] != want {
				t.Fatalf("reported %v, want [%s]", got, want)
			}
		})
	}
}
