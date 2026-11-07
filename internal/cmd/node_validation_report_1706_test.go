package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeGetContentValidationAuthorCheck1706(t *testing.T) {
	for _, check := range []string{"CHECKED", "AUTHOR_UNKNOWN"} {
		for _, state := range []string{"PASS", "STALE"} {
			t.Run(check+state, func(t *testing.T) {
				cv := validation(state, "PASS", 7)
				checked := strings.Replace(cv, `"outcome":"PASS"`, `"outcome":"PASS","authorCheck":"`+check+`"`, 1)
				srv := contentCheckedServer1706(t, map[string]string{
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

func contentCheckedServer1706(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var q struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		if q.OperationName == "NodeLiveRevisionsContentChecked" && !strings.Contains(q.Query, "authorCheck") {
			t.Error("authorCheck missing from actual wire selection")
		}
		body, ok := responses[q.OperationName]
		if !ok {
			body, ok = unstubbedDefault(q.OperationName)
		}
		if !ok {
			t.Errorf("unexpected operation %s", q.OperationName)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}
