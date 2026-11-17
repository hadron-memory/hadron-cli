package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type asWorkerResult1896 struct {
	calls          []attnCall
	stdout, stderr string
	exit           int
}

func runAsWorker1896(t *testing.T, responses map[string]string, hook func(string), args ...string) asWorkerResult1896 {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, responses, hook)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	var stderr bytes.Buffer
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	code := exitcode.OK
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	return asWorkerResult1896{append([]attnCall{}, (*calls)...), out.String(), stderr.String(), code}
}

func assertNoWrite1896(t *testing.T, r asWorkerResult1896) {
	t.Helper()
	for _, c := range r.calls {
		if c.Session != "" || (c.Op != "GetWorker" && c.Op != "TeamAppIdentity") {
			t.Fatalf("unexpected write/session before refusal: %+v", c)
		}
	}
}

// These are the worker-write and signing arms of the shared App helper.
// Other helper callers are pinned separately below. Lookup failures retain their exits.
func TestAsWorkerAppRefusalParity1896(t *testing.T) {
	for _, arm := range []string{"flag", "positional", "signing", "worker-target"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(arm+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				args := []string{"agent", "compile", "hrn:agent:acme.com:support-bot", "--as-worker", "--app", "hrn:app:acme.com:other"}
				if arm == "positional" {
					args = []string{"app", "agent", "compile", "hrn:app:acme.com:other", "hrn:agent:acme.com:support-bot", "--as-worker"}
				}
				if arm == "signing" {
					args = []string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "new template", "--app", "hrn:app:acme.com:other"}
				}
				if arm == "worker-target" {
					args = []string{"team", "worker", "compile", "hrn:worker:acme.com:other:iris", "--as-worker"}
				}
				if jsonMode {
					args = append(args, "--json")
				}
				// The missing-App result is the baseline every other kind is
				// compared with, so it is run explicitly first and does not
				// depend on the order of the kinds below (cli#2239 D-P3-3).
				run := func(kind string) asWorkerResult1896 {
					responses := c1Responses()
					if arm == "worker-target" {
						responses["GetWorker"] = strings.ReplaceAll(responses["GetWorker"], "capp100000000000000000000", "capp900000000000000000000")
					}
					switch kind {
					case "missing":
						responses["TeamAppIdentity"] = `{"data":{"app":null}}`
					case "forbidden":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"private App exists","extensions":{"code":"FORBIDDEN"}}]}`
					case "auth":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"sign in again","extensions":{"code":"UNAUTHENTICATED"}}]}`
					case "scope":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"credential needs account scope","extensions":{"code":"FORBIDDEN","reason":"OAUTH_SCOPE_INSUFFICIENT"}}]}`
					case "internal":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"lookup failed","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
					case "mixed":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"private App exists","extensions":{"code":"FORBIDDEN"}},{"message":"sign in again","extensions":{"code":"UNAUTHENTICATED"}}]}`
					}
					r := runAsWorker1896(t, responses, nil, args...)
					assertNoWrite1896(t, r)
					return r
				}
				missing := run("missing")
				if missing.exit != exitcode.Usage || !strings.Contains(missing.stdout+missing.stderr, "nothing was") {
					t.Fatalf("missing App: %+v", missing)
				}
				for _, kind := range []string{"forbidden", "auth", "scope", "internal", "mixed"} {
					r := run(kind)
					switch kind {
					case "forbidden":
						if r.exit != missing.exit || r.stdout != missing.stdout || r.stderr != missing.stderr {
							t.Fatalf("unreadable App differs from missing: got exit=%d stdout=%q stderr=%q; missing exit=%d stdout=%q stderr=%q", r.exit, r.stdout, r.stderr, missing.exit, missing.stdout, missing.stderr)
						}
					case "auth", "scope":
						if r.exit != exitcode.AuthRequired || !strings.Contains(r.stdout+r.stderr, "cannot confirm") {
							t.Fatalf("auth lookup reclassified: %+v", r)
						}
					case "internal":
						if r.exit != exitcode.Error || !strings.Contains(r.stdout+r.stderr, "cannot confirm") {
							t.Fatalf("internal lookup reclassified: %+v", r)
						}
					case "mixed":
						if r.stdout == missing.stdout && r.stderr == missing.stderr {
							t.Fatalf("mixed errors collapsed to missing App: %+v", r)
						}
					}
				}
			})
		}
	}
}

func TestAsWorkerPositionalLookupRebindStopsWrite1896(t *testing.T) {
	r := runAsWorker1896(t, c1Responses(), func(op string) {
		if op == "TeamAppIdentity" {
			swapSession(t)
		}
	}, "app", "agent", "compile", "hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot", "--as-worker")
	if r.exit != exitcode.Usage || !strings.Contains(r.stdout+r.stderr, "binding changed") {
		t.Fatalf("rebind not refused: %+v", r)
	}
	assertNoWrite1896(t, r)
}

func TestAsWorkerWorkerTargetApp1896(t *testing.T) {
	for _, target := range []string{"same", "different", "unreadable", "missing-app", "unknown-worker"} {
		for _, asWorker := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/person", true: "/worker"}[asWorker], func(t *testing.T) {
				responses := c1Responses()
				workerRef := "hrn:worker:acme.com:eng-team:iris"
				if target != "same" && target != "unknown-worker" {
					workerRef = "hrn:worker:acme.com:other:iris"
					responses["GetWorker"] = strings.ReplaceAll(responses["GetWorker"], "capp100000000000000000000", "capp900000000000000000000")
					responses["GetWorker"] = strings.ReplaceAll(responses["GetWorker"], "acme.com:eng-team:iris", "acme.com:other:iris")
					responses["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp900000000000000000000")
				}
				if target == "unreadable" {
					responses["TeamAppIdentity"] = `{"errors":[{"message":"private App exists","extensions":{"code":"FORBIDDEN"}}]}`
				}
				if target == "missing-app" {
					responses["TeamAppIdentity"] = `{"data":{"app":null}}`
				}
				if target == "unknown-worker" {
					responses["GetWorker"] = `{"data":{"worker":null}}`
				}
				args := []string{"team", "worker", "compile", workerRef}
				if asWorker {
					args = append(args, "--as-worker")
				}
				r := runAsWorker1896(t, responses, nil, args...)
				if target == "unknown-worker" {
					if r.exit != exitcode.NotFound || !strings.Contains(r.stdout+r.stderr, "worker is not available") {
						t.Fatalf("unknown worker changed: %+v", r)
					}
					assertNoWrite1896(t, r)
					return
				}
				if asWorker && target != "same" {
					if r.exit != exitcode.Usage || !strings.Contains(r.stdout+r.stderr, "nothing was sent") || strings.Contains(r.stdout+r.stderr, "acme.com:other") || strings.Contains(r.stdout+r.stderr, "private App exists") {
						t.Fatalf("cross-App worker should refuse without describing other App: %+v", r)
					}
					assertNoWrite1896(t, r)
					return
				}
				if r.exit != exitcode.OK {
					t.Fatalf("same App or person path refused: %+v", r)
				}
				writes := 0
				for _, c := range r.calls {
					if c.Op == "CompileWorkerBootPrompt" {
						writes++
						want := ""
						if asWorker {
							want = "s-new"
						}
						if c.Session != want || c.Vars["workerRef"] != "wkr1" {
							t.Fatalf("wrong compile attribution: %+v", c)
						}
					} else if c.Session != "" {
						t.Fatalf("read carried session: %+v", c)
					}
				}
				if writes != 1 {
					t.Fatalf("want one compile, got %+v", r.calls)
				}
			})
		}
	}
}

func TestAsWorkerWorkerResolutionRebindStopsWrite1896(t *testing.T) {
	r := runAsWorker1896(t, c1Responses(), func(op string) {
		if op == "GetWorker" {
			swapSession(t)
		}
	}, "team", "worker", "compile", "hrn:worker:acme.com:eng-team:iris", "--as-worker")
	if r.exit != exitcode.Usage || !strings.Contains(r.stdout+r.stderr, "binding changed") {
		t.Fatalf("worker resolution rebind not refused: %+v", r)
	}
	assertNoWrite1896(t, r)
}

// This exercises the new late-target guard with an ordinary App URN, not a
// fictional worker profile. A lookup can race a rebind after the first check.
func TestAsWorkerResolvedTargetLookupRebind1896(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, c1Responses(), func(op string) {
		if op == "TeamAppIdentity" {
			swapSession(t)
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	f.ServerFlag = srv.URL
	send, err := team.WorkerWriteSession(f)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := send(context.Background(), "hrn:app:acme.com:eng-team")
	if ctx != nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("late lookup rebind returned ctx=%v err=%v", ctx, err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "TeamAppIdentity" || (*calls)[0].Session != "" {
		t.Fatalf("unexpected lookup attribution: %+v", *calls)
	}
	// The failed check remains latched even after the original binding returns.
	swapSessionBack1896(t)
	if ctx, err = send(context.Background()); ctx != nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("restored binding resumed after refusal: ctx=%v err=%v", ctx, err)
	}
}

func swapSessionBack1896(t *testing.T) {
	t.Helper()
	rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "s-new" })
}

// The helper also serves grant reads/requests, task mine and optional write
// attribution. Missing/unreadable Apps agree at each caller; optional
// attribution sends the legitimate person write silently, without a session.
func TestBindingsAppOtherCallerParity1896(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		authored bool
	}{
		{"grant-read", []string{"team", "grant", "list"}, false},
		{"grant-request", []string{"team", "grant", "request", "--action", "node.read", "--values", `{"node":["n1"]}`, "--reason", "need it"}, false},
		{"task-mine", []string{"team", "task", "mine"}, false},
		{"write-attribution", []string{"node", "create", "-m", "acme.com:kb", "--loc", "test:authored", "--name", "Authored"}, true},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				args := append(append([]string{}, tc.args...), "--app", "hrn:app:acme.com:other")
				if jsonMode {
					args = append(args, "--json")
				}
				// Baseline run explicitly first: no kind depends on loop order
				// (cli#2239 D-P3-3).
				run := func(kind string) asWorkerResult1896 {
					responses := c1Responses()
					responses["CreateNode"] = `{"data":{"createNode":` + nodeJSON + `}}`
					switch kind {
					case "missing":
						responses["TeamAppIdentity"] = `{"data":{"app":null}}`
					case "forbidden":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"private App exists","extensions":{"code":"FORBIDDEN"}}]}`
					case "auth":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"sign in again","extensions":{"code":"UNAUTHENTICATED"}}]}`
					case "scope":
						responses["TeamAppIdentity"] = `{"errors":[{"message":"credential needs account scope","extensions":{"code":"FORBIDDEN","reason":"OAUTH_SCOPE_INSUFFICIENT"}}]}`
					}
					r := runAsWorker1896(t, responses, nil, args...)
					if tc.authored {
						if r.exit != exitcode.OK {
							t.Fatalf("person write refused: %+v", r)
						}
						writes := 0
						for _, c := range r.calls {
							if c.Session != "" {
								t.Fatalf("request attributed after failed App match: %+v", c)
							}
							if c.Op == "CreateNode" {
								writes++
							} else if c.Op != "TeamAppIdentity" {
								t.Fatalf("unexpected operation: %+v", c)
							}
						}
						if writes != 1 {
							t.Fatalf("expected one person write: %+v", r.calls)
						}
					} else {
						assertNoWrite1896(t, r)
						if len(r.calls) != 1 || r.calls[0].Op != "TeamAppIdentity" {
							t.Fatalf("unexpected lookup sequence: %+v", r.calls)
						}
					}
					return r
				}
				missing := run("missing")
				if tc.authored {
					if missing.stderr != "" {
						t.Fatalf("missing App should silently omit attribution: %q", missing.stderr)
					}
				} else if missing.exit != exitcode.Usage {
					t.Fatalf("missing App not usage: %+v", missing)
				}
				for _, kind := range []string{"forbidden", "auth", "scope"} {
					r := run(kind)
					switch kind {
					case "forbidden":
						if r.exit != missing.exit || r.stdout != missing.stdout || r.stderr != missing.stderr {
							t.Fatalf("unreadable differs from missing: got %+v; missing %+v", r, missing)
						}
					case "auth", "scope":
						if tc.authored {
							if strings.Count(r.stderr, "worker attribution unavailable") != 1 {
								t.Fatalf("credential failure note missing: %+v", r)
							}
						} else if r.exit != exitcode.AuthRequired {
							t.Fatalf("credential failure reclassified: %+v", r)
						}
					}
				}
			})
		}
	}
}
