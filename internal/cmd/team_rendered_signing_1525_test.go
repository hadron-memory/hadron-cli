package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1525 (hadron-server#2969, cor:agt:040:04): the server renders the
// worker's signature and trailer; the CLI prints them as given and never
// substitutes a variable itself.

const (
	renderedSig1525     = "— [Iris, Backend Engineer](https://hadronmemory.com/app/u/hrn:worker:acme.com:eng-team:iris) · claude-opus-5-5"
	renderedTrailer1525 = "Worker: Iris (backend-engineer) <hrn:worker:acme.com:eng-team:iris>"
	// The worker as its holder reads it: templates visible.
	signingWorkerJSON1525 = `{"id":"` + profileHexID + `","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"capp100000000000000000000","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":"You are Iris.","signature":"— [{{worker.name}}, {{worker.role}}]({{worker.url}}) · {{session.llmModel}}",
	"trailer":"Worker: {{worker.name}} ({{worker.role}}) <{{worker.urn}}>","memoryId":null,"family":null,"hasLiveSession":true,
	"reportsTo":null,"reportsToRetired":false,"repos":[],"checkStatus":null,"bootPromptRef":null,
	"retiredAt":null,"retiredBy":null,"createdAt":"2026-08-14T00:00:00Z","createdBy":null}`
)

func renderedResp1525(sig, trailer string) string {
	return `{"data":{"worker":{"renderedSignature":` + sig + `,"renderedTrailer":` + trailer + `}}}`
}

func jsonStr1525(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// runWorkerGetRendered1525 runs `team worker get` by URN with the given
// GetWorkerRendered answer.
func runWorkerGetRendered1525(t *testing.T, rendered string, args ...string) (string, *profileOpLog, error) {
	t.Helper()
	teamGitDir(t)
	log := &profileOpLog{}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		log.add(op)
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + signingWorkerJSON1525 + `}}`
		case "GetWorkerProfile":
			return profileNames
		case "GetWorkerRendered":
			return rendered
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris"}, append(args, "--server", gql.URL)...))
	err := root.Execute()
	return out.String(), log, err
}

func TestWorkerGetRenderedSigning1525(t *testing.T) {
	both := renderedResp1525(jsonStr1525(renderedSig1525), jsonStr1525(renderedTrailer1525))

	t.Run("text shows the rendered lines next to the templates", func(t *testing.T) {
		out, log, err := runWorkerGetRendered1525(t, both)
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, out)
		}
		if log.count("GetWorkerRendered") != 1 {
			t.Errorf("operations = %s, want one GetWorkerRendered", log)
		}
		for _, want := range []string{"Signature:\n" + renderedSig1525 + "\n", "Trailer:\n" + renderedTrailer1525 + "\n", "Signature template:\n— [{{worker.name}}"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("json always carries both keys", func(t *testing.T) {
		out, _, err := runWorkerGetRendered1525(t, both, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if dto["renderedSignature"] != renderedSig1525 || dto["renderedTrailer"] != renderedTrailer1525 {
			t.Errorf("rendered keys = %v / %v", dto["renderedSignature"], dto["renderedTrailer"])
		}
		if dto["signature"] == nil {
			t.Errorf("the raw template key must stay: %v", dto["signature"])
		}
	})

	t.Run("null is left out of text and is null in json", func(t *testing.T) {
		nulls := renderedResp1525("null", "null")
		out, _, err := runWorkerGetRendered1525(t, nulls)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "\nSignature:\n") || strings.Contains(out, "\nTrailer:\n") {
			t.Errorf("a null rendered line must print nothing:\n%s", out)
		}
		out, _, _ = runWorkerGetRendered1525(t, nulls, "--json")
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		for _, k := range []string{"renderedSignature", "renderedTrailer"} {
			if v, present := dto[k]; !present || v != nil {
				t.Errorf("%s = %#v (present=%v), want null", k, v, present)
			}
		}
	})

	t.Run("an older server reads as null, the lookup still works", func(t *testing.T) {
		old := `{"errors":[{"message":"Cannot query field \"renderedSignature\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
		out, _, err := runWorkerGetRendered1525(t, old, "--json")
		if err != nil {
			t.Fatalf("an older server must not fail worker get: %v", err)
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if v, present := dto["renderedSignature"]; !present || v != nil {
			t.Errorf("renderedSignature = %#v (present=%v), want null", v, present)
		}
	})

	t.Run("any other error fails the command", func(t *testing.T) {
		out, _, err := runWorkerGetRendered1525(t, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
		if err == nil {
			t.Fatalf("must fail, got:\n%s", out)
		}
	})
}

// --- whoami --signature / --trailer ---

// bindIris1525 writes a binding for worker wkr1 into a fresh git dir.
func bindIris1525(t *testing.T) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runWhoamiSigning1525(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "session", "whoami"}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

func TestWhoamiSignatureAndTrailer1525(t *testing.T) {
	both := map[string]string{"GetWorkerRendered": renderedResp1525(jsonStr1525(renderedSig1525), jsonStr1525(renderedTrailer1525))}

	t.Run("--signature prints exactly the value", func(t *testing.T) {
		bindIris1525(t)
		out, captured, err := runWhoamiSigning1525(t, both, "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if out != renderedSig1525+"\n" {
			t.Errorf("stdout = %q, want exactly the signature and a newline", out)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["GetWorkerRendered"], &vars)
		if vars["ref"] != "wkr1" {
			t.Errorf("must read the BOUND worker: vars %v", vars)
		}
	})

	t.Run("--trailer prints exactly the value", func(t *testing.T) {
		bindIris1525(t)
		out, _, err := runWhoamiSigning1525(t, both, "--trailer")
		if err != nil {
			t.Fatal(err)
		}
		if out != renderedTrailer1525+"\n" {
			t.Errorf("stdout = %q, want exactly the trailer and a newline", out)
		}
	})

	t.Run("--json carries only the one key", func(t *testing.T) {
		bindIris1525(t)
		out, _, err := runWhoamiSigning1525(t, both, "--signature", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if len(dto) != 1 || dto["renderedSignature"] != renderedSig1525 {
			t.Errorf("json = %v, want only renderedSignature", dto)
		}
		bindIris1525(t)
		out, _, _ = runWhoamiSigning1525(t, both, "--trailer", "--json")
		dto = nil
		_ = json.Unmarshal([]byte(out), &dto)
		if len(dto) != 1 || dto["renderedTrailer"] != renderedTrailer1525 {
			t.Errorf("json = %v, want only renderedTrailer", dto)
		}
	})

	t.Run("an empty model segment is printed as the server sends it", func(t *testing.T) {
		bindIris1525(t)
		sig := "— [Iris, Backend Engineer](https://x/app/u/iris) · "
		out, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": renderedResp1525(jsonStr1525(sig), "null")}, "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if out != sig+"\n" {
			t.Errorf("stdout = %q, want %q", out, sig+"\n")
		}
	})

	t.Run("null fails non-zero and prints no line", func(t *testing.T) {
		bindIris1525(t)
		out, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": renderedResp1525("null", "null")}, "--trailer")
		if err == nil {
			t.Fatalf("null must fail, got stdout %q", out)
		}
		if out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
		if exitCodeFor(err) != exitcode.Forbidden {
			t.Errorf("exit = %d, want %d (documented: withheld exits 8)", exitCodeFor(err), exitcode.Forbidden)
		}
		if !strings.Contains(err.Error(), "trailer") {
			t.Errorf("the error must name what is missing: %v", err)
		}
	})

	t.Run("an older server fails non-zero and says so", func(t *testing.T) {
		bindIris1525(t)
		old := `{"errors":[{"message":"Cannot query field \"renderedSignature\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
		out, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": old}, "--signature")
		if err == nil || out != "" {
			t.Fatalf("must fail with nothing on stdout: err=%v out=%q", err, out)
		}
		if exitCodeFor(err) != exitcode.Error {
			t.Errorf("exit = %d, want %d (documented: an older server exits 1)", exitCodeFor(err), exitcode.Error)
		}
		if !strings.Contains(err.Error(), "#2969") {
			t.Errorf("the error must name the server capability: %v", err)
		}
	})

	t.Run("no binding refuses like whoami --check", func(t *testing.T) {
		responses := map[string]string{
			"AuthContext":      whoamiAuthJSON,
			"TeamSessionsPage": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`,
		}
		teamGitDir(t)
		_, _, checkErr := runWhoamiSigning1525(t, responses, "--check")
		for _, flag := range []string{"--signature", "--trailer"} {
			teamGitDir(t)
			out, captured, err := runWhoamiSigning1525(t, responses, flag)
			if exitCodeFor(err) != exitCodeFor(checkErr) || exitCodeFor(err) != exitcode.NotFound {
				t.Errorf("%s exit = %d, --check exit = %d, want both %d", flag, exitCodeFor(err), exitCodeFor(checkErr), exitcode.NotFound)
			}
			if err == nil || checkErr == nil || err.Error() != checkErr.Error() {
				t.Errorf("%s error = %v, want the --check error %v", flag, err, checkErr)
			}
			if out != "" {
				t.Errorf("%s stdout = %q, want nothing", flag, out)
			}
			if _, asked := captured["GetWorkerRendered"]; asked {
				t.Errorf("%s read a worker with no session to read it for", flag)
			}
		}
	})

	t.Run("no binding but exactly one open session answers for it", func(t *testing.T) {
		teamGitDir(t)
		responses := map[string]string{
			"AuthContext": whoamiAuthJSON,
			"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` +
				whoamiSession("s-mine", "wkr9", "u-me", "Jonas", false) + `]}}}`,
			"GetWorkerRendered": both["GetWorkerRendered"],
		}
		out, captured, err := runWhoamiSigning1525(t, responses, "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if out != renderedSig1525+"\n" {
			t.Errorf("stdout = %q", out)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["GetWorkerRendered"], &vars)
		if vars["ref"] != "wkr9" {
			t.Errorf("must read the recovered session's worker: %v", vars)
		}
	})

	t.Run("no binding and several open sessions refuses rather than guessing", func(t *testing.T) {
		teamGitDir(t)
		responses := map[string]string{
			"AuthContext": whoamiAuthJSON,
			"TeamSessionsPage": `{"data":{"sessionsPage":{"total":2,"hasMore":false,"items":[` +
				whoamiSession("s-a", "wkr1", "u-me", "Jonas", false) + `,` +
				whoamiSession("s-b", "wkr2", "u-me", "Vera", false) + `]}}}`,
		}
		out, captured, err := runWhoamiSigning1525(t, responses, "--signature")
		if exitCodeFor(err) != exitcode.NotFound || out != "" {
			t.Errorf("exit = %d (want %d), stdout %q", exitCodeFor(err), exitcode.NotFound, out)
		}
		if _, asked := captured["GetWorkerRendered"]; asked {
			t.Errorf("must not pick one of several sessions")
		}
	})

	t.Run("the flags refuse each other and --check", func(t *testing.T) {
		for _, combo := range [][]string{{"--signature", "--trailer"}, {"--signature", "--check"}, {"--trailer", "--check"}} {
			bindIris1525(t)
			out, captured, err := runWhoamiSigning1525(t, both, combo...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Errorf("%v exit = %d, want %d", combo, exitCodeFor(err), exitcode.Usage)
			}
			if out != "" || len(captured) != 0 {
				t.Errorf("%v must refuse before any output or call: out %q, ops %v", combo, out, captured)
			}
		}
	})

	t.Run("plain whoami is unchanged and makes no rendered read", func(t *testing.T) {
		bindIris1525(t)
		// The default reporting read is best effort; a failure must not matter.
		out, captured, err := runWhoamiSigning1525(t, map[string]string{"WorkerReporting": `{"errors":[{"message":"unavailable"}]}`}, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if _, asked := captured["GetWorkerRendered"]; asked {
			t.Errorf("plain whoami must not read the rendered lines")
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if dto["source"] != "worktree" || dto["workerId"] != "wkr1" {
			t.Errorf("plain whoami json changed: %s", out)
		}
		if _, present := dto["renderedSignature"]; present {
			t.Errorf("plain whoami must not gain the rendered key: %s", out)
		}
	})
}

// --- cli#1529 LOCAL P2s: the session the read is for; mixed error envelopes ---

// sessionAwareRendered1525 is a fake server that records the X-Hadron-Session
// each operation carried and answers GetWorkerRendered the way the server does
// (hadron-server#2969 renderWorkerLines): from the recorded bind when the
// request names a session it holds a record for, else today's values. Other
// operations are answered from responses, then unstubbedDefault.
func sessionAwareRendered1525(t *testing.T, recorded map[string]string, responses map[string]string) (*httptest.Server, map[string]string) {
	t.Helper()
	headers := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		session := r.Header.Get("X-Hadron-Session")
		headers[body.OperationName] = session
		resp, ok := responses[body.OperationName]
		if body.OperationName == "GetWorkerRendered" {
			sig := "current-signature"
			if v, has := recorded[session]; has {
				sig = v
			}
			resp, ok = renderedResp1525(jsonStr1525(sig), jsonStr1525("trailer")), true
		}
		if !ok {
			resp, _ = unstubbedDefault(body.OperationName)
		}
		if resp == "" {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, headers
}

func runAgainst1525(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	err := root.Execute()
	return out.String(), err
}

func writeBinding1525(t *testing.T, binding string) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(binding), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderedReadCarriesTheSession1529(t *testing.T) {
	recorded := map[string]string{"s-new": "recorded-signature", "s-mine": "recorded-mine"}

	t.Run("whoami --signature reads as the bound session", func(t *testing.T) {
		bindIris1525(t)
		srv, headers := sessionAwareRendered1525(t, recorded, nil)
		out, err := runAgainst1525(t, srv, "team", "session", "whoami", "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if headers["GetWorkerRendered"] != "s-new" {
			t.Errorf("GetWorkerRendered session = %q, want the binding's s-new", headers["GetWorkerRendered"])
		}
		if out != "recorded-signature\n" {
			t.Errorf("stdout = %q, want the recorded bind's value", out)
		}
	})

	t.Run("whoami --signature with no binding reads as the one open session", func(t *testing.T) {
		teamGitDir(t)
		srv, headers := sessionAwareRendered1525(t, recorded, map[string]string{
			"AuthContext": whoamiAuthJSON,
			"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` +
				whoamiSession("s-mine", "wkr9", "u-me", "Jonas", false) + `]}}}`,
		})
		out, err := runAgainst1525(t, srv, "team", "session", "whoami", "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if headers["GetWorkerRendered"] != "s-mine" || out != "recorded-mine\n" {
			t.Errorf("session = %q, stdout = %q; want s-mine / recorded-mine", headers["GetWorkerRendered"], out)
		}
		// Only the rendered read is attributed; the lookups stay the caller's.
		if headers["TeamSessionsPage"] != "" || headers["AuthContext"] != "" {
			t.Errorf("the session must apply to the rendered read only: %v", headers)
		}
	})

	t.Run("a binding from another server refuses before any rendered read", func(t *testing.T) {
		writeBinding1525(t, `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
			"agentId":"agt1","appId":"capp100000000000000000000","startedAt":"2026-08-11T10:00:00Z","appBound":true,
			"server":"https://elsewhere.example","repo":"hadron-memory/hadron-cli","prNumbers":[]}`)
		srv, headers := sessionAwareRendered1525(t, recorded, nil)
		out, err := runAgainst1525(t, srv, "team", "session", "whoami", "--trailer")
		if exitCodeFor(err) != exitcode.Usage || out != "" {
			t.Errorf("exit = %d (want %d), stdout %q", exitCodeFor(err), exitcode.Usage, out)
		}
		if _, asked := headers["GetWorkerRendered"]; asked {
			t.Errorf("must not read another deployment's worker id here")
		}
	})

	ownBinding := `{"sessionId":"s-new","workerId":"` + profileHexID + `","workerName":"Iris","workerRole":"backend-engineer",
		"agentId":"agt1","appId":"capp100000000000000000000","startedAt":"2026-08-11T10:00:00Z","appBound":true,
		"repo":"hadron-memory/hadron-cli","prNumbers":[]}`
	workerResponses := map[string]string{
		"GetWorker":        `{"data":{"worker":` + signingWorkerJSON1525 + `}}`,
		"GetWorkerProfile": profileNames,
	}

	t.Run("worker get of your own bound worker reads as your session", func(t *testing.T) {
		writeBinding1525(t, ownBinding)
		srv, headers := sessionAwareRendered1525(t, recorded, workerResponses)
		out, err := runAgainst1525(t, srv, "team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if headers["GetWorkerRendered"] != "s-new" || dto["renderedSignature"] != "recorded-signature" {
			t.Errorf("session = %q, renderedSignature = %v; want s-new / recorded-signature", headers["GetWorkerRendered"], dto["renderedSignature"])
		}
		if headers["GetWorker"] != "" || headers["GetWorkerProfile"] != "" {
			t.Errorf("the session must apply to the rendered read only: %v", headers)
		}
	})

	t.Run("worker get of another worker carries no session", func(t *testing.T) {
		bindIris1525(t) // bound to wkr1, not the worker read
		srv, headers := sessionAwareRendered1525(t, recorded, workerResponses)
		out, err := runAgainst1525(t, srv, "team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if headers["GetWorkerRendered"] != "" || dto["renderedSignature"] != "current-signature" {
			t.Errorf("session = %q, renderedSignature = %v; want none / current-signature", headers["GetWorkerRendered"], dto["renderedSignature"])
		}
	})
}

func TestRenderedOldServerToleranceIsExact1529(t *testing.T) {
	unknown := `{"message":"Cannot query field \"renderedSignature\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
	forbidden := `{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}`

	t.Run("worker get: unknown field mixed with FORBIDDEN fails", func(t *testing.T) {
		out, _, err := runWorkerGetRendered1525(t, `{"errors":[`+unknown+`,`+forbidden+`]}`, "--json")
		if err == nil {
			t.Fatalf("a mixed envelope must not read as an old server:\n%s", out)
		}
		if exitCodeFor(err) == exitcode.OK {
			t.Errorf("exit = %d, want non-zero", exitCodeFor(err))
		}
	})

	t.Run("whoami --signature: unknown field mixed with FORBIDDEN is not the old-server exit", func(t *testing.T) {
		bindIris1525(t)
		out, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": `{"errors":[` + forbidden + `,` + unknown + `]}`}, "--signature")
		if err == nil || out != "" {
			t.Fatalf("must fail with nothing on stdout: err=%v out=%q", err, out)
		}
		// Which code the mixed envelope maps to is MapError's call; what is
		// pinned here is that it is NOT the old-server answer (exit 1, #2969).
		if strings.Contains(err.Error(), "#2969") || exitCodeFor(err) == exitcode.Error {
			t.Errorf("exit = %d, err = %v; must not be the old-server answer", exitCodeFor(err), err)
		}
	})

	t.Run("both new fields unknown is still the old server", func(t *testing.T) {
		trailerUnknown := `{"message":"Cannot query field \"renderedTrailer\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
		_, _, err := runWorkerGetRendered1525(t, `{"errors":[`+unknown+`,`+trailerUnknown+`]}`, "--json")
		if err != nil {
			t.Fatalf("an older server must not fail worker get: %v", err)
		}
	})
}
