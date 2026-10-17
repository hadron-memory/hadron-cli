package auth

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// beforeRead observes output at the input boundary, so a cue printed after
// scanning cannot pass merely because it appears in the final stderr buffer.
type beforeRead struct {
	io.Reader
	check func()
}

func (r *beforeRead) Read(p []byte) (int, error) {
	if r.check != nil {
		r.check()
		r.check = nil
	}
	return r.Reader.Read(p)
}

type failedInput struct{ err error }

func (r failedInput) Read([]byte) (int, error) { return 0, r.err }

func TestLoginWithTokenInputCue(t *testing.T) {
	const cue = "Now paste your key/token here:\n"
	const token = "synthetic-888-secret"
	readErr := errors.New("controlled input failure")
	for _, tty := range []bool{true, false} {
		for _, jsonMode := range []bool{false, true} {
			for _, input := range []struct {
				name    string
				reader  func() io.Reader
				wantErr string
				code    int
			}{
				{"success", func() io.Reader { return strings.NewReader("  " + token + "  \n") }, "", 0},
				{"eof", func() io.Reader { return strings.NewReader("") }, "--with-token expects a token on standard input", exitcode.Usage},
				{"empty", func() io.Reader { return strings.NewReader(" \t\n") }, "--with-token expects a non-empty token on standard input", exitcode.Usage},
				{"read-error", func() io.Reader { return failedInput{readErr} }, readErr.Error(), exitcode.Error},
			} {
				name := input.name
				if tty {
					name += "/tty"
				} else {
					name += "/pipe"
				}
				if jsonMode {
					name += "/json"
				} else {
					name += "/text"
				}
				t.Run(name, func(t *testing.T) {
					// No real credentials, config files or OS keychain are used.
					t.Setenv("XDG_CONFIG_HOME", t.TempDir())
					keyring.MockInit()
					streams, out, errOut := output.Test()
					if tty {
						streams, out, errOut = output.TestTTY("")
					}
					wantCue := ""
					if tty {
						wantCue = cue
					}
					streams.In = &beforeRead{Reader: input.reader(), check: func() {
						if got := errOut.String(); got != wantCue {
							t.Errorf("stderr before input read = %q, want %q", got, wantCue)
						}
						if out.Len() != 0 {
							t.Errorf("stdout written before input read: %q", out.String())
						}
					}}
					f := cmdutil.NewFactory()
					f.IOStreams = streams
					f.JSON = jsonMode
					f.ServerFlag = "https://login-888.invalid"
					f.TokenStoreFn = func() store.Store { return store.Keyring{} }
					cmd := newCmdLogin(f)
					cmd.SilenceErrors, cmd.SilenceUsage = true, true
					cmd.SetArgs([]string{"--with-token"})
					err := cmd.Execute()
					if input.wantErr == "" {
						if err != nil {
							t.Fatal(err)
						}
						gotToken, err := (store.Keyring{}).Get("login-888.invalid")
						if err != nil || gotToken != token {
							t.Fatalf("stored token mismatch, err=%v", err)
						}
						if jsonMode {
							var got loginResult
							if err := json.Unmarshal(out.Bytes(), &got); err != nil {
								t.Fatal(err)
							}
							if got.Server != f.ServerFlag || got.TokenStorage != "keychain" {
								t.Fatalf("unexpected JSON result: %+v", got)
							}
						} else if out.String() != "✓ Signed in to https://login-888.invalid (token stored in keychain)\n" {
							t.Errorf("unexpected success output: %q", out.String())
						}
					} else {
						if err == nil || err.Error() != input.wantErr || exitcode.FromError(err) != input.code {
							t.Fatalf("error=%v, want %q (code %d)", err, input.wantErr, input.code)
						}
						if input.name == "read-error" && !errors.Is(err, readErr) {
							t.Fatal("input error identity lost")
						}
						if out.Len() != 0 {
							t.Errorf("stdout on refused input: %q", out.String())
						}
						if _, err := (store.Keyring{}).Get("login-888.invalid"); !errors.Is(err, store.ErrNotFound) {
							t.Fatalf("credential stored after input failure: %v", err)
						}
					}
					if errOut.String() != wantCue {
						t.Errorf("final stderr = %q, want %q", errOut.String(), wantCue)
					}
					for _, secret := range []string{token, "synthetic-888"} {
						if strings.Contains(out.String()+errOut.String(), secret) {
							t.Error("credential data disclosed in output")
						}
					}
				})
			}
		}
	}
}
