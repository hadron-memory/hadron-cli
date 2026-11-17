package team

import (
	"fmt"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Explicit DTO for the server's team-channel checking result. A nil result
// remains unknown/withheld; the CLI never derives a status from task counts,
// elapsed local time or the old activity/liveness fields.
type channelCheckDTO struct {
	Status          string  `json:"status"`
	NoSessionReason *string `json:"noSessionReason"`
	PendingReason   *string `json:"pendingReason"`
	LastCheckedAt   *string `json:"lastCheckedAt"`
}

func channelCheckLabel(check *channelCheckDTO) string {
	if check == nil {
		return "—"
	}
	if check.Status == "PENDING" && check.PendingReason != nil {
		return check.Status + " (" + *check.PendingReason + ")"
	}
	if check.Status == "NO_SESSION" && check.NoSessionReason != nil {
		switch *check.NoSessionReason {
		case "IDLE":
			return check.Status + " (open, quiet)"
		case "ENDED":
			return check.Status + " (ended)"
		case "NEVER_BOUND":
			return check.Status + " (never bound)"
		case "":
			// An empty reason is no reason: bare status, never "NO_SESSION ()".
			return check.Status
		default:
			return check.Status + " (" + *check.NoSessionReason + ")"
		}
	}
	return check.Status
}

// The reason alone classifies the worker. Age is only a local-clock display
// of the server timestamp: it changes no state, availability or exit code.
func channelCheckActivityLabel(check *channelCheckDTO, lastActiveAt *string, now time.Time) string {
	label := channelCheckLabel(check)
	if check == nil || check.Status != "NO_SESSION" || check.NoSessionReason == nil || *check.NoSessionReason != "IDLE" || lastActiveAt == nil {
		return label
	}
	at, err := time.Parse(time.RFC3339, *lastActiveAt)
	if err != nil {
		return label
	}
	return check.Status + " (open, quiet " + quietAge(now.Sub(at)) + ")"
}

// quietAge renders an elapsed time for display only: "just now" under a
// minute (and for a timestamp ahead of the local clock), then whole minutes,
// whole hours, and whole days from 48 hours. Every unit truncates, never
// rounds, matching the TERMINAL column's age.
func quietAge(elapsed time.Duration) string {
	switch {
	case elapsed >= 48*time.Hour:
		return fmt.Sprintf("%dd", int64(elapsed/(24*time.Hour)))
	case elapsed >= time.Hour:
		return fmt.Sprintf("%dh", int64(elapsed/time.Hour))
	case elapsed >= time.Minute:
		return fmt.Sprintf("%dm", int64(elapsed/time.Minute))
	}
	return "just now"
}

func channelCheckFromFields(fields gen.ChannelCheckFields) *channelCheckDTO {
	dto := &channelCheckDTO{Status: string(fields.Status), LastCheckedAt: fields.LastCheckedAt}
	if fields.NoSessionReason != nil {
		reason := string(*fields.NoSessionReason)
		dto.NoSessionReason = &reason
	}
	if fields.PendingReason != nil {
		reason := string(*fields.PendingReason)
		dto.PendingReason = &reason
	}
	return dto
}
