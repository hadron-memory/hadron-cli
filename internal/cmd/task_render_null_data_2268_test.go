package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2268: renderRunNode is declared String! by the server, so a successful
// reply with no data, empty data or a null renderRunNode breaks the contract.
// The generated plain string cannot tell that from a valid empty render. The
// command refuses the three invalid replies (exit 1, nothing rendered, no
// retry, no fallback: a render records a read, cor:dmo:080:02) and keeps a
// valid {"renderRunNode":""} a success.
func TestTaskRenderRefusesMissingOrNullData2268(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"data null", `{"data":null}`},
		{"data empty object", `{"data":{}}`},
		{"renderRunNode null", `{"data":{"renderRunNode":null}}`},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, asJSON), func(t *testing.T) {
				server, requests := renderFixture1010(t, tc.reply)
				f, out := testFactory(t)
				args := []string{"task", "render", renderNodeRef1010, "--server", server.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != exitcode.Error {
					t.Fatalf("an invalid successful reply must fail with exit %d, got err=%v", exitcode.Error, err)
				}
				if out.Len() != 0 || strings.Contains(out.String(), "rendered") {
					t.Fatalf("nothing may be rendered on a refusal, stdout=%q", out.String())
				}
				if len(*requests) != 1 {
					t.Fatalf("no retry and no fallback: requests=%d, want exactly 1", len(*requests))
				}
				if !strings.Contains(err.Error(), "renderRunNode") || !strings.Contains(err.Error(), "nothing was rendered") {
					t.Fatalf("the refusal names the field and says nothing was rendered: %v", err)
				}
			})
		}
	}
}

// The other direction: a valid empty render is a success, text and JSON.
func TestTaskRenderValidEmptyStaysASuccess2268(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", asJSON), func(t *testing.T) {
			server, requests := renderFixture1010(t, `{"data":{"renderRunNode":""}}`)
			f, out := testFactory(t)
			args := []string{"task", "render", renderNodeRef1010, "--server", server.URL}
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
