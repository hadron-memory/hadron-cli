package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #643 — `--content -` read from an INTERACTIVE TERMINAL is refused.
//
// A terminal in canonical mode buffers a line discipline (typically 4 KB) and
// silently loses the overflow, so an agent driving the CLI through a PTY could
// hand over a 10 KB document, see exit 0, and have a truncated node stored.
// Observed on a ~10 KB report: sections missing, others joined together.
//
// The refusal must happen BEFORE the write. A guard that fired after the
// request would be describing corruption already committed.
func TestNodeContentStdinRefusesATerminal(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		op   string
	}{
		{"add", []string{"node", "add", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N", "--content", "-"}, "CreateNode"},
		{"update", []string{"node", "update", "acme.com::kb::x:y", "--content", "-"}, "UpdateNode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
				"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
				// `node update` resolves the ref BEFORE reading content, so the
				// fake must answer it. The guard still has to fire before the
				// WRITE, which the captured-op assertion below is what checks.
				"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"m1"}}}`,
			})
			f, _, _ := testFactoryTTY(t, "some typed content\n")
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("--content - from a terminal must be refused")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			if _, ran := captured[tc.op]; ran {
				t.Error("the refusal must happen BEFORE the write, not after")
			}
			// And before any network call at all. `node update` resolves the
			// ref and fetches the node before building its input, so without
			// the early guard this argument error would cost two round trips
			// before being rejected. Nothing here should reach the server.
			if len(captured) != 0 {
				ops := make([]string, 0, len(captured))
				for op := range captured {
					ops = append(ops, op)
				}
				t.Errorf("a refusal on argument grounds must make no requests, got %v", ops)
			}
		})
	}
}

// The diagnostic has to name the remedy, not just the problem — an agent that
// hits this needs to know which flag to use instead. Asserted on the rendered
// message the user actually sees.
func TestNodeContentStdinRefusalNamesTheRemedy(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _, errOut := testFactoryTTY(t, "x\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x:y",
		"--name", "N", "--content", "-", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	_ = renderError(f, err)
	msg := errOut.String()
	// "--content-file <path>" as a PHRASE, not the bare token. A bare-token
	// assertion passed a mutation that dropped the remedy sentence entirely:
	// removing one %s made Go emit `%!(EXTRA string=--content-file)`, which
	// still contains the token. The test was satisfied by a formatting fault.
	for _, want := range []string{"--content-file <path>", "interactive terminal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must mention %q:\n%s", want, msg)
		}
	}
	// And the message must be a message, not a format error.
	if strings.Contains(msg, "%!") {
		t.Errorf("the refusal has a formatting fault:\n%s", msg)
	}
}

// THE regression this guard must not cause: a PIPE is not a terminal, and the
// documented form (`cat file | hadron node add … --content -`) has to keep
// working. testFactory's streams are non-terminal, which is the piped case.
func TestNodeContentStdinStillWorksWhenPiped(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("piped body\nsecond line\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x:y",
		"--name", "N", "--content", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a piped --content - must still work: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if got, _ := vars.Input["content"].(string); got != "piped body\nsecond line\n" {
		t.Errorf("piped content must reach the wire verbatim, got %q", got)
	}
}

// The remedy itself must not be blocked. --content-file reads a path and never
// touches stdin, so it has to work from a terminal — otherwise the refusal
// above would point at a door that is also shut.
func TestNodeContentFileWorksFromATerminal(t *testing.T) {
	path := t.TempDir() + "/body.md"
	if err := os.WriteFile(path, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _, _ := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "add", "-m", "acme.com::kb", "--loc", "x:y",
		"--name", "N", "--content-file", path, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--content-file must work from a terminal: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if got, _ := vars.Input["content"].(string); got != "from a file\n" {
		t.Errorf("content = %q", got)
	}
}

// NARROWNESS. The guard is on DOCUMENT reads, not on "stdin is a terminal".
//
// `memory encrypt --data-key -` reads a SECRET from a terminal deliberately —
// that is the recommended form, precisely so the key never enters argv or
// shell history — and a key is short and single-line. Guarding it would break
// the safe path it exists to offer, which is why cmdutil.ReadDocumentStdin is
// opt-in per call site rather than applied to every `-`.
//
// This test is what keeps that distinction true: a later sweep that wired the
// helper into every `-` reader would turn it red. It asserts only that the
// DOCUMENT refusal is absent — the command may still fail for its own reasons,
// which is not what is under test here.
func TestDataKeyStdinIsNotGuardedByTheDocumentRule(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"EncryptMemory": `{"data":{"encryptMemory":{"id":"m1","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
		"GetMemory":     `{"data":{"memory":{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1","isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-09-20T00:00:00Z"}}}`,
	})
	f, _, errOut := testFactoryTTY(t, "SGVsbG9LZXlIZWxsb0tleQ==\n")
	root := NewRootCmd(f)
	// --yes is load-bearing HERE, not boilerplate. Without it the command
	// prompts, testFactoryTTY's script is consumed as the CONFIRMATION answer,
	// the non-affirmative reply aborts the command, and stdin is never read —
	// so this test would stay green even if the document guard were wired into
	// the secret path. It was written that way and @copilot caught it (#647):
	// a false-negative control in the one test whose only job is to fail when
	// a future sweep goes too far.
	root.SetArgs([]string{"memory", "encrypt", "acme.com::kb", "--data-key", "-", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		_ = renderError(f, err)
		t.Fatalf("a secret read from a terminal must SUCCEED: %v\n%s", err, errOut.String())
	}
	// Asserting success is what makes this a real control: the read has to have
	// happened for the mutation to be able to break it.
	if _, ran := captured["EncryptMemory"]; !ran {
		t.Error("the encrypt mutation must have run, i.e. stdin was actually read")
	}
	if msg := errOut.String(); strings.Contains(msg, "interactive terminal") {
		t.Errorf("a SECRET read from a terminal must not hit the document guard:\n%s", msg)
	}
}

// #648 — `hadron api -` reads a GraphQL DOCUMENT, so it takes the same
// terminal refusal as `--content -`: before any request, exit 2, naming the
// --input remedy as a phrase.
func TestAPIStdinRefusesATerminal(t *testing.T) {
	requests := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(gql.Close)
	f, _, errOut := testFactoryTTY(t, "query { me { id } }\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "-", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("hadron api - from a terminal must be refused")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
	}
	if requests != 0 {
		t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
	}
	msg := errOut.String()
	for _, want := range []string{"--input <path>", "interactive terminal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must mention %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "%!") {
		t.Errorf("the refusal has a formatting fault:\n%s", msg)
	}
}

// The documented form (`cat op.graphql | hadron api -`) must keep working,
// and the document must reach the wire verbatim.
func TestAPIStdinStillWorksWhenPiped(t *testing.T) {
	const doc = "query {\n  me { id }\n}\n"
	var sent string
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = body.Query
		_, _ = w.Write([]byte(`{"data":{"me":{"id":"u1"}}}`))
	}))
	t.Cleanup(gql.Close)
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(doc)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a piped hadron api - must still work: %v", err)
	}
	if sent != doc {
		t.Errorf("piped document must reach the wire verbatim, got %q", sent)
	}
}

// The remedy must not be blocked: --input reads a path, so it works from a
// terminal — as does a query passed inline, which never touches stdin.
func TestAPIInputAndInlineWorkFromATerminal(t *testing.T) {
	path := t.TempDir() + "/op.graphql"
	if err := os.WriteFile(path, []byte("query { me { id } }"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"api", "--input", path},
		{"api", "query { me { id } }"},
	} {
		gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"me":{"id":"u1"}}}`))
		}))
		t.Cleanup(gql.Close)
		f, _, _ := testFactoryTTY(t, "")
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", gql.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v must work from a terminal: %v", args, err)
		}
	}
}

// #648 — the rest of the node group's DOCUMENT stdin readers take the same
// refusal: `node update --abstract -` / `--data-merge -` and `node import -`
// (both modes). Before any request, exit 2, the remedy named as a phrase.
func TestNodeDocumentStdinRefusesATerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		remedy string
	}{
		{"update abstract", []string{"node", "update", "acme.com::kb::x:y", "--abstract", "-"}, "--abstract-file <path>"},
		{"update data-merge", []string{"node", "update", "acme.com::kb::x:y", "--data-merge", "-"}, "--data-merge-file <path>"},
		{"import restore", []string{"node", "import", "-"}, "hadron node import <path>"},
		{"import content", []string{"node", "import", "-", "--as-content", "-m", "acme.com::kb", "--loc", "x:y"}, "hadron node import <path>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			t.Cleanup(gql.Close)
			f, _, errOut := testFactoryTTY(t, "typed at a terminal\n")
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a document read from a terminal must be refused")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			if requests != 0 {
				t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
			}
			msg := errOut.String()
			for _, want := range []string{tc.remedy, "interactive terminal"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal must mention %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "%!") {
				t.Errorf("the refusal has a formatting fault:\n%s", msg)
			}
		})
	}
}

// The exemption is enumerable: --url never reads stdin, so a stray "-" beside
// it must not hit the DOCUMENT refusal (the command may fail for its own
// reasons — that is not what is under test).
func TestNodeImportURLIsNotGuardedByTheDocumentRule(t *testing.T) {
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"not under test"}]}`))
	}))
	t.Cleanup(gql.Close)
	f, _, errOut := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-", "--url", "https://example.com/p",
		"-m", "acme.com::kb", "--loc", "x:y", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		_ = renderError(f, err)
	}
	if msg := errOut.String(); strings.Contains(msg, "interactive terminal") {
		t.Errorf("--url reads no stdin and must not hit the document guard:\n%s", msg)
	}
}

// #648 — a chat message body is a DOCUMENT. `chat post --body -`,
// `team chat post -` (positional or --body -) and `channel post <addr> -` all
// refuse an interactive terminal: exit 2, before any request, remedy named.
func TestChatBodyStdinRefusesATerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		remedy string
	}{
		{"chat post --body -", []string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--body", "-"}, "--body-file <path>"},
		{"team chat post positional", []string{"team", "chat", "post", "-", "--app", "acme.com:eng-team"}, "--body-file <path>"},
		{"team chat post --body -", []string{"team", "chat", "post", "--body", "-", "--app", "acme.com:eng-team"}, "--body-file <path>"},
		{"channel post", []string{"channel", "post", "r", "-", "--as-me"}, "hadron channel post <address> - < <path>"},
		// Ambient App, no binding: the path on which team chat post pre-flights
		// the App before posting, so the refusal must come first.
		{"team chat post ambient", []string{"team", "chat", "post", "-"}, "--body-file <path>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t) // no binding file: keep team chat off the real checkout
			if strings.HasSuffix(tc.name, "ambient") {
				configuredApp(t, "acme.com:eng-team")
			}
			requests := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			t.Cleanup(gql.Close)
			f, _, errOut := testFactoryTTY(t, "typed at a terminal\n")
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a message body read from a terminal must be refused")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			if requests != 0 {
				t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
			}
			msg := errOut.String()
			for _, want := range []string{tc.remedy, "interactive terminal"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal must mention %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "%!") {
				t.Errorf("the refusal has a formatting fault:\n%s", msg)
			}
		})
	}
}

// The piped form keeps working for all three, and the body reaches the wire
// verbatim. `channel post` read os.Stdin directly before #648, so this is the
// first test that can drive its stdin at all.
func TestChatBodyStdinStillWorksWhenPiped(t *testing.T) {
	const body = "line one\nline two\n"
	for _, tc := range []struct {
		name string
		args []string
		op   string
		resp string
	}{
		{"chat post --body -", []string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--body", "-"},
			"CreateChannelMessage", `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`},
		{"team chat post -", []string{"team", "chat", "post", "-", "--app", "acme.com:eng-team"},
			"CreateTeamChatMessage", `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`},
		{"channel post", []string{"channel", "post", "r", "-", "--as-me"},
			"CreateChannelMessage", `{"data":{"createChannelMessage":` + messageJSON + `}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			gql, captured := captureGraphQL(t, map[string]string{
				tc.op:             tc.resp,
				"TeamAppIdentity": teamAppIdentityJSON,
			})
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader(body)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("a piped body must still post: %v", err)
			}
			if !strings.Contains(string(captured[tc.op]), `"line one\nline two\n"`) {
				t.Errorf("the piped body must reach the wire verbatim: %s", captured[tc.op])
			}
		})
	}
}
