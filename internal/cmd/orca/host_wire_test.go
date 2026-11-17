package orca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestHostPlan1635IdentityWireAndLaterRefusal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "fixture-token")
	repo := hostRepo1635(t)
	file := roster1635(t, repo, "Iris", "Dara")
	for _, refusal := range []string{"", "FORBIDDEN", "WORKER_NOT_FOUND"} {
		t.Run(refusal, func(t *testing.T) {
			calls := []string{}
			var mu sync.Mutex
			bodyBytes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var q struct {
					Op    string            `json:"operationName"`
					Query string            `json:"query"`
					Vars  map[string]string `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				calls = append(calls, q.Op)
				if !strings.HasPrefix(strings.TrimSpace(q.Query), "query ") {
					t.Error("provider mutation attempted")
				}
				w.Header().Set("Content-Type", "application/json")
				answer := ""
				switch q.Op {
				case "HostPlanApp":
					if q.Vars["ref"] != "hrn:app:acme.com:team" {
						t.Error(q.Vars)
					}
					answer = `{"data":{"app":{"id":"app-id"}}}`
				case "HostPlanWorker":
					if q.Vars["appRef"] != "hrn:app:acme.com:team" {
						t.Error(q.Vars)
					}
					if q.Vars["ref"] == "Dara" && refusal != "" {
						answer = fmt.Sprintf(`{"errors":[{"message":"SECRET-server-prose","extensions":{"code":%q}}]}`, refusal)
					} else {
						answer = fmt.Sprintf(`{"data":{"worker":{"id":%q,"appId":"app-id","family":"codex","retiredAt":null}}}`, "id-"+strings.ToLower(q.Vars["ref"]))
					}
				default:
					t.Errorf("unexpected provider operation %s", q.Op)
				}
				bodyBytes += len(answer)
				_, _ = fmt.Fprint(w, answer)
			}))
			defer server.Close()
			streams, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, JSON: true, ServerFlag: server.URL, ConfigFn: config.Load}
			env := defaultHostPlanEnv(f)
			hostCalls := 0
			env.Inventory = func(context.Context, []hostPlanWorker) (hostInventory, error) {
				hostCalls++
				return hostInventory{Repos: map[string]string{repo: "common"}}, nil
			}
			cmd := newCmdHostPlan(f, env)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"--roster-file", file})
			err := cmd.Execute()
			mu.Lock()
			defer mu.Unlock()
			want := 0
			if refusal == "FORBIDDEN" {
				want = 8
			}
			if refusal == "WORKER_NOT_FOUND" {
				want = 4
			}
			if exitcode.FromError(err) != want || len(calls) != 3 || calls[0] != "HostPlanApp" || calls[1] != "HostPlanWorker" || calls[2] != "HostPlanWorker" {
				t.Fatal(err, calls, out.String())
			}
			if bodyBytes == 0 || strings.Contains(out.String(), "SECRET") {
				t.Fatal("raw diagnostic leaked", out.String())
			}
			if refusal == "" && hostCalls != 1 {
				t.Fatal(hostCalls)
			}
			if refusal != "" && (hostCalls != 0 || !strings.Contains(out.String(), `"complete": false`)) {
				t.Fatal(hostCalls, out.String())
			}
		})
	}
}
