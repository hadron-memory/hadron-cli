package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"net/http"
	"net/http/httptest"
	"os"
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
					Query         string
					Variables     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if claim == "" {
					if req.OperationName != "SetAssetPublicLink" || len(req.Variables) != 2 || strings.Contains(req.Query, "instruction") {
						t.Errorf("legacy request changed: %+v", req)
					}
				} else {
					if !strings.Contains(req.Query, "instruction: $instruction") {
						t.Errorf("instruction not passed in document: %s", req.Query)
					}
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
			if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "instruction") || strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("expected instruction usage before confirmation, got %v", err)
			}
			if calls != 0 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestAssetInstruction1610Refusals(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		for code, want := range map[string]int{"PERMISSION_DENIED": exitcode.Forbidden, "NOT_FOUND": exitcode.NotFound} {
			t.Run(fmt.Sprintf("%t/%s", instructed, code), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					fmt.Fprintf(w, `{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, code)
				}))
				defer srv.Close()
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"asset", "public-link", "a1", "--off", "--server", srv.URL}
				if instructed {
					args = append(args, "--instruction", "task:one")
				}
				root.SetArgs(args)
				if err := root.Execute(); exitCodeFor(err) != want {
					t.Fatalf("want %d got %v", want, err)
				}
				if calls != 1 {
					t.Fatalf("calls=%d; refusal retried", calls)
				}
			})
		}
	}
}

func TestAssetInstruction1610OnStillConfirms(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("write before confirmation") }))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "public-link", "a1", "--on", "--instruction", "task:one", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected confirmation refusal, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestAssetInstruction1610SessionAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, code         string
		instructed, usable bool
	}{
		{"person", "", false, true},
		{"worker", "", true, true},
		{"wrong-app", "PERMISSION_DENIED", true, true},
		{"concealed", "NOT_FOUND", true, true},
		{"unusable", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				want := ""
				if tc.instructed && tc.usable {
					want = "s-new"
				}
				if got := r.Header.Get("X-Hadron-Session"); got != want {
					t.Errorf("session=%q want %q", got, want)
				}
				var req struct{ OperationName string }
				_ = json.NewDecoder(r.Body).Decode(&req)
				if !strings.HasPrefix(req.OperationName, "SetAssetPublicLink") {
					t.Errorf("pre-call lookup: %s", req.OperationName)
				}
				if tc.code != "" {
					fmt.Fprintf(w, `{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, tc.code)
					return
				}
				fmt.Fprint(w, `{"data":{"setAssetPublicLink":{"id":"a1","publicLinkEnabled":false,"canManagePublicLink":true,"publicLinkAvailable":false,"publicUrl":null}}}`)
			}))
			defer srv.Close()
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			if !tc.usable {
				rewriteBinding1592(t, func(b map[string]any) { b["server"] = "http://other.invalid" })
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"asset", "public-link", "a1", "--off", "--server", srv.URL}
			if tc.instructed {
				args = append(args, "--instruction", "task:one")
			}
			root.SetArgs(args)
			err := root.Execute()
			want := 0
			if tc.code == "PERMISSION_DENIED" {
				want = 8
			}
			if tc.code == "NOT_FOUND" {
				want = 4
			}
			if exitCodeFor(err) != want {
				t.Fatalf("exit want %d got %v", want, err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d; lookup or fallback", calls)
			}
		})
	}
}

func TestAssetInstruction1610RebindDuringClientSetup(t *testing.T) {
	teamGitDir(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("X-Hadron-Session"); got != "" {
			t.Errorf("stale or successor session sent: %q", got)
		}
		fmt.Fprint(w, `{"data":{"setAssetPublicLink":{"id":"a1","publicLinkEnabled":false,"canManagePublicLink":true,"publicLinkAvailable":false,"publicUrl":null}}}`)
	}))
	defer srv.Close()
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "")
	f.TokenStoreFn = func() store.Store {
		rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "successor" })
		return memStore{auth.Host(srv.URL): "hdr_user_test"}
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "public-link", "a1", "--off", "--instruction", "task:one", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
