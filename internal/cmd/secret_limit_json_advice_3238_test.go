package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3238: the two App-limit refusals of a staged secret change. #3291 (cli#3280) and #3317 (cli#3305) mapped them
// to exit 5; these pins add what #3238 still asked for: --json carries the server code for EACH of them (the stable
// signal an agent branches on), and the requester refusal says what to do without saying "this secret".
func jsonRefusal3238(t *testing.T, serverCode, message string) (map[string]any, string, int) {
	t.Helper()
	reply := `{"errors":[{"message":"` + message + `","extensions":{"code":"` + serverCode + `"}}],"data":null}`
	gql, _ := captureGraphQL(t, map[string]string{"CreateSecretProtected": reply})
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	args := []string{"secret", "create", "--name", "k", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic",
		"--value-file", valueFile3280(t), "--protected", "--server", gql.URL, "--json"}
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("%s: want a refusal", serverCode)
	}
	code := renderFailure(f, args, err)
	raw := out.String() + stderr.String()
	var env struct {
		Error map[string]any `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out.String()+stderr.String())), &env); jerr != nil {
		t.Fatalf("%s: --json is not one envelope (%v):\n%s", serverCode, jerr, raw)
	}
	return env.Error, raw, code
}

func TestSecretChangeAppLimitJSONCarriesServerCode3238(t *testing.T) {
	e, raw, code := jsonRefusal3238(t, "SECRET_CHANGE_APP_LIMIT", "too many open secret changes in this App")
	if code != exitcode.Conflict || e["serverCode"] != "SECRET_CHANGE_APP_LIMIT" {
		t.Errorf("exit %d (want 5), serverCode %v:\n%s", code, e["serverCode"], raw)
	}
}

func TestSecretChangeRequesterLimitJSONCarriesServerCode3238(t *testing.T) {
	e, raw, code := jsonRefusal3238(t, "SECRET_CHANGE_REQUESTER_LIMIT", "Too many open secret changes for this requester.")
	if code != exitcode.Conflict || e["serverCode"] != "SECRET_CHANGE_REQUESTER_LIMIT" {
		t.Errorf("exit %d (want 5), serverCode %v:\n%s", code, e["serverCode"], raw)
	}
}

// The requester refusal keeps the server's own sentence (cli#3305) and adds what to do, without "this secret": the
// cap is raised both for one secret and for a requester's share of the App.
func TestSecretChangeRequesterLimitAdvice3238(t *testing.T) {
	const server = "Too many open secret changes for this requester."
	reply := `{"errors":[{"message":"` + server + `","extensions":{"code":"SECRET_CHANGE_REQUESTER_LIMIT"}}],"data":null}`
	seen, code := renderedNoEcho3029(t, map[string]string{"CreateSecretProtected": reply},
		"secret", "create", "--name", "k", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", valueFile3280(t), "--protected")
	if code != exitcode.Conflict || !strings.Contains(seen, server) {
		t.Fatalf("exit %d (want 5), the server's sentence must stay:\n%s", code, seen)
	}
	for _, want := range []string{"Withdraw one of your open requests", "hadron secret change withdraw <request-id>", "wait for one to expire"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the requester refusal must say %q:\n%s", want, seen)
		}
	}
	if strings.Contains(strings.ReplaceAll(seen, server, ""), "this secret") {
		t.Errorf("the added advice must not say \"this secret\":\n%s", seen)
	}
}
