package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- memory encrypt / link-user: --data-key-file, literal --data-key deprecated (#1657, ruling 0148) ----
//
// A raw data key in argv shows in `ps`, /proc/<pid>/cmdline and shell history.
// The key now comes from a mode-checked file (or stdin), a literal still works
// for one release but warns once on stderr (never echoing the key), and the
// refusal of a literal is a later, separate change.

const encryptKeyMemory1657 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type dataKeyCase1657 struct {
	name      string
	base      []string
	responses map[string]string
	opName    string
}

func dataKeyCases1657() []dataKeyCase1657 {
	return []dataKeyCase1657{
		{"encrypt", []string{"memory", "encrypt", encryptKeyMemory1657, "--yes", "--json"},
			map[string]string{"EncryptMemory": `{"data":{"encryptMemory":{"id":"` + encryptKeyMemory1657 + `","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`}, "EncryptMemory"},
		{"link-user", []string{"memory", "link-user", "acme.com::anon-7f3", "--external-user", "u1", "--yes", "--json"},
			linkUserFakes(linkedEncryptedMemoryJSON), "LinkMemoryToUser"},
	}
}

type dataKeyRun1657 struct {
	stdout, stderr string
	err            error
	reqs           []capturedLinkUserRequest
}

func runDataKey1657(t *testing.T, c dataKeyCase1657, stdin string, extra ...string) dataKeyRun1657 {
	t.Helper()
	gql, reqs := captureLinkUserGraphQL(t, c.responses)
	f, out := testFactory(t)
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append(append(append([]string{}, c.base...), extra...), "--server", gql.URL))
	err := root.Execute()
	return dataKeyRun1657{out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err, append([]capturedLinkUserRequest{}, *reqs...)}
}

// keyFile1657 writes a key file at exactly mode (Chmod, so the umask cannot
// change what the test means).
func keyFile1657(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data.key")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDataKeyFileExclusiveWithDataKeyAndPassphraseFile1657(t *testing.T) {
	enc := dataKeyCases1657()[0]
	r := runDataKey1657(t, enc, "", "--data-key-file", keyFile1657(t, 0o600, "k\n"), "--data-key", "literal")
	if r.err == nil || !strings.Contains(r.err.Error(), "--data-key and --data-key-file are mutually exclusive") || len(r.reqs) != 0 {
		t.Fatalf("--data-key-file with --data-key: %v reqs=%d", r.err, len(r.reqs))
	}
	r = runDataKey1657(t, enc, "", "--data-key-file", keyFile1657(t, 0o600, "k\n"), "--passphrase-file", keyFile1657(t, 0o600, "p\n"))
	if r.err == nil || !strings.Contains(r.err.Error(), "--data-key-file and --passphrase-file are mutually exclusive") || len(r.reqs) != 0 || exitCodeFor(r.err) != exitcode.Usage {
		t.Fatalf("--data-key-file with --passphrase-file: %v reqs=%d", r.err, len(r.reqs))
	}
}

// --data-key-file - reads stdin, and the confirmation prompt is answered BEFORE
// stdin is consumed as the key (Confirm reads line by line).
func TestDataKeyFileStdinConfirmsBeforeReading1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			gql, reqs := captureLinkUserGraphQL(t, c.responses)
			f, out, _ := testFactoryTTY(t, "")
			f.IOStreams.In = iotest.OneByteReader(strings.NewReader("y\n" + linkUserDataKey + "\n"))
			args := []string{}
			for _, a := range c.base {
				if a != "--yes" {
					args = append(args, a)
				}
			}
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--data-key-file", "-", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("the prompt must be answered before the key is read: %v", err)
			}
			if len(*reqs) == 0 || !strings.Contains(out.String(), "{") {
				t.Fatalf("no request/output: reqs=%d out=%q", len(*reqs), out.String())
			}
			sent := false
			for _, r := range *reqs {
				if r.OpKey == linkUserDataKey || strings.Contains(r.Body, linkUserDataKey) {
					sent = true
				}
			}
			if !sent {
				t.Fatalf("the key read from stdin after the answer was not sent: %+v", *reqs)
			}
		})
	}
}

// A literal --data-key still works for one release, warning once on stderr
// about --data-key-file and never echoing the key; --data-key - does not warn.
func TestLiteralDataKeyWarnsOnceWithoutEchoingTheKey1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			r := runDataKey1657(t, c, "", "--data-key", linkUserDataKey)
			if r.err != nil || len(r.reqs) == 0 {
				t.Fatalf("a literal key must still work: err=%v reqs=%d", r.err, len(r.reqs))
			}
			if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n") + 1; r.stderr == "" || n != 1 {
				t.Fatalf("want exactly one stderr line, got %d: %q", n, r.stderr)
			}
			if !strings.Contains(r.stderr, "--data-key-file") || !strings.Contains(strings.ToLower(r.stderr), "deprecated") {
				t.Fatalf("the warning must name --data-key-file and say deprecated: %q", r.stderr)
			}
			if strings.Contains(r.stderr, linkUserDataKey) || strings.Contains(r.stdout, linkUserDataKey) {
				t.Fatalf("the key must never be echoed: stderr=%q stdout=%q", r.stderr, r.stdout)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &parsed); err != nil {
				t.Fatalf("--json stdout must stay valid JSON (the warning is stderr only): %v\n%s", err, r.stdout)
			}
			quiet := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key", "-")
			if quiet.err != nil || quiet.stderr != "" {
				t.Fatalf("--data-key - is the supported form and must not warn: err=%v stderr=%q", quiet.err, quiet.stderr)
			}
			if quiet.stdout != r.stdout {
				t.Fatalf("stdout differs between the literal and stdin forms:\n%s\n%s", r.stdout, quiet.stdout)
			}
		})
	}
}

// The warning follows the refusals: a refused run sends nothing and the
// literal is not what is reported.
func TestLiteralDataKeyWarningDoesNotPrecedeAUsageRefusal1657(t *testing.T) {
	c := dataKeyCases1657()[0]
	r := runDataKey1657(t, c, "", "--data-key", "literal", "--passphrase-file", keyFile1657(t, 0o600, "p\n"))
	if r.err == nil || len(r.reqs) != 0 {
		t.Fatalf("want the existing exclusive refusal: %v reqs=%d", r.err, len(r.reqs))
	}
	if strings.Contains(r.stderr, "literal") {
		t.Fatalf("the value must never appear: %q", r.stderr)
	}
}

// Help names --data-key-file first and says a literal is deprecated.
func TestDataKeyHelpNamesTheFileFormFirst1657(t *testing.T) {
	for _, path := range [][]string{{"memory", "encrypt"}, {"memory", "link-user"}} {
		t.Run(path[1], func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetOut(out)
			root.SetArgs(append(append([]string{}, path...), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := strings.Join(strings.Fields(out.String()), " ")
			file := strings.Index(help, "--data-key-file")
			if file < 0 {
				t.Fatalf("help must name --data-key-file:\n%s", out.String())
			}
			// Every mention of the bare literal flag comes after the file form.
			literal := strings.Index(strings.ReplaceAll(help, "--data-key-file", "--FILE"), "--data-key")
			if literal >= 0 && literal < file {
				t.Fatalf("--data-key-file must be named before --data-key:\n%s", out.String())
			}
			// The example must model a flow the command accepts: a key file made with
			// the default umask is 0644 and would be refused.
			if path[1] == "encrypt" && !strings.Contains(help, "umask 077") && !strings.Contains(help, "chmod 600 diary.key") {
				t.Fatalf("the example's key file must be created private:\n%s", out.String())
			}
			if !strings.Contains(strings.ToLower(help), "deprecated") {
				t.Fatalf("help must say a literal --data-key is deprecated:\n%s", out.String())
			}
			cmd, _, err := root.Find(path)
			if err != nil {
				t.Fatal(err)
			}
			if fl := cmd.Flags().Lookup("data-key-file"); fl == nil || !strings.Contains(fl.Usage, "stdin") {
				t.Fatalf("--data-key-file flag missing or undocumented: %+v", fl)
			}
			if fl := cmd.Flags().Lookup("data-key"); fl == nil || !strings.Contains(strings.ToLower(fl.Usage), "deprecated") {
				t.Fatalf("--data-key usage must say deprecated: %+v", fl)
			}
		})
	}
}

// The direct migration from `--data-key -`: --data-key-file - with --yes and a
// piped key sends exactly the same requests and prints exactly the same output.
func TestDataKeyFileDashWithPipedStdinMatchesDataKeyDash1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			old := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key", "-")
			now := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key-file", "-")
			if old.err != nil || now.err != nil || len(now.reqs) == 0 || len(now.reqs) != len(old.reqs) {
				t.Fatalf("old err=%v, new err=%v, reqs %d vs %d", old.err, now.err, len(old.reqs), len(now.reqs))
			}
			for i := range now.reqs {
				if now.reqs[i] != old.reqs[i] {
					t.Fatalf("request %d differs:\nnew %+v\nold %+v", i, now.reqs[i], old.reqs[i])
				}
			}
			if now.stdout != old.stdout || now.stderr != "" {
				t.Fatalf("output differs or warned: stdout %q vs %q, stderr %q", now.stdout, old.stdout, now.stderr)
			}
		})
	}
}

// link-user validates the key's shape locally before any request, whichever
// source supplied it (the check sits after the read, not inside one source).
func TestLinkUserMalformedKeyViaDataKeyFileDashRefusedLocally1657(t *testing.T) {
	c := dataKeyCases1657()[1]
	for _, bad := range []string{"not a key!", strings.Repeat("A", 43), "QUJD"} {
		r := runDataKey1657(t, c, bad+"\n", "--data-key-file", "-")
		if r.err == nil || exitCodeFor(r.err) != exitcode.Usage || len(r.reqs) != 0 {
			t.Fatalf("a malformed key (%q) from stdin must be refused locally: err=%v reqs=%d", bad, r.err, len(r.reqs))
		}
		if strings.Contains(r.err.Error(), bad) || strings.Contains(r.stderr, bad) {
			t.Fatalf("the refusal must not echo the key: %v / %q", r.err, r.stderr)
		}
	}
}

// The deprecation warning is decided after the local argument checks: a run
// refused before the key is considered (here, no --external-user) does not warn.
func TestLiteralDataKeyDoesNotWarnWhenArgumentsAreRefusedFirst1657(t *testing.T) {
	gql, reqs := captureLinkUserGraphQL(t, linkUserFakes(linkedEncryptedMemoryJSON))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3", "--data-key", linkUserDataKey, "--yes", "--server", gql.URL})
	err := root.Execute()
	stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
	if err == nil || len(*reqs) != 0 {
		t.Fatalf("want a refusal with nothing sent: %v reqs=%d", err, len(*reqs))
	}
	if strings.Contains(stderr, "deprecated") || strings.Contains(stderr, linkUserDataKey) {
		t.Fatalf("no warning and never the key: %q", stderr)
	}
}

// A failing stdin read is a usage refusal that names the flag, before any
// request, not a raw I/O error.
func TestDataKeyFileDashReadErrorIsAUsageRefusal1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			gql, reqs := captureLinkUserGraphQL(t, c.responses)
			f, _ := testFactory(t)
			f.IOStreams.In = iotest.ErrReader(errors.New("pipe broke"))
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, c.base...), "--data-key-file", "-", "--server", gql.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "reading --data-key-file from stdin") || strings.Contains(err.Error(), "pipe broke") {
				t.Fatalf("want a wrapped usage refusal naming the flag: %v (exit %d)", err, exitCodeFor(err))
			}
			if len(*reqs) != 0 {
				t.Fatalf("nothing may be sent: %+v", *reqs)
			}
		})
	}
}
