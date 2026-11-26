package cmd

// hadron-cli#3214: `ai-config create|update --api-key -` read the key from
// stdin with no terminal check, so on a terminal a pasted key echoed into the
// operator's screen and scrollback. Like #2654 and #2689, the read is refused
// there (exit 2) before stdin is read, credentials are resolved or a request
// is sent. The remedy names forms these commands accept today: a pipe, a file
// redirected to stdin, or (create) the --file JSON config.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

var createArgs3214 = []string{"ai-config", "create", "--app", "acme.com:juno-app",
	"--name", "default", "--provider", "anthropic", "--model", "claude-opus-4-8"}

func TestAIConfigAPIKeyDashOnTerminalRefused3214(t *testing.T) {
	const pasted = "sk-pasted-must-stay-unread-3214"
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"app":"acme.com:juno-app","name":"default","provider":"anthropic","model":"claude-opus-4-8"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		refusal    = "--api-key -: stdin is a terminal, so a pasted secret would echo to the screen; pipe it on stdin, or "
		wantCreate = refusal + "redirect a key file to it (--api-key - < key.txt), or put the key in the --file JSON config. Nothing was sent"
		wantUpdate = refusal + "redirect a key file to it (--api-key - < key.txt). Nothing was sent"
	)
	missing := filepath.Join(t.TempDir(), "no-such-config.json")
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"create", wantCreate, append(append([]string{}, createArgs3214...), "--api-key", "-")},
		{"create, --api-key=-", wantCreate, append(append([]string{}, createArgs3214...), "--api-key=-")},
		{"create with a --file path", wantCreate, []string{"ai-config", "create", "--file", cfg, "--api-key", "-"}},
		// The refusal comes before the --file path is loaded: a missing file
		// would otherwise answer first (Rook N1).
		{"create with a missing --file path", wantCreate, []string{"ai-config", "create", "--file", missing, "--api-key", "-"}},
		// On a terminal the secret rule answers before the --file - document
		// rule and the both-read-stdin conflict; piped, the conflict still
		// answers (TestAiConfigCreateFileStdinKeyConflict).
		{"create with --file -", wantCreate, []string{"ai-config", "create", "--file", "-", "--api-key", "-"}},
		{"update", wantUpdate, []string{"ai-config", "update", "cfg1", "--api-key", "-"}},
		{"update, --api-key=-", wantUpdate, []string{"ai-config", "update", "cfg1", "--api-key=-"}},
		{"update with other fields", wantUpdate, []string{"ai-config", "update", "cfg1", "--model", "m2", "--api-key", "-"}},
	} {
		for _, signedIn := range []bool{true, false} {
			name := c.name
			if !signedIn {
				name += " (signed out)"
			}
			t.Run(name, func(t *testing.T) {
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer srv.Close()
				f, out, errOut := testFactoryTTY(t, pasted+"\n")
				if !signedIn {
					t.Setenv("HADRON_TOKEN", "")
				}
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{}, c.args...), "--server", srv.URL))
				err := root.Execute()
				if err == nil {
					t.Fatal("expected a refusal")
				}
				if exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("exit %d, want %d (%v)", exitCodeFor(err), exitcode.Usage, err)
				}
				if err.Error() != c.want {
					t.Errorf("refusal\n got %q\nwant %q", err.Error(), c.want)
				}
				if strings.Contains(err.Error()+out.String()+errOut.String(), pasted) {
					t.Error("the pasted key must never appear")
				}
				rest, rerr := io.ReadAll(f.IOStreams.In)
				if rerr != nil {
					t.Fatal(rerr)
				}
				if string(rest) != pasted+"\n" {
					t.Errorf("nothing may be read from stdin; left %q", rest)
				}
				if n := requests.Load(); n != 0 {
					t.Errorf("a refusal must send nothing; %d requests", n)
				}
			})
		}
	}
}

// Controls: only `-` on a terminal is refused. A pipe still works, an empty
// pipe keeps its own refusal, and a literal or "" (update's clear) on a
// terminal reads nothing from stdin, so it is not refused.
func TestAIConfigAPIKeyOtherFormsUnchanged3214(t *testing.T) {
	const createOp, updateOp = "CreateAiServiceConfig", "UpdateAiServiceConfig"
	resp := map[string]string{
		createOp: `{"data":{"createAiServiceConfig":` + aiCfgJSON + `}}`,
		updateOp: `{"data":{"updateAiServiceConfig":` + aiCfgJSON + `}}`,
	}
	for _, c := range []struct {
		name      string
		tty       bool
		stdin, op string
		args      []string
		wantKey   any
		wantExit2 string
	}{
		{"create, piped", false, "sk-piped\n", createOp, append(append([]string{}, createArgs3214...), "--api-key", "-"), "sk-piped", ""},
		{"update, piped", false, "sk-piped\n", updateOp, []string{"ai-config", "update", "cfg1", "--api-key", "-"}, "sk-piped", ""},
		{"update, empty pipe", false, "", updateOp, []string{"ai-config", "update", "cfg1", "--api-key", "-"}, nil, "no API key on stdin"},
		{"create, literal on a terminal", true, "", createOp, append(append([]string{}, createArgs3214...), "--api-key", "sk-literal"), "sk-literal", ""},
		{"update, clear on a terminal", true, "", updateOp, []string{"ai-config", "update", "cfg1", "--api-key", ""}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, resp)
			var f *cmdutil.Factory
			if c.tty {
				f, _, _ = testFactoryTTY(t, c.stdin)
			} else {
				f, _ = testFactory(t)
				f.IOStreams.In = strings.NewReader(c.stdin)
			}
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, c.args...), "--server", gql.URL))
			err := root.Execute()
			if c.wantExit2 != "" {
				if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), c.wantExit2) {
					t.Fatalf("want exit 2 with %q, got %v", c.wantExit2, err)
				}
				if _, sent := captured[c.op]; sent {
					t.Error("a refusal must send nothing")
				}
				return
			}
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := unmarshalVars(t, captured[c.op])["apiKey"]; got != c.wantKey {
				t.Errorf("apiKey %v, want %v", got, c.wantKey)
			}
		})
	}
}

// Gravitus F1: a --file config's apiKey never goes through the stdin reader.
// A file value of "-" is a literal (stored as given, as before this change),
// so --file on a terminal cannot reopen the echo: stdin is left unread.
func TestAIConfigFileAPIKeyDashIsNotAStdinRead3214(t *testing.T) {
	const pasted = "sk-pasted-must-stay-unread-3214"
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"app":"acme.com:juno-app","name":"default","provider":"anthropic","model":"claude-opus-4-8","apiKey":"-"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfig": `{"data":{"createAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, out, errOut := testFactoryTTY(t, pasted+"\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--file", cfg, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rest, err := io.ReadAll(f.IOStreams.In)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != pasted+"\n" {
		t.Errorf("a file apiKey must never read stdin; left %q", rest)
	}
	if got := unmarshalVars(t, captured["CreateAiServiceConfig"])["apiKey"]; got != "-" {
		t.Errorf("apiKey %v, want the file's literal \"-\"", got)
	}
	if strings.Contains(out.String()+errOut.String(), pasted) {
		t.Error("the pasted text must never appear")
	}
}
