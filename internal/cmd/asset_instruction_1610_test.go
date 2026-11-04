package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestAssetInstruction1610Wire(t *testing.T) {
	for _, claim := range []string{"", "task:hrn:agenttask:example.com:app:task:one"} {
		t.Run(claim, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					OperationName string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if claim == "" {
					if req.OperationName != "SetAssetPublicLink" || len(req.Variables) != 2 {
						t.Errorf("legacy request changed: %+v", req)
					}
				} else {
					got, ok := req.Variables["instruction"].(map[string]any)
					if !ok || got["kind"] != "TASK" || got["ref"] != "hrn:agenttask:example.com:app:task:one" {
						t.Errorf("instruction not byte-exact: %+v", req)
					}
				}
				fmt.Fprint(w, `{"data":{"setAssetPublicLink":{"id":"a1","publicLinkEnabled":false,"canManagePublicLink":true,"publicLinkAvailable":false,"publicUrl":null}}}`)
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"asset", "public-link", "a1", "--off", "--json", "--server", srv.URL}
			if claim != "" {
				args = append(args, "--instruction", claim)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto) != 5 || dto["id"] != "a1" || dto["publicUrl"] != nil {
				t.Fatalf("DTO changed: %s", out.String())
			}
		})
	}
}

func TestAssetInstruction1610BadBeforeConfirmation(t *testing.T) {
	for _, raw := range []string{"", "task:", "unknown:ref", "task"} {
		t.Run(raw, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected network call") }))
			defer srv.Close()
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"asset", "public-link", "a1", "--on", "--instruction=" + raw, "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "instruction") {
				t.Fatalf("expected instruction usage before confirmation, got %v", err)
			}
			if calls != 0 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
