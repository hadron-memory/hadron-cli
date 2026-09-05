package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #544 — a gateway 5xx must reach exit 7, THROUGH THE REAL CLIENT.
//
// The pre-existing unit cases hand-build `&graphql.HTTPError{StatusCode: 502}`
// with no Errors, which is a shape genqlient never produces: it synthesises a
// one-entry list holding the raw body whenever that body is not JSON. So the
// suite asserted the branch worked on a fixture that could not occur, and the
// branch it was guarding had been unreachable in production the whole time —
// a fixture that stopped describing the server and started describing the test.
//
// These drive a real httptest server so the error is whatever the client
// actually builds.
func TestGatewayResponsesAreUnavailableThroughTheRealClient(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
		want        int
		// mustContain, when set, is text that can only appear if the response
		// body SURVIVED the doer and was parsed by genqlient.
		mustContain string
	}{
		{
			// The canonical case from #544: nginx / Cloudflare / Traefik.
			name: "an HTML 502 is a lost request", status: http.StatusBadGateway,
			contentType: "text/html", body: "<html>502 Bad Gateway</html>",
			want: exitcode.Unavailable,
		},
		{
			name: "a bare-text 503 is a lost request", status: http.StatusServiceUnavailable,
			contentType: "text/plain", body: "upstream connect error",
			want: exitcode.Unavailable,
		},
		{
			name: "an empty-bodied 504 is a lost request", status: http.StatusGatewayTimeout,
			contentType: "text/plain", body: "",
			want: exitcode.Unavailable,
		},
		{
			// A JSON-producing gateway that is still NOT the API's opinion:
			// `errors` entries that are not objects carrying `message`.
			name: "a JSON 502 whose errors are strings is still the edge", status: http.StatusBadGateway,
			contentType: "application/json", body: `{"errors":["upstream unavailable"]}`,
			want: exitcode.Unavailable,
		},
		{
			// THE POSITIVE CONTROL, and the one that must not regress: the API
			// itself answering 500 with a well-formed envelope is an OPINION.
			// A fix that made every 5xx transport would pass every case above
			// and fail here.
			name: "a 500 carrying a real GraphQL envelope is a refusal", status: http.StatusInternalServerError,
			contentType: "application/json",
			body:        `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
			want:        exitcode.Error,
		},
		{
			// And the API's opinion is still an opinion when it declines to
			// send extensions — `message` is the only field the spec requires.
			name: "a 500 whose error carries only message is still a refusal", status: http.StatusInternalServerError,
			contentType: "application/json", body: `{"errors":[{"message":"boom"}]}`,
			want: exitcode.Error,
		},
		{
			// THE CONTROL THAT PROVES THE BODY SURVIVED THE DOER.
			//
			// Every other case asserts only an exit code, and 1 is ALSO what you
			// get when the body is destroyed on the way through: genqlient then
			// cannot read it and MapError falls back to the generic code. So
			// "a 5xx envelope is a refusal" was passing for two different
			// reasons and could not tell them apart — deleting the body-restore
			// in the doer left the whole table GREEN.
			//
			// The server's own message is the discriminator, because it can only
			// be there if the bytes the doer consumed were handed back and
			// parsed. A destroyed body yields "read on closed response body"
			// instead.
			//
			// The exit code stays 1, and that is not an oversight in this
			// fixture: MapError's HTTPError branch keys on the STATUS and
			// returns before codeForExtension, so a 5xx carrying a typed
			// extension code still exits 1. Noted rather than changed — it is
			// not #544, and it is a different decision with its own blast
			// radius.
			name:        "a 5xx envelope's message survives the doer",
			status:      http.StatusServiceUnavailable,
			contentType: "application/json",
			body:        `{"errors":[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]}`,
			want:        exitcode.Error,
			mustContain: "no such node",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			client, err := NewClient(srv.URL, "", nil)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			var data struct{}
			rawErr := client.MakeRequest(context.Background(),
				&graphql.Request{OpName: "Probe", Query: "query Probe { __typename }"},
				&graphql.Response{Data: &data})
			if rawErr == nil {
				t.Fatal("the server answered with an error status; the client must report it")
			}
			mapped := MapError(rawErr)
			if got := exitcode.FromError(mapped); got != tc.want {
				t.Errorf("exit code = %d, want %d\n  raw:    %v\n  mapped: %v", got, tc.want, rawErr, mapped)
			}
			if tc.mustContain != "" && !strings.Contains(mapped.Error(), tc.mustContain) {
				t.Errorf("the message must carry %q — its absence means the body did not survive the doer: %v",
					tc.mustContain, mapped)
			}
			// And a gateway page must NOT be dumped at the user, which is what
			// the pre-#544 behaviour did: a page of HTML inside a JSON envelope.
			if tc.want == exitcode.Unavailable && strings.Contains(mapped.Error(), "<html>") {
				t.Errorf("a gateway body must not be dumped into the message: %v", mapped)
			}
		})
	}
}

// A 5xx whose body is CUT SHORT mid-read is a lost answer, not a refusal.
//
// This is the one branch of classifyGatewayResponse that a status+body fixture
// cannot reach: `io.ReadAll` has to fail partway. Constructed by promising more
// bytes in Content-Length than the handler writes and then hanging up, which is
// what a gateway killed mid-response actually does.
//
// It matters for the same reason PR #415's `io.ErrUnexpectedEOF` case did:
// headers arrived, so the request certainly reached the server and a mutation
// may already have committed. Falling through to exit 1 would tell a caller the
// write definitely did not happen.
func TestAGatewayBodyCutShortIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		// A COMPLETE, VALID envelope — short of the promised length.
		//
		// The shape is chosen so the two behaviours DIVERGE. A truncated body
		// that is merely invalid JSON fails the envelope check anyway, so it
		// cannot tell "we honoured the read error" from "we classified the
		// bytes we happened to get" — verified by mutation: with the readErr
		// branch disabled, a partial-JSON fixture stayed green. These bytes
		// parse as the API's opinion, so ignoring the read error scores this a
		// REFUSAL (exit 1) and only honouring it yields exit 7.
		_, _ = w.Write([]byte(`{"errors":[{"message":"partial"}]}`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hang up mid-body.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var data struct{}
	rawErr := client.MakeRequest(context.Background(),
		&graphql.Request{OpName: "Probe", Query: "query Probe { __typename }"},
		&graphql.Response{Data: &data})
	if rawErr == nil {
		t.Fatal("a truncated response must be reported as an error")
	}
	mapped := MapError(rawErr)
	if got := exitcode.FromError(mapped); got != exitcode.Unavailable {
		t.Errorf("exit code = %d, want %d (Unavailable) — a body cut short is a lost answer, not a refusal\n  raw:    %v\n  mapped: %v",
			got, exitcode.Unavailable, rawErr, mapped)
	}
}
