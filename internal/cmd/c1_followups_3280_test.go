package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3280: two server codes the re-pinned schema brought in, and the memory set help.

const appMemoryRead3280 = `{"data":{"memory":{"id":"m9","urn":"hrn:mem:acme.com:coach-ada-personal","name":"coach-ada-personal","shortDescription":null,
	"class":"personal","visibility":null,"organizationId":"o1","isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-11-26T00:00:00Z"}}}`

func valueFile3280(t *testing.T) string {
	t.Helper()
	p := t.TempDir() + "/v"
	if err := os.WriteFile(p, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// APP_MEMORY_SLUG_FIXED (hadron-server#5184): updateMemory with a urn on an App personal or private memory is a request
// the server will not honour (there is no rename: hadron-cli#3186 was closed not planned). The caller's input is the
// trouble, so exit 2, with the server's message and serverCode.
func TestMemorySlugOnAnAppMemoryIsExit2WithTheServersMessage3280(t *testing.T) {
	reply := `{"errors":[{"message":"the slug of an App memory is fixed","extensions":{"code":"APP_MEMORY_SLUG_FIXED","memoryClass":"personal"}}],"data":null}`
	gql, _ := captureGraphQL(t, map[string]string{"GetMemory": appMemoryRead3280, "UpdateMemory": reply})
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	args := []string{"memory", "set", "hrn:mem:acme.com:coach-ada-personal", "--slug", "other", "--server", gql.URL}
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatal("the refusal must fail the command")
	}
	code := renderFailure(f, args, err)
	all := out.String() + stderr.String()
	if code != exitcode.Usage || !strings.Contains(all, "the slug of an App memory is fixed") {
		t.Errorf("exit %d (want 2), the server's message reaches the caller:\n%s", code, all)
	}
}

// SECRET_CHANGE_APP_LIMIT: at most 100 staged secret changes are open per App. It is the App's state, which no retry
// changes until some are completed or expire: exit 5, like SECRET_CHANGE_REQUEST_LIMIT.
func TestSecretChangeAppLimitIsExit53280(t *testing.T) {
	reply := `{"errors":[{"message":"too many open secret changes in this App","extensions":{"code":"SECRET_CHANGE_APP_LIMIT"}}],"data":null}`
	seen, code := renderedNoEcho3029(t, map[string]string{"CreateSecretProtected": reply},
		"secret", "create", "--name", "k", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", valueFile3280(t), "--protected")
	// The server's text is withheld (it can repeat a submitted value), as for SECRET_CHANGE_REQUEST_LIMIT:
	// a fixed sentence names the App-wide limit, and the exit class stays 5.
	if code != exitcode.Conflict || strings.Contains(seen, "too many open secret changes in this App") ||
		!strings.Contains(seen, "too many change requests are already open in this App") {
		t.Errorf("exit %d (want 5), the server's message must be withheld for the fixed sentence:\n%s", code, seen)
	}
	// cli#3305 item 3: the advice no longer sends the caller to withdraw a request they cannot read. The cap counts every
	// requester's open requests, and `secret change get` shows only the caller's own.
	for _, want := range []string{"counts every requester's open requests", "shows only your own"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the App-limit sentence must say %q:\n%s", want, seen)
		}
	}
	if strings.Contains(seen, "withdraw one (hadron secret change withdraw") {
		t.Errorf("the App-limit advice must not tell the caller to withdraw a request they may not see:\n%s", seen)
	}
}

// cli#3305 item 4: SECRET_CHANGE_REQUESTER_LIMIT exits 5 like its sibling caps and shows the SERVER's own message. Unlike
// the protected-change codes it is not withheld: the server's text for it is a constant ("Too many open secret changes
// for this requester.", changeRequests.ts:89) that cannot repeat a submitted value, and the number is never restated.
func TestSecretChangeRequesterLimitIsExit5WithServerText3305(t *testing.T) {
	reply := `{"errors":[{"message":"Too many open secret changes for this requester.","extensions":{"code":"SECRET_CHANGE_REQUESTER_LIMIT"}}],"data":null}`
	seen, code := renderedNoEcho3029(t, map[string]string{"CreateSecretProtected": reply},
		"secret", "create", "--name", "k", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", valueFile3280(t), "--protected")
	if code != exitcode.Conflict || !strings.Contains(seen, "Too many open secret changes for this requester.") {
		t.Errorf("exit %d (want 5), the server's own message must reach the caller:\n%s", code, seen)
	}
}

// Items 3 and 4: the memory set help names both exit-5 codes as the agentic-usage page does, and its attach sentence
// is the attach contract (cli#3185: any readable class but system, a read-only link), not the old "attach a memory you own".
func TestMemorySetHelpAndUsageAgreeOnExit5AndAttach3280(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "memory", "set")), " ")
	for _, want := range []string{"APP_USER_MEMORY_ALREADY_EXISTS", "MEMORY_URN_CONFLICT", "APP_MEMORY_SLUG_FIXED", "read-only link", "any class except system"} {
		if !strings.Contains(help, want) {
			t.Errorf("memory set --help must say %q:\n%s", want, help)
		}
	}
	// cli#3305: the code name AND its exit-2 association (the slug of an App memory is fixed).
	if !strings.Contains(help, "exit 2 (APP_MEMORY_SLUG_FIXED)") {
		t.Errorf("memory set --help must tie APP_MEMORY_SLUG_FIXED to exit 2:\n%s", help)
	}
	if strings.Contains(help, "To attach a personal or private memory you already have") {
		t.Errorf("the old attach sentence is stale against the attach contract:\n%s", help)
	}
}
