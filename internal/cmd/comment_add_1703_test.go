package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCommentAdd1703ReplyOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		code    int
		unknown bool
	}{
		{"empty object", 200, `{"data":{"replyToComment":{}}}`, 7, true},
		{"null", 200, `{"data":{"replyToComment":null}}`, 7, true},
		{"gateway", 502, `{"error":"lost reply"}`, 7, true},
		{"gateway typed envelope", 502, addRefusal("COMMENT_FORBIDDEN", nil), 8, true},
		{"dropped connection", -1, "", 7, true},
		{"internal", 200, addRefusal("INTERNAL_SERVER_ERROR", nil), 1, true},
		{"post-commit not found", 200, addRefusal("COMMENT_NOT_FOUND", nil), 4, true},
		{"forbidden", 200, addRefusal("COMMENT_FORBIDDEN", nil), 8, false},
		{"deleted", 200, addRefusal("COMMENT_DELETED", nil), 5, false},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonMode), func(t *testing.T) {
				s := newAddServer()
				s.seed("T1", false)
				s.replyOverride = func(int) (int, string) { return tc.status, tc.body }
				args := []string{"comment", "add", addTarget, "--body", "x"}
				if jsonMode {
					args = append(args, "--json")
				}
				got := runAdd(t, s, "", args...)
				if got.code != tc.code || strings.Join(s.ops(), ",") != "CreateComment,ReplyToComment" {
					t.Fatalf("code=%d ops=%v out=%s err=%s", got.code, s.ops(), got.stdout, got.stderr)
				}
				message := got.stderr
				if jsonMode {
					var env struct {
						Error struct {
							Code       int            `json:"code"`
							Message    string         `json:"message"`
							Extensions map[string]any `json:"extensions"`
						} `json:"error"`
					}
					if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
						t.Fatal(err)
					}
					if env.Error.Code != tc.code || env.Error.Extensions["threadId"] != "T1" {
						t.Fatal(got.stdout)
					}
					message = env.Error.Message
				} else if got.stdout != "" {
					t.Fatalf("unexpected success output %q", got.stdout)
				}
				if !strings.Contains(message, "thread T1") {
					t.Fatal(message)
				}
				if tc.unknown {
					if !strings.Contains(message, "outcome is unknown") || !strings.Contains(message, "read the thread before retrying") || strings.Contains(message, "thread T1 failed:") {
						t.Fatal(message)
					}
				} else if !strings.Contains(message, "thread T1 failed:") {
					t.Fatal(message)
				}
			})
		}
	}
}
