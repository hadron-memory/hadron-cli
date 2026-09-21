package cmd

import (
	"encoding/json"
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
	gql, _ := captureGraphQL(t, map[string]string{
		"EncryptMemory": `{"data":{"encryptMemory":{"id":"m1","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
		"GetMemory":     `{"data":{"memory":{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1","isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-09-20T00:00:00Z"}}}`,
	})
	f, _, errOut := testFactoryTTY(t, "SGVsbG9LZXlIZWxsb0tleQ==\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "acme.com::kb", "--data-key", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		_ = renderError(f, err)
	}
	if msg := errOut.String(); strings.Contains(msg, "interactive terminal") {
		t.Errorf("a SECRET read from a terminal must not hit the document guard:\n%s", msg)
	}
}
