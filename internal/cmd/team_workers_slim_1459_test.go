package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1459 (hadron-server#2970): name resolution and the orca reads rode the
// full `Workers` document, which selects every worker's boot briefing. On a
// 100-worker App that was 3.4 MB and ~13 s of server time per name lookup.
//
// These pins use a SELECTION-HONOURING fake: a list item carries its
// multi-KB prompt (and the signature/trailer templates) only when the request
// document selects those fields, the way the real server answers. A static
// fixture would always carry them, or never, and could not tell a slim
// document from a full one.

const (
	slim1459Workers    = 100
	slim1459PromptSize = 40 * 1024
	slim1459Budget     = 50 * 1024
)

var slim1459Prompt = "You are Iris. " + strings.Repeat("x", slim1459PromptSize)

// slim1459Selects reports whether the document selects field: genqlient
// prints one selection per line, so a line that is the field, or the field
// opening a sub-selection, is a selection of it.
func slim1459Selects(query, field string) bool {
	for _, line := range strings.Split(query, "\n") {
		l := strings.TrimSpace(line)
		if l == field || strings.HasPrefix(l, field+" {") {
			return true
		}
	}
	return false
}

// slim1459WorkerJSON renders worker i. Worker 0 is Iris; the rest are filler.
// Every key is written only if the request document selects it, as the server
// answers; the briefing is the multi-KB one.
func slim1459WorkerJSON(i int, query string) string {
	name, slug := fmt.Sprintf("Worker%03d", i), fmt.Sprintf("worker%03d", i)
	if i == 0 {
		name, slug = "Iris", "iris"
	}
	w := map[string]any{
		"id": fmt.Sprintf("wkr%d", i+1), "urn": "hrn:worker:acme.com:eng-team:" + slug, "slug": slug,
		"portalUrl": nil, "appId": "capp100000000000000000000", "agentId": "agt1", "name": name,
		"reportsTo": nil, "reportsToRetired": false, "role": "backend-engineer",
		"bootPromptRef": nil, "bootPromptTargetDeleted": nil, "repos": []string{}, "memoryId": "mw1",
		"family": nil, "model": nil, "modelReportedAt": nil, "tier": nil,
		"heldByUserId": nil, "heldAt": nil, "hasLiveSession": false, "lastActiveAt": nil, "checkStatus": nil,
		"retiredAt": nil, "retiredBy": nil, "createdAt": "2026-08-14T00:00:00Z", "createdBy": "u-holger",
	}
	w["prompt"] = slim1459Prompt
	w["signature"] = strings.Repeat("s", 512)
	w["trailer"] = strings.Repeat("t", 512)
	for k := range w {
		if !slim1459Selects(query, k) {
			delete(w, k)
		}
	}
	raw, _ := json.Marshal(w)
	return string(raw)
}

type slim1459Server struct {
	URL   string
	mu    sync.Mutex
	bytes map[string]int // response bytes per operation
	calls []string
}

func (s *slim1459Server) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.bytes {
		n += b
	}
	return n
}

func newSlim1459Server(t *testing.T, extra map[string]string) *slim1459Server {
	t.Helper()
	s := &slim1459Server{bytes: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Query         string          `json:"query"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var vars struct {
			Ref    string `json:"ref"`
			Offset *int   `json:"offset"`
		}
		_ = json.Unmarshal(body.Variables, &vars)
		var resp string
		switch body.OperationName {
		case "Workers", "WorkersRoster":
			items := []string{}
			if vars.Offset == nil || *vars.Offset == 0 {
				for i := 0; i < slim1459Workers; i++ {
					items = append(items, slim1459WorkerJSON(i, body.Query))
				}
			}
			resp = fmt.Sprintf(`{"data":{"workers":{"total":%d,"items":[%s]}}}`, slim1459Workers, strings.Join(items, ","))
		case "GetWorker":
			resp = `{"data":{"worker":null}}`
			for i := 0; i < slim1459Workers; i++ {
				if vars.Ref == fmt.Sprintf("wkr%d", i+1) {
					resp = `{"data":{"worker":` + slim1459WorkerJSON(i, body.Query) + `}}`
				}
			}
		default:
			var ok bool
			if resp, ok = extra[body.OperationName]; !ok {
				if resp, ok = unstubbedDefault(body.OperationName); !ok {
					t.Errorf("unexpected operation %q", body.OperationName)
					resp = `{"errors":[{"message":"unexpected operation"}]}`
				}
			}
		}
		s.mu.Lock()
		s.bytes[body.OperationName] += len(resp)
		s.calls = append(s.calls, body.OperationName)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Acceptance 1: the list document used for name resolution and by the orca
// reads selects no briefing, signature or trailer template.
func TestWorkersDocumentSelectsNoBriefing1459(t *testing.T) {
	doc := gen.Workers_Operation
	for _, field := range []string{"prompt", "signature", "trailer"} {
		if slim1459Selects(doc, field) {
			t.Errorf("the Workers document selects %q; a 100-worker list then carries every worker's copy:\n%s", field, doc)
		}
	}
}

// Acceptance 2 + 4: `worker get <Name>` reads ONE worker's briefing, no more,
// and still prints the whole of it.
func TestWorkerGetByNameReadsOneBriefing1459(t *testing.T) {
	s := newSlim1459Server(t, map[string]string{"TeamAppIdentity": teamAppIdentityJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "Iris", "--app", "hrn:app:acme.com:eng-team", "--json", "--server", s.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, limit := s.total(), slim1459PromptSize+slim1459Budget; got > limit {
		t.Errorf("worker get Iris read %d bytes (%v), over one briefing plus a slim list (%d)", got, s.bytes, limit)
	}
	var dto struct {
		ID     string  `json:"id"`
		Prompt *string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if dto.ID != "wkr1" || dto.Prompt == nil || *dto.Prompt != slim1459Prompt {
		t.Errorf("worker get Iris must still carry the full briefing: id=%q prompt bytes=%d", dto.ID, len(deref1459(dto.Prompt)))
	}
}

// Acceptance 4: session start by NAME still prints the boot briefing, now
// read from the one resolved worker instead of the staff scan.
func TestSessionStartByNameKeepsBriefing1459(t *testing.T) {
	teamGitDir(t)
	s := newSlim1459Server(t, map[string]string{
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--tool", "claude-code",
		"--app", "acme.com:eng-team", "--server", s.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), slim1459Prompt) {
		t.Errorf("session start --as Iris must print the whole boot briefing (got %d bytes of output)", len(out.String()))
	}
	if got, limit := s.total(), slim1459PromptSize+slim1459Budget; got > limit {
		t.Errorf("session start --as Iris read %d bytes (%v), over one briefing plus a slim list (%d)", got, s.bytes, limit)
	}
}

// Acceptance 5 (no-regression guard, green on main): `worker list --json`
// stays on the roster projection, under the budget on a 100-worker App.
func TestWorkerListStaysSlim1459(t *testing.T) {
	s := newSlim1459Server(t, map[string]string{"TeamAppIdentity": teamAppIdentityJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "hrn:app:acme.com:eng-team", "--json", "--server", s.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := s.total(); got > slim1459Budget {
		t.Errorf("worker list --json read %d bytes (%v), over %d", got, s.bytes, slim1459Budget)
	}
	if strings.Contains(out.String(), `"prompt"`) {
		t.Error("worker list --json must not carry a prompt key")
	}
}

func deref1459(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
