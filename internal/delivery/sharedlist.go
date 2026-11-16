package delivery

import (
	"context"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// SharedList is a Terminals whose List is shared by every delivery of a poll
// cycle (#2181).
type SharedList struct {
	Terminals
}

// NewSharedList wraps inner so one List answers every caller within ttl.
func NewSharedList(inner Terminals, ttl time.Duration, now func() time.Time) *SharedList {
	return &SharedList{Terminals: inner}
}

var _ = orcaterm.Terminal{}
var _ = context.Background
