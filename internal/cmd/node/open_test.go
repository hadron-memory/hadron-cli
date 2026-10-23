package node

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestNodeOpenFakeLauncher1015(t *testing.T) {
	const target = "https://portal.example.test/custom/path?a=%26%3B&quote=%27&other=$(noop)"
	for _, tc := range []struct {
		name                        string
		json, print, terminal, fail bool
		wantCalls                   int
	}{{"interactive", false, false, true, false, 1}, {"failure", false, false, true, true, 1}, {"json", true, false, true, false, 0}, {"headless", false, false, false, false, 0}, {"print", false, true, true, false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			streams, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, JSON: tc.json}
			calls := 0
			err := writeNodePortal(f, nodePortalDTO{ID: "n1", URN: "hrn:node:acme.com:kb:x", PortalURL: target}, tc.print, tc.terminal, func(got string) error {
				calls++
				if got != target {
					t.Fatalf("URL changed %q", got)
				}
				if tc.fail {
					return errors.New("fake failure")
				}
				return nil
			})
			if calls != tc.wantCalls {
				t.Fatal(calls)
			}
			if tc.fail {
				if err == nil || !strings.Contains(err.Error(), "browser launcher failed") || out.String() != "" {
					t.Fatalf("%v %s", err, out.String())
				}
			} else if err != nil || out.String() == "" {
				t.Fatalf("%v %s", err, out.String())
			}
		})
	}
}
func TestNodeOpenInvalidURLNeverLaunches1015(t *testing.T) {
	for _, target := range []string{"", "javascript:alert(1)", "file:///tmp/x", "https:///missing-host", "https://user:secret@host/x", "https://%/x", "https://host:bad/x", "https://host/x\n"} {
		streams, out, _ := output.Test()
		f := &cmdutil.Factory{IOStreams: streams}
		calls := 0
		err := writeNodePortal(f, nodePortalDTO{PortalURL: target}, false, true, func(string) error { calls++; return nil })
		if exitcode.FromError(err) != 7 || calls != 0 || out.String() != "" {
			t.Fatalf("invalidURL calls%d err%v out%s", calls, err, out.String())
		}
	}
}
