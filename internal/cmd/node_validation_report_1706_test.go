package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeGetContentValidationAuthorCheck1706(t *testing.T) {
	for _, check := range []string{"CHECKED", "AUTHOR_UNKNOWN"} {
		for _, state := range []string{"PASS", "STALE"} {
			t.Run(check+state, func(t *testing.T) {
				cv := validation(state, "PASS", 7)
				checked := strings.Replace(cv, `"outcome":"PASS"`, `"outcome":"PASS","authorCheck":"`+check+`"`, 1)
				srv, _ := captureGraphQL(t, map[string]string{
					"ResolveUrn":                      `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
					"GetNode":                         nodeGetJSON(testNodeURL),
					"NodeLiveRevisionsContentChecked": stampedLive(7, taskAuthorship, checked),
					"NodeLiveRevisionsMinted":         stampedLive(7, taskAuthorship, cv),
				})
				raw := nodeGetOut(t, srv.URL, true)
				var dto struct {
					ContentValidation struct {
						LatestReport struct {
							AuthorCheck *string `json:"authorCheck"`
						} `json:"latestReport"`
					} `json:"contentValidation"`
				}
				if err := json.Unmarshal([]byte(raw), &dto); err != nil {
					t.Fatal(err)
				}
				if dto.ContentValidation.LatestReport.AuthorCheck == nil || *dto.ContentValidation.LatestReport.AuthorCheck != check {
					t.Fatalf("authorCheck missing/wrong: %s", raw)
				}
				text := nodeGetOut(t, srv.URL, false)
				want := "author comparison checked"
				if check == "AUTHOR_UNKNOWN" {
					want = "author could not be compared"
				}
				if !strings.Contains(text, want) || !strings.Contains(text, "(advisory)") {
					t.Fatalf("missing advisory author comparison %q: %s", want, text)
				}
			})
		}
	}
}

func TestNodeGetContentValidationOlderAuthorCheck1706(t *testing.T) {
	var probes int
	srv, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisionsContentChecked":
			probes++
			return `{"errors":[{"message":"Cannot query field \"authorCheck\" on type \"NodeContentValidationReport\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
		case "NodeLiveRevisionsMinted":
			return stampedLive(7, taskAuthorship, validation("PASS", "PASS", 7))
		}
		return ""
	})
	raw := nodeGetOut(t, srv.URL, true)
	if probes != 1 || !strings.Contains(raw, `"authorCheck": null`) || !strings.Contains(raw, `"state": "PASS"`) {
		t.Fatalf("older-field fallback must preserve stamps with unknown author comparison: probes%d %s", probes, raw)
	}
}
