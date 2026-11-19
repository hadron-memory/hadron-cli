package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

var chat1280Record = strings.NewReplacer(`"kind":"BIND"`, `"kind":"CHAT"`, `"pointerLevel":"WORKER"`, `"pointerLevel":"APP_AGENT"`).Replace(c3bRecord)

func chat1280Page(items string, total int) string {
	b, _ := json.Marshal(total)
	return `{"data":{"chatServedPrompts":{"items":` + items + `,"total":` + string(b) + `}}}`
}

func TestChat1280ConcealedOrEmptyIsExplicit(t *testing.T) {
	// Missing, denied, serving off and no record are indistinguishable on the provider.
	for _, jsonMode := range []bool{false, true} {
		args := []string{"chat", "served-prompts", "chat-denied"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("[]", 0)}, args...)
		if err != nil || len(*calls) != 1 {
			t.Fatalf("concealed read: %v calls %v", err, *calls)
		}
		if jsonMode {
			var dto struct {
				ServedPrompts *struct {
					Items                []any
					Total, Limit, Offset int
				}
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.ServedPrompts == nil || dto.ServedPrompts.Items == nil || len(dto.ServedPrompts.Items) != 0 || dto.ServedPrompts.Total != 0 {
				t.Fatalf("empty page: %v %s", err, out)
			}
		} else if !strings.Contains(out, "No records on this page") || !strings.Contains(out, "unreadable") {
			t.Fatalf("missing concealment text: %s", out)
		}
	}
}

func TestChat1280OrderAndIndependentPaging(t *testing.T) {
	// The fixture order is the server's order; the locals say only first/second,
	// since the command compares no served times (cli#1314).
	first := strings.Replace(chat1280Record, `"ordinal":1`, `"ordinal":2`, 1)
	second := strings.Replace(chat1280Record, `"id":"rec1"`, `"id":"rec2"`, 1)
	// Provenance limit (3) and offset (1) differ, as do history limit (2) and offset (3):
	// a limit/offset swap at gen.ChatServedPrompts or recordFrom must change an assertion.
	calls, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("["+first+","+second+"]", 9)}, "chat", "served-prompts", "chat-123", "--limit", "2", "--offset", "3", "--provenance-limit", "3", "--provenance-offset", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ServedPrompts struct {
			Items []struct {
				ID, Kind   string
				Provenance struct{ Total, Limit, Offset int }
			}
			Total, Limit, Offset int
		}
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	p := dto.ServedPrompts
	if len(p.Items) != 2 || p.Items[0].ID != "rec1" || p.Items[1].ID != "rec2" || p.Items[0].Kind != "CHAT" || p.Total != 9 || p.Limit != 2 || p.Offset != 3 || p.Items[0].Provenance.Limit != 3 || p.Items[0].Provenance.Offset != 1 {
		t.Fatalf("order/page lost: %s", out)
	}
	c := c2Mutation(t, calls, "ChatServedPrompts")
	if len(*calls) != 1 || c.Session != "" || c.Vars["chatId"] != "chat-123" || c.Vars["historyLimit"] != float64(2) || c.Vars["historyOffset"] != float64(3) || c.Vars["limit"] != float64(3) || c.Vars["offset"] != float64(1) {
		t.Fatalf("request: %+v", *calls)
	}
}

func TestChat1280TextAndCountsOnly(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("["+chat1280Record+"]", 1)}, "chat", "served-prompts", "chat-123", "--provenance-limit", "3", "--provenance-offset", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"served prompts: 1 of 1", "record: rec1", "served revision: 7", "recorded signature", "provenance: 1 of 2 (offset 1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	_, out, err = runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("[]", 4)}, "chat", "served-prompts", "chat-123", "--limit", "0", "--json")
	if err != nil || !strings.Contains(out, `"total": 4`) || !strings.Contains(out, `"items": []`) {
		t.Fatalf("counts: %v %s", err, out)
	}
}

func TestChat1280ProviderErrorsAndBadPages(t *testing.T) {
	cases := []struct {
		body string
		code int
	}{
		{gqlErrorJSON("FORBIDDEN"), 8}, {gqlErrorJSON("NOT_FOUND"), 4},
		{`{"data":{"chatServedPrompts":null}}`, 1}, {chat1280Page("null", 0), 1},
		{chat1280Page("[null]", 1), 1}, {chat1280Page("[]", -1), 1},
		{chat1280Page("["+chat1280Record+"]", 0), 1},
		{chat1280Page("["+strings.Replace(chat1280Record, `"servedRevSeq":7`, `"servedRevSeq":0`, 1)+"]", 1), 1},
	}
	for _, tc := range cases {
		_, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": tc.body}, "chat", "served-prompts", "chat-123", "--json")
		if exitCodeFor(err) != tc.code || out != "" {
			t.Fatalf("body %s: %v %s", tc.body, err, out)
		}
	}
}

func TestChat1280InvalidArgsNeverRead(t *testing.T) {
	cases := [][]string{{}, {" "}, {"chat-123", "--limit", "-1"}, {"chat-123", "--limit", "201"}, {"chat-123", "--offset", "-1"}, {"chat-123", "--offset", "2147483648"}, {"chat-123", "--provenance-limit", "201"}, {"chat-123", "--provenance-offset", "-1"}}
	for _, suffix := range cases {
		calls, _, err := runTeamTask(t, nil, append([]string{"chat", "served-prompts"}, suffix...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("args %v: %v calls %v", suffix, err, *calls)
		}
	}
}

func TestChat1280HelpDistinguishesChatbotFromChannel(t *testing.T) {
	_, out, err := runTeamTask(t, nil, "chat", "served-prompts", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"chat-id", "chatbot", "Channel", "newest", "--provenance-limit", "empty", "App", "sensitive"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help lacks %q: %s", want, out)
		}
	}
}

func TestChat1280NoAmbientChatConfig(t *testing.T) {
	t.Setenv("HADRON_CHAT_NODE", "not-a-node-URN")
	calls, _, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("[]", 0)}, "chat", "served-prompts", "explicit-chat", "--app", "irrelevant-context")
	if err != nil || len(*calls) != 1 {
		t.Fatalf("ambient context used: %v %v", err, *calls)
	}
	if c2Mutation(t, calls, "ChatServedPrompts").Vars["chatId"] != "explicit-chat" {
		t.Fatal("explicit id lost")
	}
}

func TestChat1280RecordOnlyOperation(t *testing.T) {
	// Check the generated document sent on the wire, not captured variables: parse
	// it and compare the whole selection against an exact allowlist, so an alias,
	// a second field on one line or a new selection cannot slip past (cli#1314).
	op := gen.ChatServedPrompts_Operation
	doc, err := parser.ParseQuery(&ast.Source{Name: "ChatServedPrompts", Input: op})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Operations) != 1 || len(doc.Fragments) != 1 {
		t.Fatalf("want one operation and one fragment, got %d and %d", len(doc.Operations), len(doc.Fragments))
	}
	// allowed maps a field to the exact fields it may select; nil = scalar leaf.
	type shape map[string]shape
	record := shape{"id": nil, "kind": nil, "ordinal": nil, "pointerLevel": nil, "compiledNodeId": nil, "compiledContentHash": nil,
		"servedRevSeq": nil, "provenanceRevSeq": nil, "servedAt": nil, "variables": nil, "pruned": nil,
		"provenance": {"total": nil, "items": {"nodeId": nil, "revSeq": nil, "hash": nil, "pruned": nil}}}
	var walk func(path string, set ast.SelectionSet, want shape)
	walk = func(path string, set ast.SelectionSet, want shape) {
		seen := map[string]bool{}
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				if s.Alias != s.Name {
					t.Errorf("%s: alias %q on %q", path, s.Alias, s.Name)
				}
				sub, ok := want[s.Name]
				if !ok {
					t.Errorf("%s: selects non-record field %q", path, s.Name)
					continue
				}
				seen[s.Name] = true
				if sub == nil && len(s.SelectionSet) != 0 {
					t.Errorf("%s.%s: a scalar must have no selection", path, s.Name)
				}
				if sub != nil {
					walk(path+"."+s.Name, s.SelectionSet, sub)
				}
			case *ast.FragmentSpread:
				if s.Name != "ServedPromptRecord" || path != "query.chatServedPrompts.items" {
					t.Errorf("%s: unexpected fragment spread %q", path, s.Name)
				}
			default:
				t.Errorf("%s: unexpected selection %T", path, sel)
			}
		}
		for name := range want {
			if path != "query.chatServedPrompts.items" && !seen[name] {
				t.Errorf("%s: record field %q is no longer selected", path, name)
			}
		}
	}
	top := doc.Operations[0].SelectionSet
	if len(top) != 1 {
		t.Fatalf("want exactly one top-level selection, got %d", len(top))
	}
	walk("query", top, shape{"chatServedPrompts": {"total": nil, "items": {}}})
	// The fragment holds the record's fields; the items selection holds only its spread.
	walk("fragment", doc.Fragments[0].SelectionSet, record)
	if f := top[0].(*ast.Field); f.Arguments.ForName("limit") == nil || f.Arguments.ForName("offset") == nil || f.Arguments.ForName("chatId") == nil || len(f.Arguments) != 3 {
		t.Fatal("record-only query must pass exactly chatId, limit and offset")
	}
}

// An older server refuses the new route's query as schema skew; the chat route
// maps it to the usage exit with the out-of-sync advice, one request, no retry (cli#1314).
func TestChat1280SchemaSkewMapsToUsage(t *testing.T) {
	skew := `{"errors":[{"message":"Cannot query field \"chatServedPrompts\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, _, err := runTeamTask(t, map[string]string{"ChatServedPrompts": skew}, "chat", "served-prompts", "chat-123")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "out of sync") || len(*calls) != 1 {
		t.Fatalf("skew: exit %d err %v calls %d", exitCodeFor(err), err, len(*calls))
	}
}

// A provenance page larger than the requested limit is refused, never rendered.
func TestChat1280OversizedProvenancePageRefused(t *testing.T) {
	two := strings.Replace(chat1280Record, `"items":[{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false}]`,
		`"items":[{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false},{"nodeId":"source2","revSeq":4,"hash":"h2","pruned":false}]`, 1)
	if two == chat1280Record {
		t.Fatal("fixture edit did not apply")
	}
	_, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("["+two+"]", 1)}, "chat", "served-prompts", "chat-123", "--provenance-limit", "1")
	if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "invalid served record/provenance page") || strings.Contains(out, "source2") {
		t.Fatalf("oversized provenance page: exit %d err %v out %s", exitCodeFor(err), err, out)
	}
}
