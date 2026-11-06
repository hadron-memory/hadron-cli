package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#1157: optional pins and diagnostics deferred from cli#1139 (#916 C1).

const (
	conflict1157 = "the compile conflicted with another change; check the compiled node with node get before compiling again"
	noResult1157 = "server returned no compiled boot prompt; check with node get before compiling again"
)

func TestCompileConflictSentenceExact1157(t *testing.T) {
	for _, c := range c1Commands {
		t.Run(c.name, func(t *testing.T) {
			r := c1Responses()
			r[c.op] = `{"errors":[{"message":"server conflict detail","extensions":{"code":"CONFLICT"}}]}`
			calls, out, err := runTeamTask(t, r, append(c.args, "--json")...)
			if err == nil || err.Error() != conflict1157 || exitCodeFor(err) != 5 {
				t.Fatalf("conflict recovery changed: %v (exit %d)", err, exitCodeFor(err))
			}
			c2Mutation(t, calls, c.op)
			if out != "" {
				t.Fatalf("stdout on a conflict: %q", out)
			}
		})
	}
}

func TestCompileEmptyResultIsExit1157(t *testing.T) {
	for _, c := range c1Commands {
		for name, body := range map[string]string{
			"null result": `null`,
			"null node":   `{"node":null,"revNo":7,"contentHash":"abc123","pointerSet":true,"approvalIssued":false}`,
			"empty urn":   `{"node":{"id":"n1","urn":""},"revNo":7,"contentHash":"abc123","pointerSet":true,"approvalIssued":false}`,
		} {
			t.Run(c.name+" "+name, func(t *testing.T) {
				r := c1Responses()
				r[c.op] = `{"data":{"` + c.field + `":` + body + `}}`
				calls, out, err := runTeamTask(t, r, append(c.args, "--json")...)
				if err == nil || err.Error() != noResult1157 || exitCodeFor(err) != 1 {
					t.Fatalf("empty result: %v (exit %d)", err, exitCodeFor(err))
				}
				c2Mutation(t, calls, c.op)
				if out != "" {
					t.Fatalf("an empty result printed a DTO: %q", out)
				}
			})
		}
	}
}

func TestCompileInvalidAppRefZeroRequests1157(t *testing.T) {
	for _, app := range []string{"hrn:agent:acme.com:iris", "not an app", "acme.com"} {
		t.Run(app, func(t *testing.T) {
			calls, out, err := runTeamTask(t, c1Responses(), "app", "agent", "compile", app, "hrn:agent:acme.com:support-bot")
			if exitCodeFor(err) != 2 || len(*calls) != 0 || out != "" {
				t.Fatalf("invalid App ref reached the server: %v calls=%v out=%q", err, *calls, out)
			}
		})
	}
}

// emptyServerBinding1157 blanks the binding's server: a binding that names no
// server cannot select an App for a worker name.
func emptyServerBinding1157(t *testing.T, server string) {
	t.Helper()
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blanked := strings.ReplaceAll(string(raw), `"server":"`+server+`"`, `"server":""`)
	if blanked == string(raw) {
		t.Fatal("binding server not found")
	}
	if err := os.WriteFile(path, []byte(blanked), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCompileWorkerNameEmptyServerBindingRefused1157(t *testing.T) {
	writeTeamBinding(t)
	responses := c1Responses()
	responses["Workers"] = `{"data":{"workers":{"total":1,"items":[` + irisWorkerJSON + `]}}}`
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	emptyServerBinding1157(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "worker", "compile", "Iris"})
	err := root.Execute()
	if exitCodeFor(err) != 2 || len(*calls) != 0 || out.String() != "" {
		t.Fatalf("empty-server binding selected an App: %v calls=%v out=%q", err, *calls, out.String())
	}
	if !strings.Contains(err.Error(), "--app") || !strings.Contains(err.Error(), "binding") {
		t.Fatalf("remedy lost: %v", err)
	}
}

func TestCompileWorkerNameExplicitAppOverForeignBinding1157(t *testing.T) {
	calls, out, err := c1ResolveRun(t, "foreign", "Iris", "--app", "hrn:app:acme.com:eng-team")
	if err != nil {
		t.Fatal(err)
	}
	read := c2Mutation(t, calls, "GetWorkerInApp")
	if read.Vars["appRef"] != "hrn:app:acme.com:eng-team" || read.Vars["ref"] != "Iris" || len(*calls) != 2 {
		t.Fatalf("explicit App lost to a foreign binding: %s calls=%d", read.RawVars, len(*calls))
	}
	if c2Mutation(t, calls, "CompileWorkerBootPrompt").Vars["workerRef"] != "wkr1" {
		t.Fatal("wrong compiled worker")
	}
	if !strings.Contains(out, "from --app") {
		t.Fatalf("source not reported: %s", out)
	}
}

func TestCompileLegacyWorkerURNIgnoresAmbient1157(t *testing.T) {
	ref := "urn:worker:acme.com:eng-team:iris"
	for _, mode := range []string{"unbound", "corrupt", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			calls, out, err := c1ResolveRun(t, mode, ref)
			if err != nil {
				t.Fatal(err)
			}
			get := c2Mutation(t, calls, "GetWorker")
			if get.Vars["ref"] != ref {
				t.Fatalf("legacy URN rewritten: %s", get.RawVars)
			}
			c2Mutation(t, calls, "CompileWorkerBootPrompt")
			if len(*calls) != 2 || !strings.Contains(out, "explicit worker reference") {
				t.Fatalf("legacy URN used ambient scope: calls=%v out=%s", *calls, out)
			}
		})
	}
}

// runCompile1157 sends the agent compile to a server that fails the request
// in the given way, and counts what reached it.
func runCompile1157(t *testing.T, h http.HandlerFunc) (int, error) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h(w, r)
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, c1Commands[0].args...))
	err := root.Execute()
	return int(hits.Load()), err
}

func TestCompileUnknownOutcomeNamesBoundedCause1157(t *testing.T) {
	const body = "private gateway body 10.0.0.7 stack trace"
	for _, tc := range []struct {
		name, cause string
		h           http.HandlerFunc
	}{
		{"gateway 502", "HTTP 502 from a gateway", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(body))
		}},
		{"gateway 504", "HTTP 504 from a gateway", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(body))
		}},
		{"dropped connection", "the connection dropped", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.(*net.TCPConn).SetLinger(0)
				_ = conn.Close()
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := runCompile1157(t, tc.h)
			if exitCodeFor(err) != 7 || hits != 1 {
				t.Fatalf("err=%v hits=%d", err, hits)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "the outcome is unknown (") || !strings.Contains(msg, tc.cause) {
				t.Fatalf("cause class missing: %q", msg)
			}
			if !strings.HasSuffix(msg, "); check the compiled node with node get before compiling again") {
				t.Fatalf("recovery sentence changed: %q", msg)
			}
			if strings.Contains(msg, "private gateway body") || strings.Contains(msg, "10.0.0.7") || strings.Contains(strings.ToLower(msg), "retry") {
				t.Fatalf("server body or retry advice leaked: %q", msg)
			}
		})
	}
}

func TestCompileHelpSynopsisLayout1157(t *testing.T) {
	for _, c := range c1Commands {
		t.Run(c.name, func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(c.args[:len(c.args)-1-btoi(c.name == "app")], "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			long := strings.SplitN(out.String(), "\nUsage:", 2)[0]
			for i, line := range strings.Split(long, "\n") {
				if len(line) > 80 {
					t.Errorf("line %d is %d columns: %q", i+1, len(line), line)
				}
			}
			workerOnly := strings.Contains(long, "or a name in the selected App")
			if workerOnly != (c.name == "worker") {
				t.Errorf("worker-name paragraph in the %s help: %v", c.name, workerOnly)
			}
			for _, want := range []string{"Sources and output addresses are chosen by the server", "Exit codes:", "never retries it."} {
				if !strings.Contains(long, want) {
					t.Errorf("synopsis lost %q", want)
				}
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
