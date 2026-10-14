package node

import "context"

// SigningCapabilityProbe answers whether the server advertises the
// `node-signing` switch (GET /capabilities, hadron-server#1925/#1930) —
// the gate for folding a signature-status summary into `node get`
// (cli#878 part 3). A switched-off or older server must cost no extra
// refused round trip, so the probe's failure reads as "not advertised".
//
// Package-level so command tests can inject a fake (house convention).
var SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
	return false, nil // RED-FIRST STUB: the api.Capabilities wiring lands with the fold
}
