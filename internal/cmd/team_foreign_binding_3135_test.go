package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
)

// cli#3135: the follow-ups to the cli#3091 foreign-binding guard.

const (
	read3135     = `query R { me { id } }`
	mutation3135 = `mutation M($v: JSON) { createTeamChatMessage(input: $v) { id } }`
	authored3135 = `mutation A { startAgentTask(id: "t") { id } }`
)

type guardRun3135 struct {
	err              error
	parses, gitCalls int
}

// guard3135 runs the guard itself on one request, in a worktree bound to s-new.
func guard3135(t *testing.T, invoker, optIn string, req *graphql.Request) guardRun3135 {
	t.Helper()
	writeTeamBinding(t)
	t.Setenv("ORCA_WORKSPACE_ID", invoker)
	t.Setenv(team.ForeignBindingOptInEnv, optIn)
	var r guardRun3135
	t.Cleanup(team.SetGitToplevelRunner(func(context.Context) (string, error) {
		r.gitCalls++
		return foreignRoot3091, nil
	}))
	t.Cleanup(team.SetQueryParser(func(next func(*ast.Source) (*ast.QueryDocument, error)) func(*ast.Source) (*ast.QueryDocument, error) {
		return func(s *ast.Source) (*ast.QueryDocument, error) {
			r.parses++
			return next(s)
		}
	}))
	r.err = team.ForeignBindingGuard()(context.Background(), req)
	return r
}

// N1: the guard judges one request, so it must not claim that earlier reads
// were not sent.
func TestForeignBindingRefusalWording3135(t *testing.T) {
	r := guard3135(t, "id1::"+ownRoot3091, "", &graphql.Request{OpName: "A", Query: authored3135})
	const want = "this worktree (/work/tina) is bound to Iris, but your terminal belongs to /work/gloria; the write would act as Iris, so that write was not sent. Run it from your own worktree, or set HADRON_ALLOW_FOREIGN_BINDING=1 to act through this binding on purpose"
	if r.err == nil || r.err.Error() != want || exitCodeFor(r.err) != exitcode.Usage {
		t.Fatalf("got  %v\nwant %s", r.err, want)
	}
}

func TestForeignBindingAgenticUsage3135(t *testing.T) {
	data, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	var lines int
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "# cli#3091:") {
			line, lines = l, lines+1
		}
	}
	if lines != 1 {
		t.Fatalf("%d cli#3091 lines in agentic-usage.md, want 1", lines)
	}
	if want := "(exit 2; the refused write is not sent, though reads the command makes first may be)"; !strings.Contains(line, want) {
		t.Errorf("missing %q", want)
	}
	for _, stale := range []string{"(exit 2, nothing sent)", "nothing was sent"} {
		if strings.Contains(line, stale) {
			t.Errorf("stale %q", stale)
		}
	}
}

// N2: which variables name the binding's session.
func TestForeignBindingSessionMatch3135(t *testing.T) {
	for name, tc := range map[string]struct {
		vars   any
		refuse bool
	}{
		"top-level value":             {map[string]any{"sessionRef": "s-new"}, true},
		"nested value":                {map[string]any{"input": map[string]any{"a": []any{map[string]any{"s": "s-new"}}}}, true},
		"exact nested key":            {map[string]any{"input": map[string]any{"s-new": 1}}, true},
		"free text is the id":         {map[string]any{"body": "s-new"}, true},
		"upper-cased":                 {map[string]any{"sessionRef": "S-NEW"}, true},
		"space-padded":                {map[string]any{"sessionRef": "  s-new\n"}, true},
		"reference form":              {map[string]any{"sessionRef": "x:s-new"}, true},
		"urn, upper-cased":            {map[string]any{"sessionRef": "hrn:session:acme.com:S-NEW "}, true},
		"nested reference":            {map[string]any{"input": []any{"x:s-new"}}, true},
		"padded key":                  {map[string]any{"input": map[string]any{" S-new ": 1}}, true},
		"reference key":               {map[string]any{"input": map[string]any{"x:s-new": 1}}, true},
		"typed variables":             {struct{ Ref *string }{new3135("S-NEW")}, true},
		"prose":                       {map[string]any{"body": "session s-new ended"}, false},
		"prose ending in the id":      {map[string]any{"body": "see session: s-new"}, false},
		"prose ending in a reference": {map[string]any{"body": "see session:s-new"}, false},
		"lines ending in a reference": {map[string]any{"body": "handing over\nsession:s-new"}, false},
		"quoted prose":                {map[string]any{"body": `it is "s-new" here`}, false},
		"json in a string":            {map[string]any{"body": `{"sessionRef":"s-new"}`}, false},
		"longer id":                   {map[string]any{"sessionRef": "s-new2"}, false},
		"prefix without a colon":      {map[string]any{"sessionRef": "xs-new"}, false},
		"key that contains it":        {map[string]any{"input": map[string]any{"s-new-note": 1}}, false},
		"another session":             {map[string]any{"sessionRef": "s-other"}, false},
		"no string in it":             {map[string]any{"a": 1, "b": nil, "c": true}, false},
		"unencodable":                 {map[string]any{"a": make(chan int), "s": "s-new"}, false},
		"no variables":                {nil, false},
		"after a huge number":         {json.RawMessage(`{"n":1e999,"s":"S-NEW"}`), true},
		"raw, duplicate key":          {json.RawMessage(`{"s":"s-new","s":"other"}`), true},
		"tail of a quoted string":     {map[string]any{"body": `see "s-new`}, true},
	} {
		t.Run(name, func(t *testing.T) {
			r := guard3135(t, "id1::"+ownRoot3091, "", &graphql.Request{OpName: "M", Query: mutation3135, Variables: tc.vars})
			if (r.err != nil) != tc.refuse {
				t.Fatalf("refuse=%v, got %v", tc.refuse, r.err)
			}
			if r.err != nil && exitCodeFor(r.err) != exitcode.Usage {
				t.Fatalf("exit %d", exitCodeFor(r.err))
			}
		})
	}
}

func new3135(s string) *string { return &s }

// N3: the cheap checks come before any parse, and one parse serves the rest.
func TestForeignBindingParseCost3135(t *testing.T) {
	foreign, own := "id1::"+ownRoot3091, "id1::"+foreignRoot3091
	named := map[string]any{"s": "s-new"}
	for name, tc := range map[string]struct {
		invoker, optIn, op, query string
		vars                      any
		parses, git               int
		refuse                    bool
	}{
		"no signal, read":                 {"", "", "R", read3135, nil, 0, 0, false},
		"no signal, mutation":             {"", "", "M", mutation3135, named, 0, 0, false},
		"no signal, authored":             {"", "", "A", authored3135, nil, 0, 0, false},
		"malformed signal":                {"no-separator", "", "A", authored3135, nil, 0, 0, false},
		"opt-in, read":                    {foreign, "1", "R", read3135, nil, 0, 0, false},
		"opt-in, mutation":                {foreign, "1", "M", mutation3135, named, 0, 0, false},
		"opt-in, authored":                {foreign, "1", "A", authored3135, nil, 0, 0, false},
		"foreign, read":                   {foreign, "", "R", read3135, named, 1, 0, false},
		"foreign, mutation":               {foreign, "", "M", mutation3135, named, 1, 1, true},
		"foreign, unrelated mutation":     {foreign, "", "M", mutation3135, nil, 1, 1, false},
		"foreign, authored":               {foreign, "", "A", authored3135, nil, 1, 1, true},
		"foreign, unnamed operation":      {foreign, "", "", authored3135, nil, 1, 1, true},
		"foreign, unparseable":            {foreign, "", "M", "mutation {", named, 1, 0, false},
		"own, authored":                   {own, "", "A", authored3135, nil, 1, 1, false},
		"opt-in is exactly 1, not zero":   {foreign, "0", "A", authored3135, nil, 1, 1, true},
		"opt-in is exactly 1, not true":   {foreign, "true", "A", authored3135, nil, 1, 1, true},
		"opt-in is exactly 1, not padded": {foreign, " 1", "A", authored3135, nil, 1, 1, true},
	} {
		t.Run(name, func(t *testing.T) {
			r := guard3135(t, tc.invoker, tc.optIn, &graphql.Request{OpName: tc.op, Query: tc.query, Variables: tc.vars})
			if (r.err != nil) != tc.refuse {
				t.Fatalf("refuse=%v, got %v", tc.refuse, r.err)
			}
			if r.parses != tc.parses || r.gitCalls != tc.git {
				t.Fatalf("parses=%d git=%d, want %d and %d", r.parses, r.gitCalls, tc.parses, tc.git)
			}
		})
	}
}
