package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type nextCall1009 struct {
	op   string
	vars map[string]any
}

func runNext1009(t *testing.T, response string, args ...string) (string, string, int, []nextCall1009) {
	t.Helper()
	var mu sync.Mutex
	calls := []nextCall1009{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string
			Variables     map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		calls = append(calls, nextCall1009{body.OperationName, body.Variables})
		mu.Unlock()
		if body.OperationName != "NextNode" {
			t.Errorf("unexpected %s", body.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "next"}, args...), "--server", srv.URL))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return out.String(), stderr.String(), code, append([]nextCall1009(nil), calls...)
}
func TestNodeNext1009FirstAndAfter(t *testing.T) {
	for _, tail := range [][]string{nil, {"--after", "hrn:node:acme.com:kb:p:c", "--raw"}} {
		args := append([]string{"hrn:node:acme.com:kb:p", "--json"}, tail...)
		out, stderr, code, calls := runNext1009(t, `{"data":{"nextNode":`+nodeDetailJSON+`}}`, args...)
		if code != 0 || len(calls) != 1 || stderr != "" {
			t.Fatalf("code%d calls%v stderr%s", code, calls, stderr)
		}
		v := calls[0].vars
		if v["parentRef"] != "hrn:node:acme.com:kb:p" {
			t.Fatal(v)
		}
		if tail == nil {
			if v["raw"] != false {
				t.Fatal(v)
			}
			if _, ok := v["currentRef"]; ok {
				t.Fatal(v)
			}
		} else if v["currentRef"] != "hrn:node:acme.com:kb:p:c" || v["raw"] != true {
			t.Fatal(v)
		}
		var dto map[string]any
		if json.Unmarshal([]byte(out), &dto) != nil || dto["id"] != "n1" || dto["rendered"] != (tail == nil) {
			t.Fatalf("dto %s", out)
		}
		for _, k := range []string{"content", "role", "tags", "outgoingEdges", "incomingEdges", "revision"} {
			if _, ok := dto[k]; !ok {
				t.Fatalf("missing %s", k)
			}
		}
	}
}
func TestNodeNext1009NullUniform(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		args := []string{"hrn:node:acme.com:kb:p"}
		if jsonMode {
			args = append(args, "--json")
		}
		out, stderr, code, calls := runNext1009(t, `{"data":{"nextNode":null}}`, args...)
		want := "end of sibling chain"
		if jsonMode {
			want = "null"
		}
		if code != 0 || stderr != "" || strings.TrimSpace(out) != want || len(calls) != 1 {
			t.Fatalf("%d %q %q %v", code, out, stderr, calls)
		}
	}
}
func TestNodeNext1009CompoundReferences(t *testing.T) {
	out, _, code, calls := runNext1009(t, `{"data":{"nextNode":null}}`, "parent", "--after", "parent:child", "-m", "acme.com::iris:app-mem:demo")
	if code != 0 || len(calls) != 1 {
		t.Fatalf("%d %s %v", code, out, calls)
	}
	for k, want := range map[string]string{"parentRef": "hrn:node:acme.com::iris:app-mem:demo::parent", "currentRef": "hrn:node:acme.com::iris:app-mem:demo::parent:child"} {
		if calls[0].vars[k] != want {
			t.Fatalf("%s %v", k, calls[0].vars)
		}
	}
}
func TestNodeNext1009InvalidAndRefusal(t *testing.T) {
	for _, args := range [][]string{{"bad"}, {"hrn:node:acme.com:kb:p", "--after="}, {""}, {"hrn:node:acme.com:kb:p", "--memory="}} {
		_, _, code, calls := runNext1009(t, `{}`, args...)
		if code != 2 || len(calls) != 0 {
			t.Fatalf("%v code%d calls%v", args, code, calls)
		}
	}
	out, stderr, code, calls := runNext1009(t, `{"errors":[{"message":"provider refused","extensions":{"code":"FORBIDDEN"}}]}`, "hrn:node:acme.com:kb:p")
	if code != 8 || out != "" || !strings.Contains(stderr, "provider refused") || len(calls) != 1 {
		t.Fatalf("%d %q %q %v", code, out, stderr, calls)
	}
}

func TestNodeNext1009NullArraysAndText(t *testing.T) {
	body := strings.Replace(nodeDetailJSON, `"tags":["ci"]`, `"tags":null`, 1)
	if body == nodeDetailJSON {
		t.Fatal("null tags fixture edit did not land")
	}
	out, _, code, _ := runNext1009(t, `{"data":{"nextNode":`+body+`}}`, "hrn:node:acme.com:kb:p", "--json")
	if code != 0 {
		t.Fatal(out)
	}
	for _, key := range []string{"tags", "outgoingEdges", "incomingEdges"} {
		if strings.Contains(out, `"`+key+`": null`) {
			t.Fatal(out)
		}
	}
	text, _, code, _ := runNext1009(t, `{"data":{"nextNode":`+nodeDetailJSON+`}}`, "hrn:node:acme.com:kb:p")
	if code != 0 || !strings.Contains(text, "Flaky CI") {
		t.Fatalf("text %q code%d", text, code)
	}
}

func TestNodeNext1009EmptyAfterGuidance(t *testing.T) {
	_, stderr, code, calls := runNext1009(t, `{}`, "hrn:node:acme.com:kb:p", "--after=")
	if code != 2 || len(calls) != 0 || !strings.Contains(stderr, "--after must not be empty") {
		t.Fatalf("code%d stderr%q calls%v", code, stderr, calls)
	}
}

// Finder Joshua6359069720: -m scopes bare locs only, never a qualified ref.
func TestNodeNext1009MixedQualifiedAndBareRefs(t *testing.T) {
	for _, tc := range []struct{ parent, after string }{
		{"p", "hrn:node:acme.com:kb:p:c"},
		{"hrn:node:acme.com:kb:p", "p:c"},
		{"hrn:node:acme.com:kb:p", "hrn:node:acme.com:kb:p:c"},
		{"urn:node:acme.com::kb::p", "hrn:node:acme.com:kb:p:c"},
	} {
		_, _, code, calls := runNext1009(t, `{"data":{"nextNode":null}}`, tc.parent, "-m", "acme.com:kb", "--after", tc.after)
		if code != 0 || len(calls) != 1 {
			t.Fatalf("%+v code%d calls%v", tc, code, calls)
		}
		parent := tc.parent
		if parent == "p" {
			parent = "hrn:node:acme.com:kb:p"
		}
		after := tc.after
		if after == "p:c" {
			after = "hrn:node:acme.com:kb:p:c"
		}
		if calls[0].vars["parentRef"] != parent || calls[0].vars["currentRef"] != after {
			t.Fatalf("mixed refs mangled: %+v", calls[0].vars)
		}
	}
}
