package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#1264 item 1: firstErrorView keeps the response's X-Request-Id by
// rebuilding headerRequestIDError around the FIRST error only. Nothing of a
// later error may survive in Error(), the JSON details or anywhere down the
// Unwrap chain, whatever the HTTP status; and an internal-fault first error
// still prints the header id.

const sentinel1264 = "LATER_SENTINEL_1264"

// cloneVia1264 drives one clone-shaped request through the real client
// boundary (which attaches the header) and MapCloneAgentError.
func cloneVia1264(t *testing.T, status int, header, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", header)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient(srv.URL, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	var data struct{}
	err = client.MakeRequest(context.Background(), &graphql.Request{Query: "mutation CloneAgent { __typename }", OpName: "CloneAgent"}, &graphql.Response{Data: &data})
	if err == nil {
		t.Fatal("expected an error")
	}
	var h *headerRequestIDError
	if !errors.As(err, &h) {
		t.Fatalf("status %d: the client did not attach the header, so the header branch is not under test: %v", status, err)
	}
	return MapCloneAgentError(err)
}

// chainText1264 renders every error reachable through Unwrap (single and
// multi), including the %+v form, so a wrapped original envelope is found
// wherever it hides.
func chainText1264(err error) string {
	var b strings.Builder
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		fmt.Fprintf(&b, "%s\n%+v\n%#v\n", e.Error(), e, e)
		switch u := e.(type) {
		case interface{ Unwrap() []error }:
			for _, c := range u.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		}
	}
	walk(err)
	return b.String()
}

func TestCloneHeaderBranchDropsLaterError(t *testing.T) {
	body := `{"errors":[` +
		`{"message":"Agent is not public","extensions":{"code":"AGENT_NOT_PUBLIC","reason":"R1"}},` +
		`{"message":"` + sentinel1264 + `","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"` + sentinel1264 + `","reason":"` + sentinel1264 + `"}}]}`
	for _, status := range []int{200, 400, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			mapped := cloneVia1264(t, status, "h-1264", body)
			if strings.Contains(mapped.Error(), sentinel1264) {
				t.Errorf("Error() carries the later error: %s", mapped.Error())
			}
			details, _ := json.Marshal(CloneAgentDetails(mapped))
			if strings.Contains(string(details), sentinel1264) {
				t.Errorf("JSON details carry the later error: %s", details)
			}
			if !strings.Contains(string(details), `"AGENT_NOT_PUBLIC"`) {
				t.Errorf("the first refusal must stay the details: %s", details)
			}
			if chain := chainText1264(mapped); strings.Contains(chain, sentinel1264) {
				t.Errorf("the later error is reachable through Unwrap():\n%s", chain)
			}
		})
	}
}

func TestCloneHeaderIDKeptForInternalFaultFirst(t *testing.T) {
	body := `{"errors":[` +
		`{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}},` +
		`{"message":"` + sentinel1264 + `","extensions":{"code":"AGENT_NOT_PUBLIC","reason":"` + sentinel1264 + `"}}]}`
	for _, status := range []int{200, 400, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			mapped := cloneVia1264(t, status, "h-77", body)
			if !strings.Contains(mapped.Error(), "request id: h-77") {
				t.Errorf("an internal-fault first error must print the header id: %s", mapped.Error())
			}
			if chain := chainText1264(mapped); strings.Contains(chain, sentinel1264) {
				t.Errorf("the later error is reachable:\n%s", chain)
			}
		})
	}
}

// cli#1264 item 2: a subject name is forwarded only when it is a string; a
// nested or non-string name is dropped and the position kept.
func TestCloneDetailsDropNonStringSubjectName(t *testing.T) {
	var ext map[string]any
	if err := json.Unmarshal([]byte(`{"code":"CLONE_IMPORT_UNUSABLE","blockers":[{"code":"CLONE_IMPORT_UNUSABLE","subjects":[`+
		`{"position":1,"name":{"nested":"`+sentinel1264+`"}},{"position":2,"name":["`+sentinel1264+`"]},{"position":3,"name":7},{"position":4,"name":null},{"position":5,"name":"Kept"}]}]}`), &ext); err != nil {
		t.Fatal(err)
	}
	d := CloneAgentDetails(MapCloneAgentError(gqlerror.List{{Message: "x", Extensions: ext}}))
	got, _ := json.Marshal(d["blockers"])
	want := `[{"code":"CLONE_IMPORT_UNUSABLE","subjects":[{"position":1},{"position":2},{"position":3},{"position":4},{"name":"Kept","position":5}]}]`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
