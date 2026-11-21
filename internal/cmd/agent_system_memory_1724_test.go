package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentSystemMemory1724(t *testing.T) {
	const memoryURN = "hrn:mem:other.example:server-returned"
	for _, asJSON := range []bool{false, true} {
		run := func(t *testing.T, memoryID, reply string, status int, failureMode string) (string, string, []string) {
			t.Helper()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if n := strings.Count(agentJSON, `"systemMemoryId":null`); n != 1 {
				t.Fatalf("agent fixture must contain exactly one null systemMemoryId, got %d", n)
			}
			var mu sync.Mutex
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OperationName, Query string
					Variables            map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				calls = append(calls, req.OperationName)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch req.OperationName {
				case "GetAgent":
					fixture := strings.Replace(agentJSON, `"systemMemoryId":null`, `"systemMemoryId":`+memoryID, 1)
					fmt.Fprint(w, `{"data":{"agent":`+fixture+`}}`)
				case "GetAgentLifecycle":
					fmt.Fprint(w, missingFieldJSON("lifecycle"))
				case "GetAgentContextCap":
					fmt.Fprint(w, missingFieldJSON("contextCap"))
				case "AgentSystemMemory":
					if req.Variables["ref"] != "sys-id" || len(req.Variables) != 1 {
						t.Errorf("lookup variables: %v", req.Variables)
					}
					// The operation must stay a bounded, read-only projection.
					compact := strings.Join(strings.Fields(req.Query), " ")
					if !strings.Contains(compact, "memory(ref: $ref) { id urn }") || strings.Contains(compact, "mutation") {
						t.Errorf("projection: %s", compact)
					}
					switch failureMode {
					case "timeout":
						// Hold only the optional request until the TEST client's
						// deadline expires; no production timeout is changed.
						<-r.Context().Done()
						return
					case "cancel":
						// Cancel only after the optional lookup has reached HTTP,
						// so an earlier required read cannot satisfy this pin.
						cancel()
						<-r.Context().Done()
						return
					}
					w.WriteHeader(status)
					fmt.Fprint(w, reply)
				default:
					t.Errorf("unexpected operation %s", req.OperationName)
					fmt.Fprint(w, `{"errors":[{"message":"unexpected"}]}`)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			if failureMode == "timeout" {
				f.HTTPClient.Timeout = time.Second
			}
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			root := NewRootCmd(f)
			args := []string{"agent", "get", "agent-ref", "--server", srv.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatalf("optional lookup changed success: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			return out.String(), stderr.String(), append([]string(nil), calls...)
		}
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			baseline, stderr, calls := run(t, `null`, "", 200, "")
			if stderr != "" || len(calls) != 3 {
				t.Fatalf("unset: %q %v", stderr, calls)
			}
			if strings.Contains(baseline, "systemMemoryUrn") {
				t.Fatal(baseline)
			}
			// An explicit empty id also needs no speculative lookup.
			empty, emptyErr, emptyCalls := run(t, `""`, "", 200, "")
			if emptyErr != "" || len(emptyCalls) != 3 || strings.Contains(empty, "systemMemoryUrn") {
				t.Fatalf("empty id: %q %q %v", empty, emptyErr, emptyCalls)
			}
			// A missing/refused/failed lookup must retain exactly the old id-only output.
			wantFallback := strings.Replace(baseline, `"systemMemoryId": null`, `"systemMemoryId": "sys-id"`, 1)
			if !asJSON {
				wantFallback = strings.Replace(baseline, "system memory: —", "system memory: sys-id", 1)
			}
			if wantFallback == baseline {
				t.Fatal("id-only fallback oracle did not replace the absent memory")
			}
			for _, tc := range []struct {
				name, reply string
				status      int
				failureMode string
			}{
				{name: "readable", reply: `{"data":{"memory":{"id":"sys-id","urn":"` + memoryURN + `"}}}`, status: 200},
				{name: "missing", reply: `{"data":{"memory":null}}`, status: 200},
				{name: "unreadable", reply: `{"errors":[{"message":"PRIVATE_SENTINEL","extensions":{"code":"FORBIDDEN"}}],"data":{"memory":null}}`, status: 200},
				{name: "partial error", reply: `{"errors":[{"message":"PRIVATE_SENTINEL"}],"data":{"memory":{"id":"sys-id","urn":"` + memoryURN + `"}}}`, status: 200},
				{name: "old server", reply: `{"errors":[{"message":"Cannot query field urn on type Memory"}]}`, status: 200},
				{name: "old server HTTP400", reply: `{"errors":[{"message":"Cannot query field urn on type Memory"}]}`, status: 400},
				{name: "slow optional lookup", failureMode: "timeout"},
				{name: "cancelled optional lookup", failureMode: "cancel"},
				{name: "outage", reply: `PRIVATE_SENTINEL`, status: 503},
				{name: "malformed", reply: `{`, status: 200},
				{name: "wrong id", reply: `{"data":{"memory":{"id":"other","urn":"` + memoryURN + `"}}}`, status: 200},
				{name: "empty urn", reply: `{"data":{"memory":{"id":"sys-id","urn":""}}}`, status: 200},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, stderr, calls := run(t, `"sys-id"`, tc.reply, tc.status, tc.failureMode)
					if stderr != "" || len(calls) != 4 || calls[0] != "GetAgent" || calls[1] != "GetAgentLifecycle" || calls[2] != "GetAgentContextCap" || calls[3] != "AgentSystemMemory" {
						t.Fatalf("stderr/calls: %q %v", stderr, calls)
					}
					if tc.name != "readable" {
						if got != wantFallback {
							t.Fatalf("fallback changed\ngot %s\nwant %s", got, wantFallback)
						}
						return
					}
					if asJSON {
						var fields map[string]any
						if err := json.Unmarshal([]byte(got), &fields); err != nil {
							t.Fatal(err)
						}
						if fields["systemMemoryUrn"] != memoryURN || fields["systemMemoryId"] != "sys-id" {
							t.Fatal(got)
						}
						delete(fields, "systemMemoryUrn")
						var old map[string]any
						if err := json.Unmarshal([]byte(wantFallback), &old); err != nil {
							t.Fatal(err)
						}
						a, _ := json.Marshal(fields)
						b, _ := json.Marshal(old)
						if string(a) != string(b) {
							t.Fatal("existing JSON fields changed")
						}
					} else if got != strings.Replace(wantFallback, "system memory: sys-id", "system memory: "+memoryURN+" (sys-id)", 1) {
						t.Fatal(got)
					}
				})
			}
		})
	}
}
