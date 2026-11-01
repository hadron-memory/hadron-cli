package cmd

import (
	"encoding/json"
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

func runWhoamiSigning1525(t *testing.T, responses map[string]string, args ...string) (string, string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "session", "whoami"}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), errOut.String(), captured, err
}

func TestWhoamiSignatureAndTrailer1525(t *testing.T) {
	both := map[string]string{"GetWorkerRendered": renderedResp1525(jsonStr1525(renderedSig1525), jsonStr1525(renderedTrailer1525))}

	t.Run("--signature prints exactly the value", func(t *testing.T) {
		bindIris1525(t)
		out, _, captured, err := runWhoamiSigning1525(t, both, "--signature")
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
		out, _, _, err := runWhoamiSigning1525(t, both, "--trailer")
		if err != nil {
			t.Fatal(err)
		}
		if out != renderedTrailer1525+"\n" {
			t.Errorf("stdout = %q, want exactly the trailer and a newline", out)
		}
	})

	t.Run("--json carries only the one key", func(t *testing.T) {
		bindIris1525(t)
		out, _, _, err := runWhoamiSigning1525(t, both, "--signature", "--json")
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
		out, _, _, _ = runWhoamiSigning1525(t, both, "--trailer", "--json")
		dto = nil
		_ = json.Unmarshal([]byte(out), &dto)
		if len(dto) != 1 || dto["renderedTrailer"] != renderedTrailer1525 {
			t.Errorf("json = %v, want only renderedTrailer", dto)
		}
	})

	t.Run("an empty model segment is printed as the server sends it", func(t *testing.T) {
		bindIris1525(t)
		sig := "— [Iris, Backend Engineer](https://x/app/u/iris) · "
		out, _, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": renderedResp1525(jsonStr1525(sig), "null")}, "--signature")
		if err != nil {
			t.Fatal(err)
		}
		if out != sig+"\n" {
			t.Errorf("stdout = %q, want %q", out, sig+"\n")
		}
	})

	t.Run("null fails non-zero and prints no line", func(t *testing.T) {
		bindIris1525(t)
		out, _, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": renderedResp1525("null", "null")}, "--trailer")
		if err == nil {
			t.Fatalf("null must fail, got stdout %q", out)
		}
		if out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
		if !strings.Contains(err.Error(), "trailer") {
			t.Errorf("the error must name what is missing: %v", err)
		}
	})

	t.Run("an older server fails non-zero and says so", func(t *testing.T) {
		bindIris1525(t)
		old := `{"errors":[{"message":"Cannot query field \"renderedSignature\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
		out, _, _, err := runWhoamiSigning1525(t, map[string]string{"GetWorkerRendered": old}, "--signature")
		if err == nil || out != "" {
			t.Fatalf("must fail with nothing on stdout: err=%v out=%q", err, out)
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
		_, _, _, checkErr := runWhoamiSigning1525(t, responses, "--check")
		for _, flag := range []string{"--signature", "--trailer"} {
			teamGitDir(t)
			out, _, captured, err := runWhoamiSigning1525(t, responses, flag)
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
		out, _, captured, err := runWhoamiSigning1525(t, responses, "--signature")
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
		out, _, captured, err := runWhoamiSigning1525(t, responses, "--signature")
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
			out, _, captured, err := runWhoamiSigning1525(t, both, combo...)
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
		out, _, captured, err := runWhoamiSigning1525(t, map[string]string{"WorkerReporting": `{"errors":[{"message":"unavailable"}]}`}, "--json")
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
