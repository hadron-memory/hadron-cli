package team

import (
	"context"
	"errors"
	"sort"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	checkEmpty     = 9
	checkUncertain = 10
)

var errCheckIncarnation = errors.New("channel incarnation changed; run team chat check again to sample the current channel")
var errCheckTransport = errors.New("check transport unavailable")

type checkSnapshot struct {
	ChannelID                 string
	Incarnation, Cursor, Head int
	CursorIncarnation         *int
}
type checkError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type checkResult struct {
	ScanCursor        int         `json:"scanCursor"`
	CursorIncarnation *int        `json:"cursorIncarnation"`
	Error             *checkError `json:"error"`
	Outcome           string      `json:"outcome"`
	SampledCursor     int         `json:"sampledCursor"`
	SampledHead       int         `json:"sampledHead"`
	MessageCount      int         `json:"messageCount"`
	ArtifactPath      *string     `json:"artifactPath"`
	MarkAttempted     bool        `json:"markAttempted"`
	MarkStatus        string      `json:"markStatus"`
	MarkVerification  *string     `json:"markVerification"`
}
type checkDeps struct {
	// before is exclusive and decreases: the server's beforeSeq always pages backward.
	page     func(context.Context, int) ([]teamChatMessageDTO, error)
	publish  func([]teamChatMessageDTO) (string, error)
	mark     func(context.Context) (bool, error)
	readback func(context.Context) (bool, error)
}

func runChatCheck(ctx context.Context, s checkSnapshot, d checkDeps) (checkResult, error) {
	floor := checkScanFloor(s)
	r := checkResult{Outcome: "failed", SampledCursor: s.Cursor, ScanCursor: floor, CursorIncarnation: s.CursorIncarnation, SampledHead: s.Head, MarkStatus: "not_attempted"}
	if s.Cursor < 0 || s.Head < floor || s.Head >= 2147483647 {
		return r, exitcode.Newf(exitcode.Conflict, "inconsistent channel sample; nothing marked")
	}
	messages := []teamChatMessageDTO{}
	before := s.Head + 1
	for {
		if err := ctx.Err(); err != nil {
			return r, exitcode.New(exitcode.Cancelled, err)
		}
		page, err := d.page(ctx, before)
		if err != nil {
			return r, err
		}
		if len(page) == 0 {
			break
		}
		// Each bounded backward page is returned oldest-first. Reject duplicates,
		// late arrivals, and any nonprogress; never trust total or short-page exhaustion.
		previous := floor
		for _, m := range page {
			if m.Seq <= previous || m.Seq >= before {
				return r, exitcode.Newf(exitcode.Unavailable, "invalid tagged page sequence; nothing marked")
			}
			previous = m.Seq
		}
		messages = append(messages, page...)
		before = page[0].Seq
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Seq < messages[j].Seq })
	r.MessageCount = len(messages)
	path, err := d.publish(messages)
	if path != "" {
		r.ArtifactPath = &path
	}
	if err != nil {
		return r, err
	}
	if err := ctx.Err(); err != nil {
		return r, exitcode.New(exitcode.Cancelled, err)
	}
	r.MarkAttempted = true
	r.MarkStatus = "uncertain"
	confirmed, err := d.mark(ctx)
	verify := "response"
	ambiguous := errors.Is(err, errCheckTransport)
	if errors.Is(err, errCheckTransport) {
		confirmed, err = d.readback(ctx)
		verify = "readback"
		if !confirmed && !errors.Is(err, errCheckIncarnation) && ctx.Err() == nil {
			confirmed, err = d.mark(ctx)
			verify = "retry_response"
		}
	}
	if !confirmed {
		if err != nil && !errors.Is(err, errCheckTransport) {
			if ambiguous {
				return r, exitcode.Newf(checkUncertain, "mark outcome uncertain after a lost reply; %s", err)
			}
			if exitcode.FromError(err) != checkUncertain {
				r.MarkStatus = "refused"
			}
			return r, err
		}
		return r, exitcode.Newf(checkUncertain, "mark outcome uncertain; keep the artifact and run team chat check again (no further retry was sent)")
	}
	r.MarkStatus = "confirmed"
	r.MarkVerification = &verify
	r.Outcome = "written_and_marked"
	if len(messages) == 0 {
		r.Outcome = "empty_and_marked"
	}
	return r, nil
}

// A legacy or previous-incarnation cursor is not evidence of current coverage.
// Scan the whole current incarnation, preserving the stored value for disclosure.
func checkScanFloor(s checkSnapshot) int {
	if s.CursorIncarnation == nil || *s.CursorIncarnation != s.Incarnation {
		return 0
	}
	return s.Cursor
}
