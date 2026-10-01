package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// sessionStartContinuity is the stable JSON projection delivered at bind.
// In particular, a MISSING status can still carry an OLDER handoff; callers
// must not infer currency from the handoff's presence alone (cor:agt:020:03).
type sessionStartContinuity struct {
	Status          string                       `json:"status"`
	Handoff         *sessionStartHandoff         `json:"handoff"`
	PreviousSession *sessionStartPreviousSession `json:"previousSession"`
}

type sessionStartHandoff struct {
	URN       string  `json:"urn"`
	Loc       string  `json:"loc"`
	Content   *string `json:"content"`
	CreatedAt string  `json:"createdAt"`
}

type sessionStartPreviousSession struct {
	ID            string  `json:"id"`
	EndedAt       *string `json:"endedAt"`
	AutoExpiredAt *string `json:"autoExpiredAt"`
}

func sessionStartContinuityDTO(c *gen.GetWorkerContinuityWorkerContinuity) *sessionStartContinuity {
	if c == nil {
		return nil // masked or otherwise unavailable, not "no handoff"
	}
	d := &sessionStartContinuity{Status: string(c.Status)}
	if c.Handoff != nil {
		d.Handoff = &sessionStartHandoff{
			URN: c.Handoff.Urn, Loc: c.Handoff.Loc,
			Content: c.Handoff.Content, CreatedAt: c.Handoff.CreatedAt,
		}
	}
	if c.PreviousSession != nil {
		d.PreviousSession = &sessionStartPreviousSession{
			ID: c.PreviousSession.Id, EndedAt: c.PreviousSession.EndedAt,
			AutoExpiredAt: c.PreviousSession.AutoExpiredAt,
		}
	}
	return d
}

func renderSessionStartContinuity(out io.Writer, c *sessionStartContinuity) error {
	switch c.Status {
	case "CURRENT":
		if c.Handoff == nil {
			_, err := fmt.Fprintln(out, "\nhandoff: current status reported, but the handoff is unavailable")
			return err
		}
		if _, err := fmt.Fprintf(out, "\nHandoff from the previous stint (CURRENT): %s\n", c.Handoff.URN); err != nil {
			return err
		}
	case "FIRST_STINT":
		if _, err := fmt.Fprintln(out, "\nhandoff: none (first stint)"); err != nil {
			return err
		}
		if c.Handoff == nil {
			return nil
		}
		if _, err := fmt.Fprintf(out, "An unlinked handoff exists for this worker: %s\n", c.Handoff.URN); err != nil {
			return err
		}
	case "MISSING":
		if _, err := fmt.Fprintln(out, "\nNo current handoff: the previous stint ended without one."); err != nil {
			return err
		}
		if p := c.PreviousSession; p != nil {
			if p.AutoExpiredAt != nil {
				if _, err := fmt.Fprintf(out, "Previous session %s was auto-expired at %s.\n", p.ID, *p.AutoExpiredAt); err != nil {
					return err
				}
			} else if p.EndedAt != nil {
				if _, err := fmt.Fprintf(out, "Previous session %s ended at %s.\n", p.ID, *p.EndedAt); err != nil {
					return err
				}
			}
		}
		if c.Handoff == nil {
			return nil
		}
		if _, err := fmt.Fprintf(out, "Older handoff (history, not the current state): %s\n", c.Handoff.URN); err != nil {
			return err
		}
	default:
		_, err := fmt.Fprintf(out, "\nworker continuity: unrecognized status %q\n", c.Status)
		return err
	}
	if content := strings.TrimSpace(derefHandoffContent(c.Handoff)); content != "" {
		_, err := fmt.Fprintf(out, "\n%s\n", content)
		return err
	}
	_, err := fmt.Fprintln(out, "handoff content is unavailable")
	return err
}

func derefHandoffContent(h *sessionStartHandoff) string {
	if h == nil || h.Content == nil {
		return ""
	}
	return *h.Content
}
