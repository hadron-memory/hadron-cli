package api

import (
	"math"
	"sync"
)

// DeliveryPendingRecorder holds the latest valid
// extensions.hadron.deliveryPending snapshot (hadron-server#2473, cli#1084)
// seen on a successful response. It is filled in ONE place, the shared client,
// and read once by the entry point after the command has succeeded.
//
// The CLI computes nothing here: a count is either exactly what a supported,
// complete provider payload said, or there is no count. Absent, malformed,
// unsupported or incomplete metadata records nothing (never a fabricated zero).
type DeliveryPendingRecorder struct {
	mu    sync.Mutex
	count int64
	ok    bool
}

// maxSafeCount is 2^53-1, the largest integer a JSON number carries exactly.
const maxSafeCount = 1<<53 - 1

// ParseDeliveryPending reads the metadata of one response. It reports a count
// only for {v:1, count:<safe non-negative integer>, complete:true}; everything
// else, including a future incomplete (lower-bound) payload, is unavailable.
func ParseDeliveryPending(extensions map[string]any) (int64, bool) {
	hadron, _ := extensions["hadron"].(map[string]any)
	dp, _ := hadron["deliveryPending"].(map[string]any)
	if dp == nil {
		return 0, false
	}
	if v, ok := dp["v"].(float64); !ok || v != 1 {
		return 0, false
	}
	if complete, ok := dp["complete"].(bool); !ok || !complete {
		return 0, false
	}
	n, ok := dp["count"].(float64)
	if !ok || math.IsNaN(n) || n < 0 || n > maxSafeCount || n != math.Trunc(n) {
		return 0, false
	}
	return int64(n), true
}

func (r *DeliveryPendingRecorder) record(extensions map[string]any) {
	if r == nil {
		return
	}
	n, ok := ParseDeliveryPending(extensions)
	if !ok {
		return
	}
	r.mu.Lock()
	r.count, r.ok = n, true
	r.mu.Unlock()
}

// Count returns the latest valid count; ok is false when none was seen.
func (r *DeliveryPendingRecorder) Count() (int64, bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count, r.ok
}

// ClientOption adjusts NewClient.
type ClientOption func(*nullSafeGraphQLClient)

// WithDeliveryPending records each successful response's deliveryPending
// metadata in r.
func WithDeliveryPending(r *DeliveryPendingRecorder) ClientOption {
	return func(c *nullSafeGraphQLClient) { c.delivery = r }
}
