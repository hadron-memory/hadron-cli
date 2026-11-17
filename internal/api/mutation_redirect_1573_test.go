package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("boom") }

// #1573 item 2: the fail-closed branches of the decision function, hand-built.
func TestPostBodyIsMutationFailClosedBranches(t *testing.T) {
	post := func(body string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://x/graphql", strings.NewReader(body))
		return r
	}
	opaque, _ := http.NewRequest(http.MethodPost, "http://x/graphql", io.NopCloser(strings.NewReader(`{"query":"{ x }"}`)))
	getBodyErr := post(`{"query":"{ x }"}`)
	getBodyErr.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("boom") }
	readErr := post(`{"query":"{ x }"}`)
	readErr.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(errBody{}), nil }
	get, _ := http.NewRequest(http.MethodGet, "http://x/graphql", nil)
	noBody, _ := http.NewRequest(http.MethodPost, "http://x/graphql", nil)

	for _, c := range []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"GetBody nil (opaque reader)", opaque, true},
		{"GetBody error", getBodyErr, true},
		{"ReadAll error", readErr, true},
		{"non-JSON body", post("not json"), true},
		{"empty query (APQ envelope)", post(`{"extensions":{"persistedQuery":{"version":1}}}`), true},
		{"blank query", post(`{"query":"  "}`), true},
		{"query only", post(`{"query":"query X { x }"}`), false},
		{"mutation", post(`{"query":"mutation X { x }"}`), true},
		{"GET is never a mutation", get, false},
		{"POST without a body", noBody, false},
	} {
		if got := postBodyIsMutation(c.req); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// #1573 item 2 / Rico: the redirect decision itself, including the arms that
// do not read a body.
func TestRequiresOneSendArms(t *testing.T) {
	q, _ := http.NewRequest(http.MethodPost, "http://x/graphql", strings.NewReader(`{"query":"query X { x }"}`))
	next, _ := http.NewRequest(http.MethodGet, "http://y/graphql", nil)
	if requiresOneSend(next, []*http.Request{q}) {
		t.Error("a query chain must follow")
	}
	if !requiresOneSend(next.WithContext(WithNoRedirect(context.Background())), []*http.Request{q}) {
		t.Error("WithNoRedirect must refuse even a query")
	}
	if requiresOneSend(next, nil) || requiresOneSend(next, []*http.Request{nil}) {
		t.Error("an empty or nil-origin chain has no mutation to protect")
	}
}

// #1573 items 1 and 3: every redirect status refuses a mutation (301/302/303
// convert to GET and drop the body, refusing is still right); the empty-query
// envelope refuses 307/308 too.
func TestMutationRedirectEveryStatusAndEmptyEnvelope(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, c := range []struct {
			name, body string
			refuse     bool
		}{
			{"mutation", `{"query":"mutation X { x }"}`, true},
			{"empty-query envelope", `{"extensions":{"persistedQuery":{"version":1}}}`, true},
			{"query", `{"query":"query X { x }"}`, false},
		} {
			var dest atomic.Int32
			destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				dest.Add(1)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			t.Cleanup(destSrv.Close)
			srcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destSrv.URL+"/graphql", status)
			}))
			t.Cleanup(srcSrv.Close)
			// MakeRequest always sends a query field, so send the raw envelope
			// through the redirect policy's own http.Client.
			req, _ := http.NewRequest(http.MethodPost, srcSrv.URL+"/graphql", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := withSessionRedirects(&http.Client{}).Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if c.refuse && (!errors.Is(err, ErrRedirectPolicy) || dest.Load() != 0) {
				t.Errorf("%d %s: err=%v dest=%d", status, c.name, err, dest.Load())
			}
			if !c.refuse && err != nil {
				t.Errorf("%d %s must follow: %v", status, c.name, err)
			}
		}
	}
}
