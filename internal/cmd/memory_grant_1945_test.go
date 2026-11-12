package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryGrant1945MarksAndReadbacks(t *testing.T) {
	for _, surface := range surfaceCases1338()[:6] {
		if surface.name == "grant register" || strings.HasPrefix(surface.name, "revoked") {
			continue
		}
		for _, state := range []struct {
			name, mark, reason string
			hidden             bool
		}{
			{"effective", "false", "null", false},
			{"system", "true", `"system"`, false},
			{"withheld-reason", "true", "null", true},
			{"concealed", "null", "null", true},
			{"future-reason", "true", `"future-rule"`, false},
		} {
			for _, jsonOut := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json=%t", surface.name, state.name, jsonOut), func(t *testing.T) {
					grant := strings.TrimSuffix(grant1338, "}") + `,"notEffective":` + state.mark + `,"notEffectiveReason":` + state.reason + `}`
					if state.hidden {
						grant = strings.Replace(grant, `"memoryUrn":"hrn:mem:acme.com:notes"`, `"memoryUrn":null`, 1)
					}
					if surface.name == "grant rm" {
						grant = strings.Replace(grant, `"revokedAt":null`, `"revokedAt":"2026-10-31T01:00:00Z"`, 1)
					}
					response := strings.Replace(surface.response, grant1338, grant, 1)
					if surface.name == "grant rm" {
						response = `{"revokeAppMemoryWriteGrant":` + grant + `}`
					}
					requests := make(chan struct{ Query, OperationName string }, 4)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req struct{ Query, OperationName string }
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							http.Error(w, "bad request", 400)
							return
						}
						requests <- req
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"data":`+response+`}`)
					}))
					t.Cleanup(srv.Close)
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := append([]string{}, surface.args...)
					if jsonOut {
						args = append(args, "--json")
					}
					root.SetArgs(append(args, "--server", srv.URL))
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					if len(requests) != 1 {
						t.Fatalf("operations=%d want1", len(requests))
					}
					req := <-requests
					if req.OperationName != surface.op {
						t.Fatalf("operation %s want %s", req.OperationName, surface.op)
					}
					for _, field := range []string{"notEffective", "notEffectiveReason"} {
						if !strings.Contains(req.Query, field) {
							t.Errorf("query missing %s", field)
						}
					}
					if jsonOut {
						var d map[string]any
						if surface.name == "grant list" {
							var rows []map[string]any
							if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
								t.Fatal(err)
							}
							if len(rows) != 1 {
								t.Fatal(rows)
							}
							d = rows[0]
						} else if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
							t.Fatal(err)
						}
						for key, want := range map[string]string{"notEffective": state.mark, "notEffectiveReason": state.reason} {
							value, ok := d[key]
							actual, _ := json.Marshal(value)
							if !ok || string(actual) != want {
								t.Errorf("%s=%s present=%t want %s", key, actual, ok, want)
							}
						}
						if state.hidden && d["notEffectiveReason"] != nil {
							t.Fatal("concealed reason leaked")
						}
					} else {
						marked := strings.Contains(out.String(), "not-effective")
						if marked != (state.mark == "true") {
							t.Fatalf("mark mismatch: %q", out.String())
						}
						if state.mark == "true" && state.reason != "null" && !strings.Contains(out.String(), strings.Trim(state.reason, `"`)) {
							t.Fatal(out.String())
						}
						if state.reason == "null" && strings.Contains(out.String(), "not-effective:") {
							t.Fatal("invented reason: ", out.String())
						}
						if state.name == "effective" && surface.name == "grant list" {
							want := "ID  MEMORY                  PREFIX   REGISTER  REVOKED\ng1  hrn:mem:acme.com:notes  reports  -         -\n"
							if out.String() != want {
								t.Fatalf("effective text changed: %q", out.String())
							}
						}
					}
				})
			}
		}
	}
}
