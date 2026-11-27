package cmd

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3277: `memory detach` sends the one detachMemoryFromApp mutation and
// prints the server's answer. The server judges who may detach; the CLI checks
// nothing. Shipped text says attach / detach / attachment, never "link"
// (D-2026-11-26-001, cor:dmo:060:22), and never says detaching revokes access.
func detachArgs3277(extra ...string) []string {
	return append([]string{"memory", "detach", "acme.com::kb", "--app", "hrn:app:acme.com:coach"}, extra...)
}

func TestMemoryDetachSendsOneMutationAndPrintsTheAnswer3277(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"DetachMemoryFromApp": `{"data":{"detachMemoryFromApp":true}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(detachArgs3277("--yes", "--json"), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("exactly one call expected, got %v", captured)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DetachMemoryFromApp"], &vars)
	if vars["memoryRef"] != "hrn:mem:acme.com:kb" || vars["appRef"] != "hrn:app:acme.com:coach" {
		t.Errorf("refs = %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if dto["detached"] != true || dto["memory"] != "hrn:mem:acme.com:kb" || dto["app"] != "hrn:app:acme.com:coach" {
		t.Errorf("DTO = %v", dto)
	}

	gql2, _ := captureGraphQL(t, map[string]string{"DetachMemoryFromApp": `{"data":{"detachMemoryFromApp":true}}`})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs(append(detachArgs3277("--yes"), "--server", gql2.URL))
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "✓ detached hrn:mem:acme.com:kb from hrn:app:acme.com:coach") {
		t.Errorf("text = %q", out2.String())
	}
}

// Like the other reversible removals: a non-interactive caller needs --yes, and
// nothing is sent without it. A missing --app is a usage error with no request.
func TestMemoryDetachNeedsYesAndApp3277(t *testing.T) {
	for name, args := range map[string][]string{
		"no --yes": detachArgs3277(),
		"no --app": {"memory", "detach", "acme.com::kb", "--yes"},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"DetachMemoryFromApp": `{"data":{"detachMemoryFromApp":true}}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", gql.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a usage error (exit 2), got %v", err)
			}
			if len(captured) != 0 {
				t.Errorf("nothing may be sent: %v", captured)
			}
		})
	}
}

// The server's table: no signed-in user, no authority over this attachment, a
// memory the caller cannot read. Code and message are printed as they come.
func TestMemoryDetachRefusals3277(t *testing.T) {
	for name, c := range map[string]struct {
		code, message string
		exit          int
	}{
		"no user":      {"UNAUTHENTICATED", "Authentication required", exitcode.AuthRequired},
		"no authority": {"FORBIDDEN", "Forbidden", exitcode.Forbidden},
		"not readable": {"MEMORY_NOT_FOUND", "Memory not found", exitcode.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			reply := `{"data":null,"errors":[{"message":"` + c.message + `","path":["detachMemoryFromApp"],"extensions":{"code":"` + c.code + `"}}]}`
			gql, _ := captureGraphQL(t, map[string]string{"DetachMemoryFromApp": reply})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(detachArgs3277("--yes", "--json"), "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a refusal must fail")
			}
			if got := renderError(f, err); got != c.exit {
				t.Errorf("exit %d, want %d", got, c.exit)
			}
			var env struct {
				Error struct {
					Message    string `json:"message"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
				t.Fatalf("%v\n%s", e, out.String())
			}
			if !strings.Contains(env.Error.Message, c.message) || env.Error.ServerCode != c.code {
				t.Errorf("code and message must come as the server sent them: %s", out.String())
			}
		})
	}
}

// A false answer is not a success: the CLI does not claim a detachment the server
// did not confirm.
func TestMemoryDetachFalseIsNotASuccess3277(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"DetachMemoryFromApp": `{"data":{"detachMemoryFromApp":false}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(detachArgs3277("--yes"), "--server", gql.URL))
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v", err)
	}
	if strings.Contains(out.String(), "✓") {
		t.Errorf("no success mark for a false answer: %q", out.String())
	}
}

var retiredTerm3277 = regexp.MustCompile(`(?i)\b(un)?link(s|ed|ing)?\b`)

// Shipped text: attach / detach / attachment, never "link"; detach changes scope
// only and never "revokes access". Covers the help of both commands and their
// agentic-usage entries.
func TestMemoryDetachWordingIsTheAttachVocabulary3277(t *testing.T) {
	read := func(args ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(f.IOStreams.Out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(out.String()), " ")
	}
	usage := read("agentic-usage")
	entry := func(prefix string) string {
		i := strings.Index(usage, prefix)
		if i < 0 {
			t.Fatalf("agentic-usage has no %q entry", prefix)
		}
		e := usage[i:]
		if j := strings.Index(e, " - "); j > 0 {
			e = e[:j]
		}
		return e
	}
	for name, text := range map[string]string{
		"detach help":  read("memory", "detach", "--help"),
		"attach help":  read("memory", "attach", "--help"),
		"detach usage": entry("`memory detach"),
		"attach usage": entry("`memory attach"),
	} {
		if m := retiredTerm3277.FindString(text); m != "" {
			t.Errorf("%s says %q (retired term):\n%s", name, m, text)
		}
		if strings.Contains(strings.ToLower(text), "revoke") {
			t.Errorf("%s says detaching revokes access:\n%s", name, text)
		}
	}
	// The runtime strings and comments of attach.go too (#3293): the schema-skew
	// hint is not on any help surface.
	src, err := os.ReadFile("memory/attach.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := retiredTerm3277.FindString(string(src)); m != "" {
		t.Errorf("memory/attach.go says %q (retired term)", m)
	}
	h := read("memory", "detach", "--help")
	for _, want := range []string{"detach", "attachment", "memory itself is unchanged", "--yes", "server decides who may detach", "attach it again"} {
		if !strings.Contains(h, want) {
			t.Errorf("detach help missing %q:\n%s", want, h)
		}
	}
	if !strings.Contains(read("memory", "attach", "--help"), "memory detach") {
		t.Error("attach help must point at memory detach")
	}
}
