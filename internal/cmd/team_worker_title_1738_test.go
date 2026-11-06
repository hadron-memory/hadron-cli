package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func titleWorker1738(t *testing.T, changes map[string]any) string {
	t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(profileWorkerJSON), &w); err != nil {
		t.Fatal(err)
	}
	for k, v := range changes {
		w[k] = v
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runTitle1738(t *testing.T, body string, args ...string) (string, *profileOpLog, error) {
	t.Helper()
	teamGitDir(t)
	log := &profileOpLog{}
	srv, _ := captureGraphQLFunc(t, func(op string) string { log.add(op); return body })
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "terminal-title"}, append(args, "--server", srv.URL)...))
	err := root.Execute()
	return out.String(), log, err
}

func TestWorkerTerminalTitle1738(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"reported", map[string]any{"model": "claude-sonnet-5-5", "family": "claude", "reportsTo": map[string]any{"id": "leader", "name": "Ada", "urn": nil}, "reportsToRetired": true}, "Iris [backend-engineer, claude-sonnet-5-5, Ada]\n"},
		{"family", map[string]any{"family": "codex"}, "Iris [backend-engineer, codex]\n"},
		{"masked", map[string]any{}, "Iris [backend-engineer, model unavailable]\n"},
		{"role-null", map[string]any{"role": nil}, "Iris [role unavailable, model unavailable]\n"},
		{"scrub-fallback", map[string]any{"role": "\x1b\u202e[,]\n", "model": "[,]\u0085", "family": "glm"}, "Iris [role unavailable, glm]\n"},
		{"scrub-all", map[string]any{"name": " I[ri],s\n\u202e", "role": " dev\trole ", "model": "M\u2028 x", "reportsTo": map[string]any{"id": "l", "name": "A,da", "urn": nil}}, "I ri s [dev role, M x, A da]\n"},
		{"blank-model-family", map[string]any{"model": "[,]", "family": "\u2029"}, "Iris [backend-engineer, model unavailable]\n"},
		{"retired-worker", map[string]any{"retiredAt": "2026-01-01T00:00:00Z"}, "Iris [backend-engineer, model unavailable]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, log, err := runTitle1738(t, `{"data":{"worker":`+titleWorker1738(t, tc.fields)+`}}`, profileHexID)
			if err != nil || out != tc.want {
				t.Fatalf("out=%q err=%v want=%q", out, err, tc.want)
			}
			if log.String() != "GetWorker" {
				t.Fatalf("ops=%s", log)
			}
		})
	}
}

func TestWorkerTerminalTitleJSON1738(t *testing.T) {
	fields := map[string]any{"name": "I[ris\n", "model": "\u202e[,]", "family": "glm", "role": nil, "teamPlacement": nil, "reportsTo": map[string]any{"id": "l", "name": "A,da", "urn": nil}, "reportsToRetired": true}
	out, log, err := runTitle1738(t, `{"data":{"worker":`+titleWorker1738(t, fields)+`}}`, "hrn:worker:acme.com:eng-team:iris", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if log.String() != "GetWorker" {
		t.Fatal(log)
	}
	var got map[string]any
	if err = json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"title", "worker", "parts", "model_source", "teamPlacement", "reportsToRetired"} {
		if _, ok := got[k]; !ok {
			t.Fatal("missing", k)
		}
	}
	if len(got) != 6 || got["model_source"] != "family-fallback" || got["teamPlacement"] != nil || got["reportsToRetired"] != true {
		t.Fatal(got)
	}
	parts := got["parts"].(map[string]any)
	if len(parts) != 5 || parts["name"] != fields["name"] || parts["model"] != fields["model"] || parts["role"] != nil || parts["family"] != "glm" {
		t.Fatal(parts)
	}
	leader := parts["reports_to"].(map[string]any)
	if len(leader) != 3 || leader["name"] != "A,da" || leader["urn"] != nil {
		t.Fatal(leader)
	}
	worker := got["worker"].(map[string]any)
	if len(worker) != 3 || worker["id"] != profileHexID || worker["name"] != fields["name"] {
		t.Fatal(worker)
	}
	for _, tc := range []struct {
		fields map[string]any
		source string
	}{{map[string]any{}, "none"}, {map[string]any{"model": "raw-model"}, "reported-by-session"}} {
		out, _, err = runTitle1738(t, `{"data":{"worker":`+titleWorker1738(t, tc.fields)+`}}`, profileHexID, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		parts = got["parts"].(map[string]any)
		if got["model_source"] != tc.source || parts["reports_to"] != nil || parts["family"] != nil {
			t.Fatal(got)
		}
	}
}

func TestWorkerTerminalTitleBound1738(t *testing.T) {
	out, _, err := runTitle1738(t, `{"data":{"worker":`+titleWorker1738(t, map[string]any{"name": strings.Repeat("界", 300), "model": "\x00\x7f\x85\u202e\u2028\u2029"})+`}}`, profileHexID)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(out, "\n")
	if len([]rune(line)) != 256 || !strings.HasSuffix(line, "…]") || strings.Count(out, "\n") != 1 {
		t.Fatalf("out=%q runes=%d", out, len([]rune(line)))
	}
	for _, r := range line {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			t.Fatalf("unsafe %U", r)
		}
	}
}

func TestWorkerTerminalTitleRefusals1738(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"data":{"worker":null}}`, 4},
		{`{"errors":[{"message":"hidden","extensions":{"code":"WORKER_NOT_FOUND"}}]}`, 4},
		{`{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{`{"errors":[{"message":"bad","extensions":{"code":"BAD_USER_INPUT"}}]}`, 2},
	} {
		out, log, err := runTitle1738(t, tc.body, profileHexID)
		var coded *exitcode.CodedError
		errors.As(err, &coded)
		if err == nil {
			t.Fatal("wanted refusal")
		}
		if coded.Code != tc.code || out != "" || log.String() != "GetWorker" {
			t.Fatalf("out=%q err=%v code=%d ops=%s", out, err, coded.Code, log)
		}
	}
	out, log, err := runTitle1738(t, `{}`, profileHexID, "extra")
	if err == nil || out != "" || log.String() != "" {
		t.Fatalf("usage: %q %v %s", out, err, log)
	}
}
