package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAgentSystemMemory1724(t *testing.T) {
	const memoryURN = "hrn:mem:other.example:server-returned"
	for _, asJSON := range []bool{false, true} {
		run := func(t *testing.T, memoryID, reply string, status int) (string, string, []string) {
			t.Helper()
			var mu sync.Mutex
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OperationName, Query string
					Variables            map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls = append(calls, req.OperationName)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch req.OperationName {
				case "GetAgent":
					fixture := strings.Replace(agentJSON, `"systemMemoryId":null`, `"systemMemoryId":`+memoryID, 1)
					fmt.Fprint(w, `{"data":{"agent":`+fixture+`}}`)
				case "AgentSystemMemory":
					if req.Variables["ref"] != "sys-id" || len(req.Variables) != 1 {
						t.Errorf("lookup variables: %v", req.Variables)
					}
					// The operation must stay a bounded, read-only projection.
					compact := strings.Join(strings.Fields(req.Query), " ")
					if !strings.Contains(compact, "memory(ref: $ref) { id urn }") || strings.Contains(compact, "mutation") {
						t.Errorf("projection: %s", compact)
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
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			root := NewRootCmd(f)
			args := []string{"agent", "get", "agent-ref", "--server", srv.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("optional lookup changed success: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			return out.String(), stderr.String(), append([]string(nil), calls...)
		}
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			baseline, stderr, calls := run(t, `null`, "", 200)
			if stderr != "" || len(calls) != 1 {
				t.Fatalf("unset: %q %v", stderr, calls)
			}
			if strings.Contains(baseline, "systemMemoryUrn") {
				t.Fatal(baseline)
			}
			// A missing/refused/failed lookup must retain exactly the old id-only output.
			wantFallback := strings.Replace(baseline, `"systemMemoryId":null`, `"systemMemoryId":"sys-id"`, 1)
			if !asJSON {
				wantFallback = strings.Replace(baseline, "system memory: -", "system memory: sys-id", 1)
			}
			for _, tc := range []struct {
				name, reply string
				status      int
			}{
				{"readable", `{"data":{"memory":{"id":"sys-id","urn":"` + memoryURN + `"}}}`, 200},
				{"missing", `{"data":{"memory":null}}`, 200},
				{"unreadable", `{"errors":[{"message":"PRIVATE_SENTINEL","extensions":{"code":"FORBIDDEN"}}],"data":{"memory":null}}`, 200},
				{"partial error", `{"errors":[{"message":"PRIVATE_SENTINEL"}],"data":{"memory":{"id":"sys-id","urn":"` + memoryURN + `"}}}`, 200},
				{"old server", `{"errors":[{"message":"Cannot query field urn on type Memory"}]}`, 200},
				{"outage", `PRIVATE_SENTINEL`, 503},
				{"malformed", `{`, 200},
				{"wrong id", `{"data":{"memory":{"id":"other","urn":"` + memoryURN + `"}}}`, 200},
				{"empty urn", `{"data":{"memory":{"id":"sys-id","urn":""}}}`, 200},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, stderr, calls := run(t, `"sys-id"`, tc.reply, tc.status)
					if stderr != "" || len(calls) != 2 || calls[0] != "GetAgent" || calls[1] != "AgentSystemMemory" {
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
						_ = json.Unmarshal([]byte(wantFallback), &old)
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
