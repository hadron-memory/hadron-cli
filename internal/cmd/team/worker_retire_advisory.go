package team

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// retireAdvisoryDTO is the advisory object added to `worker retire --json`
// (cli#494). Every key is always present; null means UNAVAILABLE to this
// caller — never "none", never a guess. It carries stored facts only: no
// handoff body, no previous-session internals, no consumption claim.
type retireAdvisoryDTO struct {
	ContinuityStatus *string `json:"continuityStatus"`
	HasLiveSession   *bool   `json:"hasLiveSession"`
	HandoffURN       *string `json:"handoffURN"`
}

// retireAdvisory is the advisory plus the one extra fact the human text shows
// (the handoff's write time); the time is deliberately not in the JSON shape.
type retireAdvisory struct {
	retireAdvisoryDTO
	handoffAt *string
}

// readRetireAdvisory makes the ONE advisory read. Any failure — a transport
// error, an older server that rejects Worker.continuity, a masked worker, an
// unrecognized status, a slow server — leaves the continuity status and
// handoff reference null (hasLiveSession is retained from the already-authorized
// resolved worker): the retirement continues through its ordinary confirmation
// (never blocked, never retried).
// retireAdvisoryTimeout bounds the best-effort read so a hung server cannot
// hang the prompt.
const retireAdvisoryTimeout = 5 * time.Second

func readRetireAdvisory(ctx context.Context, client graphql.Client, w *gen.WorkerFields) retireAdvisory {
	adv := retireAdvisory{retireAdvisoryDTO: retireAdvisoryDTO{HasLiveSession: w.HasLiveSession}}
	ctx, cancel := context.WithTimeout(ctx, retireAdvisoryTimeout)
	defer cancel()
	resp, err := gen.GetWorkerRetireAdvisory(ctx, client, w.Id)
	if err != nil || resp == nil || resp.Worker == nil || resp.Worker.Continuity == nil {
		return adv
	}
	c := resp.Worker.Continuity
	switch c.Status {
	case gen.WorkerContinuityStatusCurrent, gen.WorkerContinuityStatusFirstStint, gen.WorkerContinuityStatusMissing:
		status := string(c.Status)
		adv.ContinuityStatus = &status
	default:
		return adv // a status this build does not know is unavailable, not absence
	}
	if c.Handoff != nil {
		urn, at := c.Handoff.Urn, c.Handoff.CreatedAt
		adv.HandoffURN, adv.handoffAt = &urn, &at
	}
	return adv
}

// writeRetireAdvisory prints the human advisory (to stderr, before the
// confirmation). Wording is bound by the CHECKED gate: it states stored facts
// only, never claims a handoff is unconsumed, unread, lost or deleted, and
// never turns a false/null liveness into "no session ever existed".
func writeRetireAdvisory(out io.Writer, a retireAdvisory) {
	switch {
	case a.ContinuityStatus == nil:
		fmt.Fprintln(out, "Handoff: the handoff state is unavailable to you (not readable, or this server does not report it) — that is not evidence that none exists.")
	case *a.ContinuityStatus == string(gen.WorkerContinuityStatusCurrent):
		fmt.Fprintf(out, "Handoff: the last ended stint wrote the newest handoff%s.\n", handoffRef(a))
	case *a.ContinuityStatus == string(gen.WorkerContinuityStatusFirstStint):
		fmt.Fprintf(out, "Handoff: this worker is in its first stint; no earlier ended stint is recorded%s.\n", handoffRefAfter(a, "; a handoff you can see: "))
	default: // MISSING
		fmt.Fprintf(out, "Handoff: no handoff from the last stint that you can see — this is not proof none was written%s.\n", handoffRefAfter(a, "; an older handoff you can see: "))
	}
	if a.HasLiveSession != nil && *a.HasLiveSession {
		fmt.Fprintln(out, "An open session exists; that open session may still write a handoff.")
	}
	fmt.Fprintln(out, "Note: retirement stops new bindings and deletes no handoff nodes.")
}

func handoffRef(a retireAdvisory) string { return handoffRefAfter(a, ": ") }

func handoffRefAfter(a retireAdvisory, lead string) string {
	if a.HandoffURN == nil {
		return ""
	}
	// handoffAt is set together with HandoffURN (same returned handoff row).
	return lead + *a.HandoffURN + " (written " + *a.handoffAt + ")"
}
