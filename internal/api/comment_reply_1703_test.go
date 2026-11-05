package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestCommentReply1703CertaintyAndChain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   error
		known bool
	}{
		{"forbidden", gqlErr("COMMENT_FORBIDDEN"), true},
		{"deleted", gqlErr("COMMENT_DELETED"), true},
		{"body invalid", gqlErr("COMMENT_BODY_INVALID"), true},
		{"impersonation", gqlErr("COMMENT_IMPERSONATION_REFUSED"), true},
		{"ambiguous not found", gqlErr("COMMENT_NOT_FOUND"), false},
		{"internal", gqlErr("INTERNAL_SERVER_ERROR"), false},
		{"future code", gqlErr("COMMENT_FUTURE_ERROR"), false},
		{"timeout", context.DeadlineExceeded, false},
		{"cancel", context.Canceled, false},
		{"empty result", exitcode.Newf(exitcode.Unavailable, "no result"), false},
		{"5xx refusal", &graphql.HTTPError{StatusCode: 502, Response: graphql.Response{Errors: gqlerror.List{{Message: "denied", Extensions: map[string]any{"code": "COMMENT_FORBIDDEN"}}}}}, false},
		{"nested refusal", gqlerror.List{{Message: "denied", Path: ast.Path{ast.PathName("replyToComment"), ast.PathName("author")}, Extensions: map[string]any{"code": "COMMENT_FORBIDDEN"}}}, false},
		{"mixed", gqlerror.List{{Message: "denied", Extensions: map[string]any{"code": "COMMENT_FORBIDDEN"}}, {Message: "fault", Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapped := MapError(tc.raw)
			wrapped := WithThreadID(mapped, "T1")
			if !errors.Is(wrapped, mapped) || exitcode.FromError(wrapped) != exitcode.FromError(mapped) || ServerCodeOf(wrapped) != ServerCodeOf(mapped) {
				t.Fatalf("chain lost: %v", wrapped)
			}
			var threaded interface{ ThreadID() string }
			if !errors.As(wrapped, &threaded) || threaded.ThreadID() != "T1" {
				t.Fatal(wrapped)
			}
			if tc.known {
				if !strings.Contains(wrapped.Error(), "thread T1 failed:") {
					t.Fatal(wrapped)
				}
			} else if !strings.Contains(wrapped.Error(), "outcome is unknown") || !strings.Contains(wrapped.Error(), "read the thread before retrying") || strings.Contains(wrapped.Error(), "thread T1 failed:") {
				t.Fatal(wrapped)
			}
		})
	}
	if WithThreadID(nil, "T1") != nil {
		t.Fatal("nil changed")
	}
}
