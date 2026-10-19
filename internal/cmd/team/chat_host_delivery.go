package team

import "context"

// postHostTarget is the posting scope, not a grant to deliver to that App.
// The C1 adapter must resolve canonical identity and authorize its own host
// using a separate session-free key before returning a pass.
type postHostTarget struct {
	AppRef string
	Server string
}

type postHostDeliverySummary struct {
	Delivered, Held, Pending, Failed int
}

// A pass must use C1's shared locks, current registration, terminal transaction,
// confirmation and receipt reconciliation. It never posts or marks chat read.
type postHostDeliveryPass interface {
	Run(context.Context) (postHostDeliverySummary, error)
}

type postHostDeliveryPreparer func(context.Context, string, postHostTarget) (postHostDeliveryPass, error)
