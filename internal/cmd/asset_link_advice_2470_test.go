package cmd

import (
	"strings"
	"testing"
)

// cli#2470: `asset link` displays the plain-pin advice the server attaches when the
// asset's file name carries include syntax (the server interpolates it unescaped
// into the reference node), in text and --json mode, and shows nothing for an
// ordinary file name. Stdout and the exit code do not change either way.
func TestAssetLinkDisplaysIncludeAdvice2470(t *testing.T) {
	const node = `"createAssetReferenceNode":{"id":"n1","urn":"hrn:node:acme.com:kb:designs:logo",` +
		`"loc":"designs:logo","name":"logo.png","nodeType":"reference","memoryId":"mem1"}`
	advised := `{"data":{` + node + `},"extensions":{"warnings":[{"path":"createAssetReferenceNode","code":"include.pin.unkept","text":"include in file name"}]}}`
	clean := `{"data":{` + node + `}}`

	run := func(t *testing.T, body string, extra ...string) (stderr, stdout string) {
		t.Helper()
		gql, _ := captureGraphQL(t, map[string]string{"CreateAssetReferenceNode": body})
		f, out := testFactory(t)
		errOut := &strings.Builder{}
		f.IOStreams.ErrOut = errOut
		root := NewRootCmd(f)
		args := append([]string{"asset", "link", "a1", "--node", "hrn:node:acme.com:kb:designs:logo", "--server", gql.URL}, extra...)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return errOut.String(), out.String()
	}

	for _, mode := range []struct {
		name  string
		extra []string
	}{{"text", nil}, {"json", []string{"--json"}}} {
		t.Run(mode.name, func(t *testing.T) {
			stderr, stdout := run(t, advised, mode.extra...)
			if !strings.Contains(stderr, "warning: [include.pin.unkept] include in file name\n") {
				t.Errorf("advice not displayed: stderr=%q", stderr)
			}
			cleanErr, cleanOut := run(t, clean, mode.extra...)
			if strings.Contains(cleanErr, "warning:") {
				t.Errorf("an ordinary file name must display no advice: stderr=%q", cleanErr)
			}
			if stdout != cleanOut {
				t.Errorf("advice must not change stdout:\n advised: %q\n clean:   %q", stdout, cleanOut)
			}
		})
	}
}
