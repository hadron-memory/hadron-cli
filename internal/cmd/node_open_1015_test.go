package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func nodeOpen1015(t *testing.T, response string, args ...string) (string, string, int, map[string]json.RawMessage) {
	t.Helper()
	srv, calls := captureGraphQLFunc(t, func(op string) string {
		if op != "NodePortal" {
			t.Errorf("unexpected %s", op)
		}
		return response
	})
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "open"}, args...), "--server", srv.URL))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), stderr.String(), code, calls
}
func TestNodeOpen1015CanonicalPointer(t *testing.T) {
	url := "https://portal.example.test/custom/app/u/hrn:node:acme.com:kb:x?x=a%26b&y=%3B"
	r := `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:x","portalUrl":"` + url + `"}}}`
	for _, tail := range [][]string{{}, {"--json"}, {"--print"}} {
		out, stderr, code, calls := nodeOpen1015(t, r, append([]string{"hrn:node:acme.com:kb:x"}, tail...)...)
		if code != 0 || stderr != "" || len(calls) != 1 {
			t.Fatalf("%d %s %s %v", code, out, stderr, calls)
		}
		if len(tail) > 0 && tail[0] == "--json" {
			var d map[string]any
			if json.Unmarshal([]byte(out), &d) != nil || d["portalUrl"] != url || d["urn"] != "hrn:node:acme.com:kb:x" {
				t.Fatal(out)
			}
		} else if strings.TrimSpace(out) != url {
			t.Fatal(out)
		}
	}
}
func TestNodeOpen1015UnavailableRefusalAndValidation(t *testing.T) {
	for _, tc := range []struct {
		response string
		code     int
	}{{`{"data":{"node":null}}`, 4}, {`{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:x","portalUrl":null}}}`, 7}, {`{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:x","portalUrl":"javascript:bad"}}}`, 7}, {`{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, 8}} {
		out, stderr, code, calls := nodeOpen1015(t, tc.response, "hrn:node:acme.com:kb:x")
		if code != tc.code || out != "" || stderr == "" || len(calls) != 1 {
			t.Fatalf("%d %q %q %v", code, out, stderr, calls)
		}
	}
	_, _, code, calls := nodeOpen1015(t, `{}`, "not:a:qualified:ref")
	if code != 2 || len(calls) != 0 {
		t.Fatalf("%d %v", code, calls)
	}
}
