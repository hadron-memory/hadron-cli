package cmd

// hadron-cli#2689: on a terminal, a pasted secret echoes into the screen and
// scrollback, so every memory encrypt, link-user and unlock path that reads a
// secret from stdin refuses it there, as #2654 does. The refusal comes before
// any read, confirmation prompt or request.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSecretStdinOnTerminalRefused2689(t *testing.T) {
	const pasted = "pasted-secret-must-stay-unread-2689"
	const mem = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	encrypt := []string{"memory", "encrypt", mem}
	link := []string{"memory", "link-user", mem, "--external-user", "u1"}
	with := func(base []string, more ...string) []string {
		return append(append([]string{}, base...), more...)
	}
	for _, c := range []struct {
		name, flag string
		args       []string
	}{
		{"encrypt passphrase-file", "--passphrase-file", with(encrypt, "--passphrase-file", "-")},
		{"encrypt data-key-file", "--data-key-file", with(encrypt, "--data-key-file", "-")},
		{"encrypt data-key", "--data-key", with(encrypt, "--data-key", "-")},
		{"link-user data-key-file", "--data-key-file", with(link, "--data-key-file", "-")},
		{"link-user data-key", "--data-key", with(link, "--data-key", "-")},
		{"unlock passphrase stdin", "--unlock-passphrase-stdin", []string{"node", "get", "hrn:node:acme.com:kb:x", "--unlock", "acme.com:kb", "--unlock-passphrase-stdin"}},
	} {
		// Without --yes the command would prompt; with it, it would go on to read.
		for _, yes := range []bool{false, true} {
			args, name := c.args, c.name
			if yes {
				if c.args[0] == "node" {
					continue // node get has no --yes
				}
				args, name = with(c.args, "--yes"), name+" --yes"
			}
			t.Run(name, func(t *testing.T) {
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer srv.Close()
				// "y" answers a confirmation, if one is wrongly asked.
				f, out, errOut := testFactoryTTY(t, "y\n"+pasted+"\n")
				root := NewRootCmd(f)
				root.SetArgs(with(args, "--server", srv.URL))
				err := root.Execute()
				if err == nil {
					t.Fatal("expected a refusal")
				}
				if exitCodeFor(err) != 2 {
					t.Fatalf("exit %d (%v)", exitCodeFor(err), err)
				}
				for _, want := range []string{c.flag, "stdin is a terminal", "echo", "pipe", "Nothing was sent"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal lacks %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error()+out.String()+errOut.String(), pasted) {
					t.Error("the pasted material must never appear")
				}
				if errOut.String() != "" {
					t.Errorf("no prompt or warning before the refusal: %q", errOut.String())
				}
				if rest, _ := io.ReadAll(f.IOStreams.In); string(rest) != "y\n"+pasted+"\n" {
					t.Errorf("nothing may be read from stdin; left %q", rest)
				}
				if n := requests.Load(); n != 0 {
					t.Errorf("a refusal must send nothing; %d requests", n)
				}
			})
		}
	}
}
