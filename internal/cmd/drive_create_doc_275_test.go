package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
			t.Run("options="+strconv.FormatBool(opts)+"/json="+strconv.FormatBool(asJSON), func(t *testing.T) {
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
	cases := [][]string{
		{"node", "--connection", "c"}, {"node", "--yes"},
		{"node", "--connection", " ", "--yes"}, {" ", "--connection", "c", "--yes"},
		{"node", "--connection", "c", "--folder", "", "--yes"},
		{"node", "--connection", "c", "--folder", " ", "--yes"},
		{"node", "--connection", "c", "--title", "", "--yes"},
		{"node", "--connection", "c", "--title", " ", "--yes"},
		{"--connection", "c", "--yes"}, {"node", "extra", "--connection", "c", "--yes"},
	}
	for i, tail := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			srv, n := createDocServer275(t, func(http.ResponseWriter, *http.Request, map[string]any) { t.Error("request before refusal") })
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(append([]string{"drive", "create-doc"}, tail...), "--server", srv.URL))
			if err := r.Execute(); exitCodeFor(err) != 2 {
				t.Fatalf("args %v: %v", tail, err)
			}
			if n.Load() != 0 {
				t.Fatalf("args %v: HTTP %d, want zero", tail, n.Load())
			}
		})
	}
	for _, answer := range []string{"n\n", "y\n"} {
		for _, withOptions := range []bool{false, true} {
			t.Run(strconv.Quote(answer)+strconv.FormatBool(withOptions), func(t *testing.T) {
				srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
					_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `}}`))
				})
				f, _, stderr := testFactoryTTY(t, answer)
				r := NewRootCmd(f)
				args := []string{"drive", "create-doc", "node-275-ref", "--connection", "conn-275-id", "--server", srv.URL}
				const folder = "folder-275\nforged-line"
				const title = "title-275\t\x1b"
				if withOptions {
					args = append(args, "--folder", folder, "--title", title)
				}
				r.SetArgs(args)
				wantCode, wantN := 6, int32(0)
				if answer == "y\n" {
					wantCode, wantN = 0, 1
				}
				if err := r.Execute(); exitCodeFor(err) != wantCode || n.Load() != wantN {
					t.Fatalf("err=%v HTTP=%d, want code=%d HTTP=%d", err, n.Load(), wantCode, wantN)
				}
				phrases := []string{"new Google Doc", "external", "cannot be recalled", strconv.Quote("node-275-ref"), strconv.Quote("conn-275-id")}
				if withOptions {
					phrases = append(phrases, strconv.Quote(folder), strconv.Quote(title))
				}
				for _, phrase := range phrases {
					if !strings.Contains(stderr.String(), phrase) {
						t.Fatalf("prompt lacks %q: %s", phrase, stderr)
					}
				}
				if strings.Contains(stderr.String(), "\x1b") || strings.Contains(stderr.String(), "folder-275\nforged-line") {
					t.Fatal("prompt contains unescaped controls")
				}
			})
		}
	}
}

func TestDriveCreateDoc275Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"NODE_NOT_FOUND", 4}, {"NOT_FOUND", 4}, {"SESSION_EXPIRED", 3}, {"FUTURE_DRIVE_REFUSAL", 1}} {
		t.Run(tc.code, func(t *testing.T) {
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
		})
	}
}

func TestDriveCreateDoc275UnknownOutcome(t *testing.T) {
	for _, mode := range []string{"gateway", "structured-5xx", "coded-refusal-5xx", "unavailable-200", "internal-200", "partial-200", "uncoded-200", "empty-id", "drop", "redirect307", "redirect308", "malformed", "null"} {
		t.Run(mode, func(t *testing.T) {
			srv, n := createDocServer275(t, func(w http.ResponseWriter, req *http.Request, _ map[string]any) {
				switch mode {
				case "gateway":
					http.Error(w, "gateway failed", http.StatusBadGateway)
				case "structured-5xx":
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"errors":[{"message":"request failed after dispatch","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
				case "coded-refusal-5xx":
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"errors":[{"message":"exact server refusal","extensions":{"code":"FORBIDDEN"}}]}`))
				case "unavailable-200":
					// Exercise the shared Unavailable mapping, not a Drive-specific server code.
					_, _ = w.Write([]byte(`{"errors":[{"message":"provider unavailable after dispatch","extensions":{"code":"GITHUB_UNAVAILABLE"}}]}`))
				case "internal-200":
					_, _ = w.Write([]byte(`{"errors":[{"message":"Unexpected error.","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"exportNodeToGoogleDoc":null}}`))
				case "partial-200":
					_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `},"errors":[{"message":"Unexpected error.","path":["exportNodeToGoogleDoc","name"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
				case "uncoded-200":
					_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
				case "empty-id":
					_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + strings.Replace(driveFile275, `"id":"f"`, `"id":""`, 1) + `}}`))
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
			wantMessage := map[string]string{"structured-5xx": "request failed after dispatch", "coded-refusal-5xx": "exact server refusal", "unavailable-200": "provider unavailable after dispatch", "internal-200": "Unexpected error.", "partial-200": "Unexpected error.", "uncoded-200": "boom"}[mode]
			if wantMessage != "" && (err == nil || !strings.Contains(err.Error(), "; server said: "+strconv.Quote(wantMessage))) {
				t.Fatalf("server message missing: %v", err)
			}
			if exitCodeFor(err) != 7 || err == nil || !strings.Contains(err.Error(), "the outcome is unknown; check the drive") || n.Load() != 1 || out.Len() != 0 {
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
	var stderr bytes.Buffer
	f.IOStreams.ErrOut = &stderr
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL,
		"--unlock", "hrn:mem:example.com:kb", "--unlock-passphrase-file", passFile1234(t, secret, 0o600)})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("HTTP=%d, want one", n.Load())
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("stdout contains the unlock passphrase")
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatal("stderr contains the unlock passphrase")
	}
}

func TestDriveCreateDoc275CancelledAttemptIsUnknown(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if mode == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			srv, n := createDocServer275(t, func(_ http.ResponseWriter, r *http.Request, _ map[string]any) {
				if mode == "cancel" {
					cancel()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
			})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL})
			err := r.ExecuteContext(ctx)
			if exitCodeFor(err) != 7 || err == nil || !strings.Contains(err.Error(), "the outcome is unknown; check the drive") || n.Load() != 1 || out.Len() != 0 {
				t.Fatalf("err=%v HTTP=%d stdout=%q", err, n.Load(), out.String())
			}
		})
	}
}
func TestDriveCreateDoc275Non200RefusalPreservesMessage(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		exit   int
	}{{http.StatusUnauthorized, "SESSION_EXPIRED", 3}, {http.StatusForbidden, "FORBIDDEN", 8}, {http.StatusBadRequest, "BAD_USER_INPUT", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "exact server refusal", "extensions": map[string]any{"code": tc.code}}}})
			})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL})
			err := r.Execute()
			if exitCodeFor(err) != tc.exit || err == nil || err.Error() != "exact server refusal" || n.Load() != 1 {
				t.Fatalf("err=%v HTTP=%d", err, n.Load())
			}
		})
	}
}
func TestDriveCreateDoc275ReferenceForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"  hrn:node:example.com:kb:notes ", "hrn:node:example.com:kb:notes"}, {"example.com::kb::notes", "hrn:node:example.com::kb::notes"}} {
		t.Run(tc.in, func(t *testing.T) {
			srv, _ := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, v map[string]any) {
				if v["nodeRef"] != tc.want {
					t.Errorf("nodeRef=%v, want %s", v["nodeRef"], tc.want)
				}
				_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + driveFile275 + `}}`))
			})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"drive", "create-doc", tc.in, "--connection", "c", "--yes", "--server", srv.URL})
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDriveCreateDoc275IncompleteMetadataPreservesID(t *testing.T) {
	srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = w.Write([]byte(`{"data":{"exportNodeToGoogleDoc":` + strings.Replace(driveFile275, `"parents":[]`, `"parents":null`, 1) + `}}`))
	})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL})
	err := r.Execute()
	if exitCodeFor(err) != 7 || err == nil || !strings.Contains(err.Error(), `Google Doc ID "f" but incomplete metadata`) || n.Load() != 1 || out.Len() != 0 {
		t.Fatalf("err=%v HTTP=%d stdout=%q", err, n.Load(), out.String())
	}
}

// An INTERNAL_SERVER_ERROR JSON envelope on a 200 is an unknown outcome in --json mode: the
// diagnostic is quoted data, and the command itself wrote nothing to stdout before the renderer.
func TestDriveCreateDoc275InternalServerErrorEnvelopeInJSONMode(t *testing.T) {
	const diagnostic = "provider line\nforged:\x1b[31m"
	srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": diagnostic, "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR"}}}})
	})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--json", "--server", srv.URL})
	err := r.Execute()
	if err == nil || n.Load() != 1 {
		t.Fatalf("err=%v HTTP=%d", err, n.Load())
	}
	if !strings.Contains(err.Error(), strconv.Quote(diagnostic)) || strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("diagnostic must be quoted data: %q", err.Error())
	}
	if out.Len() != 0 {
		t.Fatalf("the command must write nothing to stdout before the error renderer runs: %q", out.String())
	}
	renderError(f, err)
	var envelope struct {
		Error struct {
			Code       int    `json:"code"`
			ServerCode string `json:"serverCode"`
			Message    string `json:"message"`
		} `json:"error"`
	}
	if e := json.Unmarshal([]byte(out.String()), &envelope); e != nil {
		t.Fatal(e)
	}
	if envelope.Error.Code != 7 || envelope.Error.ServerCode != "INTERNAL_SERVER_ERROR" || !strings.Contains(envelope.Error.Message, "the outcome is unknown") {
		t.Fatalf("envelope: %s", out.String())
	}
}

// cli#2737 (BLIND delta on #2732): a bodyless HTTP 401 or 429 has no coded refusal, so create-doc
// conservatively treats the attempted write as unknown (exit 7, check the drive before retrying),
// as the plan documents. Four shapes: no body, and a plain-text body (quoted as data), each status.
func TestDriveCreateDoc2737BodylessAuthAndRateLimitAreUnknownOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		said   string // the quoted "server said" tail, empty when the body carried nothing
	}{
		{"401 no body", http.StatusUnauthorized, "", ""},
		{"401 plain text", http.StatusUnauthorized, "unauthorized", "unauthorized"},
		{"429 no body", http.StatusTooManyRequests, "", ""},
		{"429 plain text", http.StatusTooManyRequests, "slow down", "slow down"},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "", true: " json"}[asJSON], func(t *testing.T) {
				srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
					if tc.body != "" {
						w.Header().Set("Content-Type", "text/plain")
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				})
				f, out := testFactory(t)
				r := NewRootCmd(f)
				args := []string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				r.SetArgs(args)
				err := r.Execute()
				if err == nil || exitCodeFor(err) != 7 || n.Load() != 1 || out.Len() != 0 {
					t.Fatalf("want exit 7 after exactly one request and no stdout: err=%v exit=%d HTTP=%d stdout=%q", err, exitCodeFor(err), n.Load(), out.String())
				}
				if !strings.Contains(err.Error(), "the outcome is unknown; check the drive before retrying") {
					t.Errorf("the unknown-outcome guidance is missing: %v", err)
				}
				if (tc.said == "") == strings.Contains(err.Error(), "server said") || (tc.said != "" && !strings.Contains(err.Error(), "; server said: "+strconv.Quote(tc.said))) {
					t.Errorf("the body (if any) is quoted data after the guidance, nothing is invented for an empty one: %v", err)
				}
				if asJSON {
					renderError(f, err)
					var env struct {
						Error struct {
							Code    int    `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					if json.Unmarshal([]byte(out.String()), &env) != nil || env.Error.Code != 7 || !strings.Contains(env.Error.Message, "check the drive before retrying") {
						t.Errorf("--json envelope: %s", out.String())
					}
				}
			})
		}
	}
}

// cli#2737: a DEFINITE refusal keeps the shared mapping (exit 8, the server's message verbatim in the
// error), and what reaches the terminal goes through the shared renderer, which neutralises control
// characters (cli#2739): the line forgery in the server's text never reaches stderr, and in --json the
// document carries no raw ESC byte. Pinned at the command level so the create-doc refusal branch is
// covered, not only the renderer in isolation.
func TestDriveCreateDoc2737DefiniteRefusalWithControlCharactersIsNeutralisedAtRender(t *testing.T) {
	const message = "refused by drive\nforged: \x1b[31mred\x1b[0m \u202eevil"
	run := func(t *testing.T, asJSON bool) (error, *int32, string, string) {
		srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "FORBIDDEN"}}}})
		})
		f, out := testFactory(t)
		var errOut strings.Builder
		f.IOStreams.ErrOut = &errOut
		f.JSON = asJSON
		r := NewRootCmd(f)
		args := []string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--server", srv.URL}
		if asJSON {
			args = append(args, "--json")
		}
		r.SetArgs(args)
		err := r.Execute()
		if out.Len() != 0 {
			t.Fatalf("the command wrote to stdout before the renderer: %q", out.String())
		}
		renderError(f, err)
		c := n.Load()
		return err, &c, out.String(), errOut.String()
	}
	err, n, _, stderr := run(t, false)
	if err == nil || exitCodeFor(err) != 8 || *n != 1 {
		t.Fatalf("a definite refusal keeps its exit: err=%v exit=%d HTTP=%d", err, exitCodeFor(err), *n)
	}
	if r, bad := errTextHasControl2739(stderr); bad {
		t.Errorf("stderr carries %U: %q", r, stderr)
	}
	for _, want := range []string{"refused by drive", "forged", "red", "evil"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the visible words survive neutralisation (%q missing): %q", want, stderr)
		}
	}
	err, _, stdout, _ := run(t, true)
	if err == nil || exitCodeFor(err) != 8 {
		t.Fatalf("--json: %v", err)
	}
	if strings.ContainsRune(stdout, 0x1b) || strings.Contains(stdout, "\u202e") || strings.ContainsRune(stdout, 0x202e) {
		t.Errorf("the --json document must hold no raw ESC and no bidi override: %q", stdout)
	}
	var env struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(stdout), &env) != nil || env.Error.Code != 8 {
		t.Errorf("--json envelope: %q", stdout)
	}
}

// cli#2737: a GITHUB_UNAVAILABLE answer (the shared Unavailable mapping, not a Drive code) is an
// unknown outcome with guidance AND its server code survives into the --json envelope's serverCode.
func TestDriveCreateDoc2737UnavailableCodeSurvivesIntoTheJSONEnvelope(t *testing.T) {
	srv, n := createDocServer275(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"provider unavailable after dispatch","extensions":{"code":"GITHUB_UNAVAILABLE"}}]}`))
	})
	f, out := testFactory(t)
	f.JSON = true
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "create-doc", "node", "--connection", "c", "--yes", "--json", "--server", srv.URL})
	err := r.Execute()
	if err == nil || exitCodeFor(err) != 7 || n.Load() != 1 || out.Len() != 0 {
		t.Fatalf("err=%v exit=%d HTTP=%d stdout=%q", err, exitCodeFor(err), n.Load(), out.String())
	}
	renderError(f, err)
	var env struct {
		Error struct {
			Code       int    `json:"code"`
			ServerCode string `json:"serverCode"`
			Message    string `json:"message"`
		} `json:"error"`
	}
	if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
		t.Fatal(e)
	}
	if env.Error.Code != 7 || env.Error.ServerCode != "GITHUB_UNAVAILABLE" || !strings.Contains(env.Error.Message, "the outcome is unknown; check the drive before retrying") ||
		!strings.Contains(env.Error.Message, "provider unavailable after dispatch") {
		t.Fatalf("envelope: %s", out.String())
	}
}
