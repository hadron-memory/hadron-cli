package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssetPermission1530Wire(t *testing.T) {
	for _, verb := range []string{"enable", "disable"} {
		for _, status := range []int{200, 500} {
			for _, js := range []bool{false, true} {
				for _, tc := range []struct {
					code string
					exit int
				}{{"PERMISSION_DENIED", 8}, {"NOT_FOUND", 4}, {"INTERNAL_SERVER_ERROR", 1}} {
					t.Run(verb+tc.code+http.StatusText(status)+map[bool]string{true: "json", false: "text"}[js], func(t *testing.T) {
						var calls atomic.Int32
						srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(status)
							_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "not found forbidden permission denied", "extensions": map[string]any{"code": tc.code, "requestId": "req-1530"}}}})
						}))
						defer srv.Close()
						f, out := testFactory(t)
						root := NewRootCmd(f)
						args := []string{"app", "asset-upload", verb, "hrn:agent:acme.com:bot", "--yes", "--server", srv.URL}
						if js {
							args = append(args, "--json")
						}
						root.SetArgs(args)
						err := root.Execute()
						if err == nil {
							t.Fatal("expected refusal")
						}
						if got := renderError(f, err); got != tc.exit {
							t.Fatalf("exit %d want%d: %v", got, tc.exit, err)
						}
						if calls.Load() != 1 {
							t.Fatalf("calls %d", calls.Load())
						}
						if js {
							var env struct {
								Error struct {
									Code       int    `json:"code"`
									ServerCode string `json:"serverCode"`
									RequestID  string `json:"requestId"`
								} `json:"error"`
							}
							if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
								t.Fatal(err)
							}
							if env.Error.Code != tc.exit || env.Error.ServerCode != tc.code {
								t.Fatalf("bad envelope %s", out.String())
							}
							if tc.code == "INTERNAL_SERVER_ERROR" && env.Error.RequestID != "req-1530" {
								t.Fatalf("lost requestId %s", out.String())
							}
						}
						if strings.Contains(out.String(), "can't read") {
							t.Fatal("must not distinguish hidden from missing")
						}
					})
				}
			}
		}
	}
}
