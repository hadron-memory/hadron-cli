package team

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Xan LOCAL P2 original probe, retained with live/lapsed controls.
func TestPushLapsedExactSessionMustHold(t *testing.T) {
	for _, live := range []bool{true, false} {
		t.Run(fmt.Sprint(live), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct{ OperationName string }
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "GetTeamSession", "GetBoundSessionLiveness":
					fmt.Fprintf(w, `{"data":{"session":{"id":"session","workerId":"worker","tool":"claude-code","endedAt":null,"isLive":%t}}}`, live)
				default:
					t.Errorf("unexpected operation %s", q.OperationName)
				}
			}))
			defer srv.Close()
			b := &binding{SessionID: "session", WorkerID: "worker", AppID: "app", Server: srv.URL}
			client := graphql.NewClient(srv.URL, srv.Client())
			if got := pushSession(context.Background(), client, b); got != live {
				t.Errorf("sender exact-session liveness=%t, gate accepted=%t", live, got)
			}
			if got := pushSessionFamily(context.Background(), client, b, "claude"); got != live {
				t.Errorf("recipient exact-session liveness=%t, gate accepted=%t", live, got)
			}
		})
	}
}
