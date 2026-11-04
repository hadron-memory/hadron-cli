package team

import "github.com/hadron-memory/hadron-cli/internal/api/gen"

// Explicit DTO for the server's team-channel checking result. A nil result
// remains unknown/withheld; the CLI never derives a status from task counts,
// elapsed local time or the old activity/liveness fields.
type channelCheckDTO struct {
	Status        string  `json:"status"`
	PendingReason *string `json:"pendingReason"`
	LastCheckedAt *string `json:"lastCheckedAt"`
}

func channelCheckLabel(check *channelCheckDTO) string {
	if check == nil {
		return "—"
	}
	if check.Status == "PENDING" && check.PendingReason != nil {
		return check.Status + " (" + *check.PendingReason + ")"
	}
	return check.Status
}

func channelCheckFromFields(fields gen.ChannelCheckFields) *channelCheckDTO {
	dto := &channelCheckDTO{Status: string(fields.Status), LastCheckedAt: fields.LastCheckedAt}
	if fields.PendingReason != nil {
		reason := string(*fields.PendingReason)
		dto.PendingReason = &reason
	}
	return dto
}
