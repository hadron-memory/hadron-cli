package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkerQuietServerReasonAndAge(t *testing.T) {
	for _, surface := range []string{"worker", "roster"} {
		for _, tc := range []struct{ name, status, reason, age, want string }{
			{"idle thirty hours", "NO_SESSION", "IDLE", "30h10m", "NO_SESSION (open, quiet 30h)"},
			{"idle five minutes", "NO_SESSION", "IDLE", "5m10s", "NO_SESSION (open, quiet 5m)"},
			{"ended", "NO_SESSION", "ENDED", "30h10m", "NO_SESSION (ended)"},
			{"never bound", "NO_SESSION", "NEVER_BOUND", "", "NO_SESSION (never bound)"},
			{"old activity is not idle", "OK", "", "30h10m", "OK"},
			{"missing reason", "NO_SESSION", "", "30h10m", "NO_SESSION"},
			{"idle without activity", "NO_SESSION", "IDLE", "", "NO_SESSION (open, quiet)"},
		} {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				var at *string
				if tc.age != "" {
					d, err := time.ParseDuration(tc.age)
					if err != nil {
						t.Fatal(err)
					}
					s := time.Now().Add(-d).UTC().Format(time.RFC3339)
					at = &s
				}
				check := map[string]any{"status": tc.status, "pendingReason": nil, "lastCheckedAt": nil, "noSessionReason": nil}
				if tc.reason != "" {
					check["noSessionReason"] = tc.reason
				}
				var base map[string]any
				fixture := profileWorkerJSON
				if surface == "roster" {
					fixture = rosterRow993("Iris", "UNAVAILABLE", false, "null", "null")
				}
				if err := json.Unmarshal([]byte(fixture), &base); err != nil {
					t.Fatal(err)
				}
				base["checkStatus"] = check
				base["lastActiveAt"] = at
				if surface == "worker" {
					base["hasLiveSession"] = false
				} else {
					base["liveSession"] = false
				}
				raw, err := json.Marshal(base)
				if err != nil {
					t.Fatal(err)
				}
				for _, jsonMode := range []bool{false, true} {
					var out string
					if surface == "worker" {
						args := []string{profileHexID}
						if jsonMode {
							args = append(args, "--json")
						}
						out, _, err = runWorkerGet(t, string(raw), profileNames, args...)
					} else {
						args := []string{"team", "roster"}
						if jsonMode {
							args = append(args, "--json")
						}
						_, out, err = runTeamTask(t, rosterResponses993(string(raw)), args...)
					}
					if err != nil {
						t.Fatalf("json=%v: %v\n%s", jsonMode, err, out)
					}
					if jsonMode {
						var dto map[string]any
						if err := json.Unmarshal([]byte(out), &dto); err != nil {
							t.Fatal(err)
						}
						if surface == "roster" {
							dto = dto["workers"].([]any)[0].(map[string]any)
						}
						got := dto["checkStatus"].(map[string]any)
						if got["status"] != tc.status || got["noSessionReason"] != check["noSessionReason"] {
							t.Errorf("raw server distinction lost: %v", got)
						}
						if at != nil && dto["lastActiveAt"] != *at {
							t.Errorf("absolute timestamp lost: %v", dto["lastActiveAt"])
						}
						if _, ok := got["noSessionReason"]; !ok {
							t.Error("missing nullable noSessionReason")
						}
					} else {
						if !strings.Contains(out, tc.want) {
							t.Errorf("missing %q:\n%s", tc.want, out)
						}
						if tc.reason == "" && strings.Contains(out, "open, quiet") {
							t.Errorf("classification inferred from age:\n%s", out)
						}
						if surface == "roster" && !strings.Contains(out, "UNAVAILABLE") {
							t.Errorf("stored unavailable state changed:\n%s", out)
						}
					}
				}
			})
		}
	}
}

func TestWorkerQuietQueriesSelectServerReason(t *testing.T) {
	for _, surface := range []string{"worker", "roster"} {
		t.Run(surface, func(t *testing.T) {
			teamGitDir(t)
			responses := rosterResponses993(rosterRow993("Iris", "AVAILABLE", true, "null", "null"))
			responses["GetWorker"] = `{"data":{"worker":` + profileWorkerJSON + `}}`
			responses["GetWorkerProfile"] = profileNames
			srv, calls := newLintFake2007(t, func(op string, _ map[string]any) string { return responses[op] })
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "roster", "--app", "acme.com:eng-team", "--server", srv.URL}
			op := "TeamRoster"
			if surface == "worker" {
				args = []string{"team", "worker", "get", profileHexID, "--server", srv.URL}
				op = "GetWorker"
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			reads := calls.ops(op)
			if len(reads) != 1 {
				t.Fatalf("reads=%d", len(reads))
			}
			if !strings.Contains(reads[0].Query, "noSessionReason") {
				t.Error("query does not select noSessionReason: fixture fields must be requested on the wire")
			}
		})
	}
}
