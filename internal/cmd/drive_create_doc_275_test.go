package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Every HTTP request counts, including GETs and malformed/unexpected operations.
// No provider or real server is contacted by these command-level pins.
func createDocServer275(t *testing.T, reply func(http.ResponseWriter, *http.Request, map[string]any)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	n := &atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		var req struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("expected a GraphQL POST")
			http.Error(w, "unexpected request", 400)
			return
		}
		doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
		if err != nil {
			t.Error(err)
			http.Error(w, "invalid query", 400)
			return
		}
		op := doc.Operations.ForName(req.OperationName)
		if op == nil || op.Operation != ast.Mutation || len(op.SelectionSet) != 1 {
			t.Error("expected exactly one mutation field")
			http.Error(w, "wrong operation", 400)
			return
		}
		field, ok := op.SelectionSet[0].(*ast.Field)
		if !ok || field.Name != "exportNodeToGoogleDoc" {
			t.Error("unexpected mutation field")
			http.Error(w, "wrong field", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reply(w, r, req.Variables)
	}))
	t.Cleanup(s.Close)
	return s, n
}

func TestDriveCreateDoc275Success(t *testing.T) {
	for _, opts := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "default", true: "options"}[opts], map[bool]string{false: "text", true: "json"}[asJSON]}, "/"), func(t *testing.T) {
				srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, vars map[string]any) {
					want := map[string]any{"nodeRef": "hrn:node:example.com:kb:notes", "connectionRef": "c"}
					if opts {
						want["folderId"], want["title"] = "folder", " My title "
					}
					a, _ := json.Marshal(vars)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Errorf("variables %s, want %s", a, b)
					}
					_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `}}`))
				})
				f, out := testFactory(t)
				r := NewRootCmd(f)
				args := []string{"drive", "create-doc", "hrn:node:example.com:kb:notes", "--connection", "c", "--yes", "--server", srv.URL}
				if opts {
					args = append(args, "--folder", "folder", "--title", " My title ")
				}
				if asJSON {
					args = append(args, "--json")
				}
				r.SetArgs(args)
				if err := r.Execute(); err != nil {
					t.Fatal(err)
				}
				if n.Load() != 1 {
					t.Fatalf("HTTP requests %d, want 1", n.Load())
				}
				if asJSON {
					var got, want map[string]any
					if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
						t.Fatal(err)
					}
					_ = json.Unmarshal([]byte(driveFile275), &want)
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Fatalf("DTO %s, want %s", a, b)
					}
				} else if !strings.Contains(out.String(), `"size": null`) || !strings.Contains(out.String(), `"id": "f"`) {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestDriveCreateDoc275ConsentAndValidation(t *testing.T) {
	for _, tail := range [][]string{
		{"node", "--connection", "c"},
		{"node", "--yes"},
		{"node", "--connection", " ", "--yes"},
		{" ", "--connection", "c", "--yes"},
		{"node", "--connection", "c", "--folder", "", "--yes"},
		{"node", "--connection", "c", "--title", " ", "--yes"},
	} {
		srv, n := createDocServer275(t, func(http.ResponseWriter, *http.Request, map[string]any) { t.Error("request before refusal") })
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(append([]string{"drive", "create-doc"}, tail...), "--server", srv.URL))
		if err := r.Execute(); exitCodeFor(err) != 2 {
			t.Fatalf("args %v: %v", tail, err)
		}
		if n.Load() != 0 {
			t.Fatalf("args %v: HTTP requests %d, want 0", tail, n.Load())
		}
	}
	for _, answer := range []string{"n\n", "y\n"} {
		srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `}}`))
		})
		f, _, stderr := testFactoryTTY(t, answer)
		r := NewRootCmd(f)
		r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--server", srv.URL})
		wantCode, wantN := 6, int32(0)
		if answer == "y\n" {
			wantCode, wantN = 0, 1
		}
		if err := r.Execute(); exitCodeFor(err) != wantCode || n.Load() != wantN {
			t.Fatalf("answer %q: err %v, requests %d", answer, err, n.Load())
		}
		for _, phrase := range []string{"new Google Doc", "external", "cannot be recalled", "node", "c"} {
			if !strings.Contains(stderr.String(), phrase) {
				t.Fatalf("prompt lacks %q: %s", phrase, stderr)
			}
		}
	}
}

func TestDriveCreateDoc275Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"NODE_NOT_FOUND", 4}, {"NOT_FOUND", 4}, {"SESSION_EXPIRED", 3}, {"FUTURE_DRIVE_REFUSAL", 1}, {"GITHUB_UNAVAILABLE", 7}} {
		srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "exact server refusal", "extensions": map[string]any{"code": tc.code}}}})
		})
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL})
		err := r.Execute()
		if exitCodeFor(err) != tc.exit || err == nil || err.Error() != "exact server refusal" || n.Load() != 1 || out.Len() != 0 {
			t.Fatalf("%s: err=%v HTTP=%d stdout=%q", tc.code, err, n.Load(), out.String())
		}
	}
}

func TestDriveCreateDoc275UnknownOutcome(t *testing.T) {
	for _, mode := range []string{"gateway", "drop", "redirect307", "redirect308", "malformed", "null"} {
		t.Run(mode, func(t *testing.T) {
			srv, n := createDocServer275(t, func(w http.ResponseWriter, req *http.Request, _ map[string]any) {
				switch mode {
				case "gateway":
					http.Error(w, "gateway failed", http.StatusBadGateway)
				case "redirect307", "redirect308":
					status := http.StatusTemporaryRedirect
					if mode == "redirect308" {
						status = http.StatusPermanentRedirect
					}
					http.Redirect(w, req, "/replayed", status)
				case "drop":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "malformed":
					_, _ = w.Write([]byte("not JSON"))
				case "null":
					_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":null}}`))
				}
			})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL})
			err := r.Execute()
			if exitCodeFor(err) != 7 || err == nil || !strings.Contains(err.Error(), "the outcome is unknown; check the drive") || strings.Contains(err.Error(), "failed") || n.Load() != 1 || out.Len() != 0 {
				t.Fatalf("err=%v HTTP=%d stdout=%q", err, n.Load(), out.String())
			}
		})
	}
}

func TestDriveCreateDoc275UnlockIsHeaderOnly(t *testing.T) {
	const secret = "synthetic-unlock-275"
	srv, n := createDocServer275(t, func(w http.ResponseWriter, r *http.Request, vars map[string]any) {
		var keys []map[string]string
		if err := json.Unmarshal([]byte(r.Header.Get(api.MemoryKeysHeader)), &keys); err != nil {
			t.Error("missing or malformed memory key header")
			return
		}
		if len(keys) != 1 || keys[0]["memory"] != "hrn:mem:example.com:kb" || keys[0]["passphrase"] != secret {
			t.Error("unexpected invocation key")
		}
		if len(vars) != 2 {
			t.Error("unlock must not add mutation variables")
		}
		_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `}}`))
	})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL,
		"--unlock", "hrn:mem:example.com:kb", "--unlock-passphrase-file", passFile1234(t, secret, 0o600)})
	if err := r.Execute(); err != nil || n.Load() != 1 || strings.Contains(out.String(), secret) {
		t.Fatalf("err=%v HTTP=%d", err, n.Load())
	}
}
