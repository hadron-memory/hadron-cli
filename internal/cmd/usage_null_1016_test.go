package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// #1016 reports a synthetic full page with a null entry; no original provider
// payload was retained. These fixtures reconstruct that documented shape.
func TestUsage1016MalformedPageIsNotSuccess(t *testing.T) {
	for _, payload := range []struct{ name, rows string }{
		{"full-null-last", usage65Event + `,null`},
		{"full-null-first", `null,` + usage65Event},
		{"partial-null", `null`},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", payload.name, asJSON), func(t *testing.T) {
				args := []string{"usage", "events", "--limit", "2", "--offset", "4"}
				if asJSON {
					args = append(args, "--json")
				}
				out, stderr, code, _ := usage65Run(t, `{"data":{"usageEvents":[`+payload.rows+`]}}`, args...)
				if code != 1 {
					t.Fatalf("exit=%d want1; stdout=%q stderr=%q", code, out, stderr)
				}
				if asJSON {
					var envelope struct {
						Error struct {
							Code    int    `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.Error.Code != 1 || envelope.Error.Message != "the server returned an invalid usage-event row" {
						t.Fatalf("expected error envelope, got %q (%v)", out, err)
					}
				} else if out != "" {
					t.Fatalf("malformed page emitted success: %q", out)
				}
				if strings.Contains(stderr, "no total available") || strings.Contains(stderr, "more may exist") {
					t.Fatalf("malformed page emitted page note: %q", stderr)
				}
				if !asJSON && !strings.Contains(stderr, "invalid usage-event row") {
					t.Fatalf("missing fixed malformed-response diagnostic: %q", stderr)
				}
			})
		}
	}
}

func TestUsage1016ValidPagesKeepSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, rows string
		count      int
		full       bool
	}{
		{"empty", "", 0, false}, {"partial", usage65Event, 1, false}, {"full", usage65Event + `,` + usage65Event, 2, true},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", tc.name, asJSON), func(t *testing.T) {
				args := []string{"usage", "events", "--limit", "2", "--offset", "4", "--loc", "a:b:c:d", "--memory", "acme.com:kb", "--type", "read"}
				if asJSON {
					args = append(args, "--json")
				}
				out, stderr, code, calls := usage65Run(t, `{"data":{"usageEvents":[`+tc.rows+`]}}`, args...)
				if code != 0 {
					t.Fatalf("exit%d %s %s", code, out, stderr)
				}
				if !strings.Contains(stderr, "page at offset 4 (limit 2); no total available") || strings.Contains(stderr, "more may exist") != tc.full {
					t.Fatalf("page semantics: %q", stderr)
				}
				if tc.full && !strings.Contains(stderr, "--offset 6") {
					t.Fatal(stderr)
				}
				var vars map[string]any
				if err := json.Unmarshal(calls["UsageEvents"], &vars); err != nil {
					t.Fatal(err)
				}
				if len(vars) != 5 || vars["limit"] != float64(2) || vars["offset"] != float64(4) || vars["loc"] != "a:b:c:d" || vars["memoryId"] != "hrn:mem:acme.com:kb" || vars["eventType"] != "read" {
					t.Fatal(vars)
				}
				if asJSON {
					if tc.count == 0 && strings.TrimSpace(out) != "[]" {
						t.Fatalf("empty array: %q", out)
					}
					var rows []map[string]any
					if err := json.Unmarshal([]byte(out), &rows); err != nil {
						t.Fatal(err)
					}
					if rows == nil || len(rows) != tc.count {
						t.Fatalf("array: %s", out)
					}
					for _, row := range rows {
						if len(row) != 11 || row["costMicroCents"] != nil || row["tokensIn"] != nil {
							t.Fatalf("projection: %s", out)
						}
					}
				} else if tc.count > 0 && (!strings.Contains(out, "unpriced") || !strings.Contains(out, "unknown")) {
					t.Fatal(out)
				}
			})
		}
	}
}
