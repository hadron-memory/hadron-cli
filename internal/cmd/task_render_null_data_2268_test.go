package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The #2268 suite's own names for the shared task-render fixture (cli#2402 F6):
// the helpers were written for #1010 and are reused as they are, so the aliases
// keep the two issue numbers from mixing in these tests.
const renderNodeRef2268 = renderNodeRef1010

func renderFixture2268(t *testing.T, reply string) (*httptest.Server, *[]map[string]json.RawMessage) {
	t.Helper()
	return renderFixture1010(t, reply)
}

// refusal2268 is the refusal's literal message (internal/cmd/task/render.go).
const refusal2268 = "the server's reply had no renderRunNode (missing or null), so nothing was rendered"

// cli#2268: renderRunNode is declared String! by the server, so a successful
// reply with no data key, null data, empty data or a null renderRunNode breaks
// the contract. The generated plain string cannot tell that from a valid empty
// render. The command refuses the four invalid replies (exit 1, nothing
// rendered, no retry, no fallback: a render records a read, cor:dmo:080:02) and
// keeps a valid {"renderRunNode":""} a success. The refusal renders like every
// other error (cli#2402 F5): the message on stderr in text mode, the standard
// error envelope in --json mode.
func TestTaskRenderRefusesMissingOrNullData2268(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"no data key", `{}`},
		{"data null", `{"data":null}`},
		{"data empty object", `{"data":{}}`},
		{"renderRunNode null", `{"data":{"renderRunNode":null}}`},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, asJSON), func(t *testing.T) {
				server, requests := renderFixture2268(t, tc.reply)
				f, out := testFactory(t)
				args := []string{"task", "render", renderNodeRef2268, "--server", server.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != exitcode.Error {
					t.Fatalf("an invalid successful reply must fail with exit %d, got err=%v", exitcode.Error, err)
				}
				// The command itself writes nothing: any output here would be a
				// render of the broken reply (F1: one assertion, no dead disjunct).
				if out.Len() != 0 {
					t.Fatalf("nothing may be rendered on a refusal, stdout=%q", out.String())
				}
				if len(*requests) != 1 {
					t.Fatalf("no retry and no fallback: requests=%d, want exactly 1", len(*requests))
				}
				if err.Error() != refusal2268 {
					t.Fatalf("the refusal is the literal message: %q", err.Error())
				}
				// F5: it renders like every other error, through the shared renderer.
				if code := renderError(f, err); code != exitcode.Error {
					t.Fatalf("renderError exit=%d, want %d", code, exitcode.Error)
				}
				stderr, ok := f.IOStreams.ErrOut.(fmt.Stringer)
				if !ok {
					t.Fatal("stderr cannot be inspected")
				}
				if !asJSON {
					if out.Len() != 0 || stderr.String() != "hadron: "+refusal2268+"\n" {
						t.Fatalf("text mode: stdout must stay empty and stderr carry the message; stdout=%q stderr=%q", out.String(), stderr.String())
					}
					return
				}
				var env struct {
					Error struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
					t.Fatalf("--json mode: the error envelope must be valid JSON: %v: %q", err, out.String())
				}
				if env.Error.Code != exitcode.Error || env.Error.Message != refusal2268 || strings.Contains(out.String(), `"rendered"`) {
					t.Fatalf("--json mode: envelope=%q (want code %d and the refusal, no rendered document)", out.String(), exitcode.Error)
				}
				if stderr.String() != "" {
					t.Fatalf("--json mode: the envelope is the only output, stderr=%q", stderr.String())
				}
			})
		}
	}
}

// The other direction: a valid empty render is a success, text and JSON.
func TestTaskRenderValidEmptyStaysASuccess2268(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			server, requests := renderFixture2268(t, `{"data":{"renderRunNode":""}}`)
			f, out := testFactory(t)
			args := []string{"task", "render", renderNodeRef2268, "--server", server.URL}
			if asJSON {
				args = append(args, "--json")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("a valid empty render must succeed: %v", err)
			}
			if len(*requests) != 1 {
				t.Fatalf("requests=%d", len(*requests))
			}
			if !asJSON {
				if out.String() != "" {
					t.Fatalf("raw stdout must be empty, got %q", out.String())
				}
				return
			}
			var dto map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto) != 1 || string(dto["rendered"]) != `""` {
				t.Fatalf("json=%s, want exactly {\"rendered\":\"\"}", out.String())
			}
		})
	}
}
