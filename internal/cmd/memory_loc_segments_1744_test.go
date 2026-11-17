package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type locRun1744 struct {
	out, stderr string
	err         error
	vars        []map[string]json.RawMessage
}

func runLoc1744(t *testing.T, body string, args ...string) locRun1744 {
	t.Helper()
	var mu sync.Mutex
	calls := []map[string]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var q struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		if q.OperationName != "MemoryLocSegments" {
			t.Errorf("unexpected operation %s", q.OperationName)
		}
		mu.Lock()
		calls = append(calls, q.Variables)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"memory", "loc-segments", "--server", srv.URL}, args...))
	err := root.Execute()
	mu.Lock()
	defer mu.Unlock()
	return locRun1744{out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err, calls}
}

const locPage1744 = `{"data":{"memoryLocSegments":{"items":[{"segment":"z","hasNode":false,"memoryId":"inherited"},{"segment":"z","hasNode":true,"memoryId":"other"}],"hasMore":true,"endCursor":"opaque+/:="}}}`

func TestMemoryLocSegmentsWire1744(t *testing.T) {
	r := runLoc1744(t, locPage1744, "--memory", "raw,comma", "--memory", "hrn:mem:acme.com:kb", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if len(r.vars) != 1 {
		t.Fatalf("want one query, got %d", len(r.vars))
	}
	v := r.vars[0]
	if string(v["memoryIds"]) != `["raw,comma","hrn:mem:acme.com:kb"]` || string(v["parentLoc"]) != `""` {
		t.Fatalf("variables %v", v)
	}
	if _, ok := v["limit"]; ok {
		t.Fatal("absent limit must be omitted")
	}
	if _, ok := v["after"]; ok {
		t.Fatal("absent after must be omitted")
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || string(got["hasMore"]) != "true" || string(got["endCursor"]) != `"opaque+/:="` {
		t.Fatalf("shape %s", r.out)
	}
	var items []map[string]any
	_ = json.Unmarshal(got["items"], &items)
	if len(items) != 2 || items[0]["memoryId"] != "inherited" || items[0]["hasNode"] != false || items[1]["hasNode"] != true {
		t.Fatalf("rows %s", r.out)
	}
	for _, item := range items {
		if len(item) != 3 {
			t.Fatalf("extra fields %v", item)
		}
	}
	r = runLoc1744(t, `{"data":{"memoryLocSegments":{"items":[],"hasMore":false,"endCursor":null}}}`, "--memory", "raw,comma", "--parent", " invalid:parent ", "--after", "opaque+/:=", "--limit", "999", "--json")
	if r.err != nil || len(r.vars) != 1 {
		t.Fatalf("continuation %v %v", r.err, r.vars)
	}
	if string(r.vars[0]["after"]) != `"opaque+/:="` || string(r.vars[0]["limit"]) != "999" || string(r.vars[0]["parentLoc"]) != `" invalid:parent "` {
		t.Fatalf("continuation vars %v", r.vars)
	}
	if !strings.Contains(r.out, `"items": []`) || !strings.Contains(r.out, `"endCursor": null`) {
		t.Fatal(r.out)
	}
}
func TestMemoryLocSegmentsUsage1744(t *testing.T) {
	for _, args := range [][]string{nil, {"--memory", ""}, {"--memory", "m", "--limit", "0"}, {"--memory", "m", "--limit", "-1"}, {"--memory", "m", "--after", ""}} {
		r := runLoc1744(t, locPage1744, args...)
		if r.err == nil || exitCodeFor(r.err) != 2 || len(r.vars) != 0 || r.out != "" {
			t.Fatalf("args%v err%v calls%d out%q", args, r.err, len(r.vars), r.out)
		}
	}
}
func TestMemoryLocSegmentsMalformed1744(t *testing.T) {
	for _, page := range []string{`null`, `{}`, `{"items":null}`, `{"items":[null]}`, `{"items":[{"segment":"","memoryId":"m"}]}`, `{"items":[{"segment":"a","memoryId":""}]}`, `{"items":[],"hasMore":true}`, `{"items":[],"hasMore":true,"endCursor":""}`, `{"items":[],"hasMore":false,"endCursor":"bad\nnext"}`} {
		r := runLoc1744(t, `{"data":{"memoryLocSegments":`+page+`}}`, "--memory", "m", "--json")
		if r.err == nil || r.out != "" || r.stderr != "" || len(r.vars) != 1 {
			t.Errorf("page%s err%v out%q stderr%q calls%d", page, r.err, r.out, r.stderr, len(r.vars))
		}
	}
}
func TestMemoryLocSegmentsTextAndNull1744(t *testing.T) {
	r := runLoc1744(t, locPage1744, "--memory", "m")
	if r.err != nil || !strings.Contains(r.out, "MEMORY ID") || strings.Contains(r.out, "more segments") || r.stderr != "more segments remain — continue with --after 'opaque+/:='\n" {
		t.Fatalf("text err%v out%q stderr%q", r.err, r.out, r.stderr)
	}
	// genqlient bools cannot distinguish null from false; retain/pin that limit.
	r = runLoc1744(t, `{"data":{"memoryLocSegments":{"items":[{"segment":"a","hasNode":null,"memoryId":"m"}],"hasMore":false,"endCursor":"last"}}}`, "--memory", "m", "--json")
	if r.err != nil || !strings.Contains(r.out, `"hasNode": false`) || !strings.Contains(r.out, `"endCursor": "last"`) {
		t.Fatalf("null bool: %v %s", r.err, r.out)
	}
}
func TestMemoryLocSegmentsErrors1744(t *testing.T) {
	for _, body := range []string{`{"errors":[{"message":"no access","extensions":{"code":"FORBIDDEN"}}]}`, `{"errors":[{"message":"Cannot query field \"memoryLocSegments\" on type \"Query\"."}]}`} {
		r := runLoc1744(t, body, "--memory", "m", "--json")
		if r.err == nil || r.out != "" || len(r.vars) != 1 {
			t.Fatalf("err%v out%q calls%d", r.err, r.out, len(r.vars))
		}
	}
}

func TestMemoryLocSegmentsSafeText1744(t *testing.T) {
	body := `{"data":{"memoryLocSegments":{"items":[{"segment":"a\nb","hasNode":false,"memoryId":"m\tq"}],"hasMore":false,"endCursor":null}}}`
	r := runLoc1744(t, body, "--memory", "m")
	if r.err != nil || strings.Contains(r.out, "a\nb") || strings.Contains(r.out, "m\tq") || r.stderr != "" {
		t.Fatalf("text err%v out%q stderr%q", r.err, r.out, r.stderr)
	}
	r = runLoc1744(t, body, "--memory", "m", "--json")
	if r.err != nil || !strings.Contains(r.out, `"a\nb"`) || !strings.Contains(r.out, `"m\tq"`) {
		t.Fatalf("raw JSON: %v %q", r.err, r.out)
	}
	for _, arg := range []string{"empty", "unreadable", "garbage"} {
		r = runLoc1744(t, `{"data":{"memoryLocSegments":{"items":[],"hasMore":false,"endCursor":null}}}`, "--memory", arg)
		if r.err != nil || r.stderr != "" || !strings.Contains(r.out, "MEMORY ID") || strings.Contains(r.out, "not found") {
			t.Fatalf("empty %s err%v out%q stderr%q", arg, r.err, r.out, r.stderr)
		}
	}
}
