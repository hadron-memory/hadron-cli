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
	age := "just now"
	elapsed := now.Sub(at)
	if elapsed >= time.Hour {
		age = fmt.Sprintf("%dh", int64(elapsed/time.Hour))
	} else if elapsed >= time.Minute {
		age = fmt.Sprintf("%dm", int64(elapsed/time.Minute))
	}
	return "NO_SESSION (open, quiet " + age + ")"
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
