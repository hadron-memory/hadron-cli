package cmd

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// cli#3249: each grant selection verb names only the refusals it can get. The
// sets are the gate's, checked against hadron-server#5001's resolvers.

var refusalCode3249 = regexp.MustCompile(`\b(?:(?:SELECTION|GRANT|SIGNING)_[A-Z_]+|FORBIDDEN)\b`)

func codes3249(text string) []string {
	codes := refusalCode3249.FindAllString(text, -1)
	slices.Sort(codes)
	return slices.Compact(codes)
}

var selectionVerbs3249 = []struct {
	name  string
	argv  []string
	codes []string
	exit  string // the whole exit-code paragraph, whitespace collapsed
}{
	{
		name: "request-selection-signature",
		argv: []string{"team", "grant", "request-selection-signature"},
		codes: []string{"FORBIDDEN", "GRANT_EXPIRED", "GRANT_NOT_FOUND", "GRANT_NOT_PENDING", "GRANT_TERMS_UNREPRESENTABLE",
			"SELECTION_DUPLICATE_GRANT", "SELECTION_EMPTY", "SELECTION_MIXED_APPS", "SELECTION_TOO_LARGE"},
		exit: "Exit codes: 0 done; 2 usage (a blank id, a binding for another server or App, a server without selection signing, " +
			"SELECTION_EMPTY, SELECTION_TOO_LARGE, SELECTION_DUPLICATE_GRANT, SELECTION_MIXED_APPS); 4 GRANT_NOT_FOUND; " +
			"5 GRANT_NOT_PENDING, GRANT_EXPIRED, GRANT_TERMS_UNREPRESENTABLE; " +
			"8 not permitted (FORBIDDEN, which the server also answers to asking while impersonating).",
	},
	{
		name: "get-selection",
		argv: []string{"team", "grant", "signing-request", "get-selection"},
		exit: "Exit codes: 0 done; 2 usage (a blank id, a binding for another server or App, a server without selection signing); " +
			"4 a selection that is not found or not visible.",
	},
	{
		name:  "withdraw-selection",
		argv:  []string{"team", "grant", "signing-request", "withdraw-selection"},
		codes: []string{"FORBIDDEN", "SIGNING_REQUEST_ENDED", "SIGNING_REQUEST_NOT_FOUND", "SIGNING_REQUEST_NOT_REQUESTER"},
		exit: "Exit codes: 0 done; 2 usage (a blank id, a binding for another server or App, a server without selection signing); " +
			"4 SIGNING_REQUEST_NOT_FOUND, or a selection that is not found or not visible; 5 SIGNING_REQUEST_ENDED; " +
			"8 not permitted (SIGNING_REQUEST_NOT_REQUESTER, and FORBIDDEN, which the server also answers to withdrawing while impersonating).",
	},
}

func TestGrantSelectionHelp3249PerVerbRefusals(t *testing.T) {
	for _, v := range selectionVerbs3249 {
		t.Run(v.name, func(t *testing.T) {
			f, _ := testFactory(t)
			c, _, err := NewRootCmd(f).Find(v.argv)
			if err != nil || c.Name() != v.name {
				t.Fatalf("find %v: %v", v.argv, err)
			}
			if got := codes3249(c.Long); !slices.Equal(got, v.codes) {
				t.Errorf("codes named:\n got %v\nwant %v", got, v.codes)
			}
			text := strings.Join(strings.Fields(c.Long), " ")
			if !strings.HasSuffix(text, v.exit) {
				t.Errorf("exit codes, want the help to end:\n%s\ngot:\n%s", v.exit, c.Long)
			}
			// Only the request takes grant ids, so only it can be shown one.
			passes := v.name == "request-selection-signature"
			if got := strings.Contains(text, "only when it is one you passed"); got != passes {
				t.Errorf("grant id sentence present=%v, want %v", got, passes)
			}
			// The shared paragraphs stay on every verb.
			for _, s := range []string{"carries this worktree's worker session", "passkey", `"does not offer"`} {
				if !strings.Contains(text, s) {
					t.Errorf("help lacks %q", s)
				}
			}
		})
	}
}

// The agent contract attributes each code to its verb.
func TestGrantSelectionHelp3249AgenticUsage(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(out.String(), "#### Signing a selection of grants (cli#3197)")
	if !ok {
		t.Fatal("selection section not found")
	}
	section, _, ok = strings.Cut(section, "\n### ")
	if !ok {
		t.Fatal("selection section has no end")
	}
	total := 0
	for _, v := range selectionVerbs3249 {
		_, line, ok := strings.Cut(section, "\n- `"+v.name+"`: ")
		if !ok {
			t.Errorf("no refusal line for %s", v.name)
			continue
		}
		line, _, _ = strings.Cut(line, "\n")
		got := codes3249(line)
		if !slices.Equal(got, v.codes) {
			t.Errorf("%s:\n got %v\nwant %v", v.name, got, v.codes)
		}
		total += len(refusalCode3249.FindAllString(line, -1))
	}
	// No code is listed again outside its verb's line.
	if all := len(refusalCode3249.FindAllString(section, -1)); all != total {
		t.Errorf("%d code mentions in the section, %d on the per-verb lines", all, total)
	}
}
