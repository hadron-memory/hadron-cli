package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1736: `spec rescind` and `spec supersede` retire through the server's one
// retireSpec door (hadron-server#3429, cor:spc:010:02). The server composes the
// text and owns every refusal; the CLI sends one request and reports what the
// answer, or one current-state re-read, shows. Synthetic transport only.

const (
	door1736Old = "msg:010:02"
	door1736By  = "msg:010:03"
	door1736New = "msg:010:09"
)

type door1736 struct {
	t *testing.T
	// The retired spec as the PRE-READ sees it.
	tags     []string
	abstract string
	// answer is retireSpec's reply; zero value = success.
	status int
	reply  string
	// state is the re-read's `spec`; "" = the pre-read's facts, "null" = unreadable.
	state     string
	successor string // the re-read's `successor`, default a usable one
	readFails bool   // the re-read gets no answer
	calls     []string
	vars      map[string][]map[string]any
}

func spec1736(id, loc, memory string, revision int, tags, abstract, edges string) string {
	return `{"id":"` + id + `","memoryId":"` + memory + `","loc":"` + loc + `","revision":` + jsonInt1736(revision) +
		`,"tags":` + tags + `,"abstract":` + abstract + `,"outgoingEdges":[` + edges + `]}`
}

func jsonInt1736(n int) string { b, _ := json.Marshal(n); return string(b) }

func edge1736(targetID, loc, memory string) string {
	return `{"name":"superseded-by","target":{"id":"` + targetID + `","loc":"` + loc + `","memoryId":"` + memory + `"}}`
}

const unreadableEdge1736 = `{"name":"superseded-by","target":null}`

func success1736(form string) string {
	abstract := "RESCINDED (2026-11-26); do not apply. why Historical: old"
	edge := "null"
	if form == "SUPERSEDE" {
		abstract = "SUPERSEDED (2026-11-26) by msg:010:03 (W3); do not apply. Historical: old"
		edge = `{"id":"e1","name":"superseded-by","target":{"id":"sp3","loc":"msg:010:03","name":"msg:010:03 — W3","tags":["spec","p2"]}}`
	}
	return `{"data":{"retireSpec":{"node":{"id":"sp1","loc":"msg:010:02","revision":8,"tags":["spec","superseded"],"abstract":"` + abstract + `"},"successorEdge":` + edge + `}}}`
}

func refusal1736(code string) string {
	return `{"data":null,"errors":[{"message":"The spec cannot be retired in its current state; re-read it and retry.","path":["retireSpec"],"extensions":{"code":"` + code + `"}}]}`
}

func (s *door1736) serve() *httptest.Server {
	s.vars = map[string][]map[string]any{}
	if s.tags == nil {
		s.tags = []string{"spec"}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		op := body.OperationName
		s.calls = append(s.calls, op)
		s.vars[op] = append(s.vars[op], body.Variables)
		w.Header().Set("Content-Type", "application/json")
		tags, _ := json.Marshal(s.tags)
		abstract := "null"
		if s.abstract != "" {
			b, _ := json.Marshal(s.abstract)
			abstract = string(b)
		}
		switch op {
		case "ResolveUrn":
			urn, _ := body.Variables["urn"].(string)
			id := ""
			switch {
			case strings.HasSuffix(urn, door1736Old):
				id = "sp1"
			case strings.HasSuffix(urn, door1736By):
				id = "sp3"
			}
			if id == "" {
				_, _ = w.Write([]byte(`{"data":{"resolveUrn":null}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"` + id + `","kind":"node","memoryId":"mem1"}}}`))
		case "GetSpecNodeForEdit":
			_, _ = w.Write([]byte(`{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","description":null,"tags":` + string(tags) +
				`,"role":"spec.rule","content":"# msg:010:02 — W2\n\nBody {{name}}.\n","abstract":` + abstract + `,"abstractOriginHash":null,"revision":7}}}`))
		case "AuthContext":
			_, _ = w.Write([]byte(`{"data":{"authContext":null}}`))
		case "CreateSpecNode":
			_, _ = w.Write([]byte(`{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:09","name":"msg:010:09 — V2","nodeType":"info","tags":["spec"],"updatedAt":"2026-11-26T00:00:00Z"}}}`))
		case "RetireSpec":
			if s.status != 0 {
				w.WriteHeader(s.status)
			}
			reply := s.reply
			if reply == "" {
				in, _ := body.Variables["input"].(map[string]any)
				form, _ := in["form"].(string)
				reply = success1736(form)
			}
			_, _ = w.Write([]byte(reply))
		case "GetSpecRetireState":
			if s.readFails {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("lost answer"))
				return
			}
			spec := s.state
			if spec == "" {
				spec = spec1736("sp1", door1736Old, "mem1", 7, string(tags), abstract, "")
			}
			successor := s.successor
			if successor == "" {
				successor = spec1736("sp3", door1736By, "mem1", 1, `["spec"]`, "null", "")
			}
			_, _ = w.Write([]byte(`{"data":{"spec":` + spec + `,"successor":` + successor + `}}`))
		default:
			s.t.Errorf("unexpected operation %q: a retirement is one retireSpec, never a node update or an edge create", op)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	s.t.Cleanup(srv.Close)
	return srv
}

func (s *door1736) count(op string) int { return len(s.vars[op]) }

var (
	verbRescind1736   = []string{"spec", "rescind", door1736Old, "--reason", "why", "--summary", "old"}
	verbWithdrawn1736 = []string{"spec", "rescind", door1736Old, "--reason", "why", "--summary", "old", "--withdrawn"}
	verbBy1736        = []string{"spec", "supersede", door1736Old, "--by", door1736By}
	verbCreate1736    = []string{"spec", "supersede", door1736Old, "--to", door1736New, "--title", "V2"}
)

func allVerbs1736() map[string][]string {
	return map[string][]string{"rescind": verbRescind1736, "by": verbBy1736, "create": verbCreate1736}
}

func run1736(t *testing.T, s *door1736, args []string, extra ...string) (string, error) {
	t.Helper()
	s.t = t
	srv := s.serve()
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("")
	root := NewRootCmd(f)
	all := append(append([]string{}, args...), "-m", specMem, "--yes", "--server", srv.URL)
	root.SetArgs(append(all, extra...))
	err := root.Execute()
	return out.String(), err
}

func json1736(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return m
}

func text1736(out string, err error) string {
	if err != nil {
		return out + "\n" + err.Error()
	}
	return out
}

// No output may claim what this run did or did not do when it cannot know, nor
// state a link or retirement as history rather than as current state.
func assertNoHistoryClaims1736(t *testing.T, text string) {
	t.Helper()
	low := strings.ToLower(text)
	for _, banned := range []string{"committed", "landed", "finish retiring", "hadron spec link", "spec edit", "already rescinded (nothing"} {
		if strings.Contains(low, banned) {
			t.Errorf("output must not say %q:\n%s", banned, text)
		}
	}
	for _, claim := range []string{"not retired", "not linked"} {
		for rest := low; ; {
			i := strings.Index(rest, claim)
			if i < 0 {
				break
			}
			before := rest[:i]
			if j := strings.LastIndexAny(before, ".;:\n("); j >= 0 {
				before = before[j+1:]
			}
			if !strings.Contains(before, "currently") {
				t.Errorf("%q must be labelled current:\n%s", claim, text)
			}
			rest = rest[i+len(claim):]
		}
	}
}

func TestSpec1736EachVerbSendsOneRetireSpec(t *testing.T) {
	cases := map[string]struct {
		args []string
		want map[string]any
	}{
		"rescind":   {verbRescind1736, map[string]any{"ref": "sp1", "form": "RESCIND", "withdrawn": false, "expectedRevision": 7.0, "reason": "why", "summary": "old"}},
		"withdrawn": {verbWithdrawn1736, map[string]any{"ref": "sp1", "form": "RESCIND", "withdrawn": true, "expectedRevision": 7.0, "reason": "why", "summary": "old"}},
		"by":        {verbBy1736, map[string]any{"ref": "sp1", "form": "SUPERSEDE", "successorRef": "sp3", "expectedRevision": 7.0}},
		"by with wording": {append(append([]string{}, verbBy1736...), "--reason", "ruling 1", "--summary", "short"),
			map[string]any{"ref": "sp1", "form": "SUPERSEDE", "successorRef": "sp3", "expectedRevision": 7.0, "reason": "ruling 1", "summary": "short"}},
		"create": {verbCreate1736, map[string]any{"ref": "sp1", "form": "SUPERSEDE", "successorRef": "new1", "expectedRevision": 7.0}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := &door1736{}
			out, err := run1736(t, s, c.args, "--json")
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if s.count("RetireSpec") != 1 {
				t.Fatalf("retireSpec calls = %d, want 1 (%v)", s.count("RetireSpec"), s.calls)
			}
			got, _ := json.Marshal(s.vars["RetireSpec"][0]["input"])
			want, _ := json.Marshal(c.want)
			if string(got) != string(want) {
				t.Errorf("input = %s, want %s", got, want)
			}
			wantCreates := 0
			if name == "create" {
				wantCreates = 1
			}
			if s.count("CreateSpecNode") != wantCreates {
				t.Errorf("creates = %d, want %d", s.count("CreateSpecNode"), wantCreates)
			}
		})
	}
}

// Success is rendered from RetireSpecResult and nothing is read after it.
func TestSpec1736SuccessComesFromTheResultAlone(t *testing.T) {
	for name, args := range allVerbs1736() {
		t.Run(name, func(t *testing.T) {
			s := &door1736{}
			out, err := run1736(t, s, args, "--json")
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if last := s.calls[len(s.calls)-1]; last != "RetireSpec" {
				t.Errorf("%q was sent after the success: %v", last, s.calls)
			}
			m := json1736(t, out)
			if m["retired"] != true || m["outcome"] != "retired" || m["current"] != nil {
				t.Errorf("retired/outcome/current = %v/%v/%v", m["retired"], m["outcome"], m["current"])
			}
			a, _ := m["abstract"].(string)
			if !strings.Contains(a, "(2026-11-26)") {
				t.Errorf("abstract = %q, want the server's", a)
			}
			if tags, ok := m["tags"].([]any); !ok || len(tags) == 0 {
				t.Errorf("tags = %v", m["tags"])
			}
			if name != "rescind" {
				edges := m["edges"].([]any)
				last := edges[len(edges)-1].(map[string]any)
				if last["label"] != "superseded-by" || last["status"] != "created" {
					t.Errorf("edge = %v", last)
				}
			}
		})
	}
}

// A retired spec is no longer stopped on the pre-read: the door is asked, and
// its refusal is exit 5, never a success.
func TestSpec1736AlreadyRetiredAsksTheDoorAndConflicts(t *testing.T) {
	for name, args := range allVerbs1736() {
		for lead, abstract := range map[string]string{
			"rescinded":  "RESCINDED (2026-01-01); do not apply. x Historical: y",
			"withdrawn":  "WITHDRAWN (2026-01-01); do not apply. x Historical: y",
			"superseded": "SUPERSEDED (2026-01-01) by msg:010:03 (W3); do not apply. Historical: y",
		} {
			t.Run(name+"/"+lead, func(t *testing.T) {
				s := &door1736{tags: []string{"spec", "superseded"}, abstract: abstract, reply: refusal1736("NODE_WRITE_CONFLICT")}
				out, err := run1736(t, s, args, "--json")
				if got := exitCodeFor(err); got != exitcode.Conflict {
					t.Fatalf("exit = %d (%v), want %d", got, err, exitcode.Conflict)
				}
				if s.count("RetireSpec") != 1 || s.count("GetSpecRetireState") != 1 {
					t.Fatalf("calls = %v, want one retireSpec and one re-read", s.calls)
				}
				m := json1736(t, out)
				if m["outcome"] != "refused" || m["retired"] != true {
					t.Errorf("outcome/retired = %v/%v", m["outcome"], m["retired"])
				}
				all := text1736(out, err)
				for _, want := range []string{"currently already retired", "nothing written"} {
					if !strings.Contains(all, want) {
						t.Errorf("missing %q:\n%s", want, all)
					}
				}
				if m["unchanged"] != nil {
					t.Errorf("no unchanged success: %v", m)
				}
				assertNoHistoryClaims1736(t, all)
			})
		}
	}
}

func TestSpec1736ConflictCauses(t *testing.T) {
	cases := map[string]struct {
		state, successor string
		verbs            []string
		want             []string
	}{
		"successor link": {state: spec1736("sp1", door1736Old, "mem1", 7, `["spec"]`, "null", edge1736("x1", "msg:010:05", "mem1")),
			verbs: []string{"rescind", "by", "create"},
			want:  []string{"currently linked to msg:010:05, not retired", "needs manual review; the CLI makes no automatic repair", "nothing written"}},
		"revision moved": {state: spec1736("sp1", door1736Old, "mem1", 9, `["spec"]`, "null", ""),
			verbs: []string{"rescind", "by", "create"},
			want:  []string{"changed since read", "re-run", "nothing written", "currently not retired"}},
		"unreadable edge": {state: spec1736("sp1", door1736Old, "mem1", 7, `["spec"]`, "null", unreadableEdge1736),
			verbs: []string{"rescind", "by", "create"},
			want:  []string{"refused, nothing written; the cause isn't visible to this read"}},
		"nothing visible": {verbs: []string{"rescind"},
			want: []string{"refused, nothing written; the cause isn't visible to this read", "currently not retired"}},
		"unreadable spec": {state: "null", verbs: []string{"rescind", "by"},
			want: []string{"refused, nothing written; the cause isn't visible to this read"}},
		"retired successor": {successor: spec1736("sp3", door1736By, "mem1", 1, `["spec","superseded"]`, "null", ""),
			verbs: []string{"by"}, want: []string{"the successor can't be used", "nothing written"}},
		"successor in another memory": {successor: spec1736("sp3", door1736By, "mem2", 1, `["spec"]`, "null", ""),
			verbs: []string{"by"}, want: []string{"the successor can't be used", "nothing written"}},
		"successor with a successor": {successor: spec1736("sp3", door1736By, "mem1", 1, `["spec"]`, "null", edge1736("x1", "msg:010:05", "mem1")),
			verbs: []string{"by"}, want: []string{"the successor can't be used", "nothing written"}},
		"successor is the spec": {successor: spec1736("sp1", door1736Old, "mem1", 7, `["spec"]`, "null", ""),
			verbs: []string{"by"}, want: []string{"the successor can't be used", "nothing written"}},
		"unreadable successor": {successor: "null", verbs: []string{"by"},
			want: []string{"refused, nothing written; the cause isn't visible to this read"}},
	}
	for name, c := range cases {
		for _, verb := range c.verbs {
			t.Run(name+"/"+verb, func(t *testing.T) {
				s := &door1736{reply: refusal1736("NODE_WRITE_CONFLICT"), state: c.state, successor: c.successor}
				out, err := run1736(t, s, allVerbs1736()[verb])
				if got := exitCodeFor(err); got != exitcode.Conflict {
					t.Fatalf("exit = %d (%v)", got, err)
				}
				if s.count("RetireSpec") != 1 || s.count("GetSpecRetireState") != 1 {
					t.Fatalf("calls = %v", s.calls)
				}
				all := text1736(out, err)
				for _, want := range c.want {
					if !strings.Contains(all, want) {
						t.Errorf("missing %q:\n%s", want, all)
					}
				}
				if strings.Contains(c.state, "unreadable") && strings.Contains(all, "linked to") {
					t.Errorf("no inference of a hidden edge:\n%s", all)
				}
				assertNoHistoryClaims1736(t, all)
			})
		}
	}
}

// Every state the old create-mode pre-read stopped on now creates the successor,
// asks the door once and reports the created node. The legacy linked spec has no
// finish path.
func TestSpec1736CreateModeNoPreReadStopsAndOrphanTruth(t *testing.T) {
	s := &door1736{tags: []string{"spec", "superseded", "rescinded"}, abstract: "RESCINDED (2026-01-01); do not apply.",
		reply: refusal1736("NODE_WRITE_CONFLICT")}
	out, err := run1736(t, s, verbCreate1736, "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if s.count("CreateSpecNode") != 1 || s.count("RetireSpec") != 1 {
		t.Fatalf("calls = %v", s.calls)
	}
	m := json1736(t, out)
	created, _ := m["created"].(map[string]any)
	if created["id"] != "new1" || created["loc"] != door1736New {
		t.Errorf("created = %v", m["created"])
	}
	current, _ := m["current"].(map[string]any)
	if current == nil || !strings.HasPrefix(current["statement"].(string), "currently") {
		t.Errorf("current = %v", m["current"])
	}
	all := text1736(out, err)
	for _, want := range []string{"new1", "this retirement request wrote nothing"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q:\n%s", want, all)
		}
	}
	assertNoHistoryClaims1736(t, all)

	human := &door1736{reply: refusal1736("NODE_WRITE_CONFLICT")}
	hout, herr := run1736(t, human, verbCreate1736)
	all = text1736(hout, herr)
	for _, want := range []string{"created successor: " + door1736New + " (id new1)", "currently not linked", "currently not retired"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q:\n%s", want, all)
		}
	}
	assertNoHistoryClaims1736(t, all)
}

// No answer, or an answer that cannot be read as a result: one re-read, current
// state only, no resend.
func TestSpec1736UncertainAnswer(t *testing.T) {
	retired := spec1736("sp1", door1736Old, "mem1", 8, `["spec","superseded"]`, `"RESCINDED (2026-11-26); do not apply."`, "")
	replies := map[string]struct {
		status int
		reply  string
		exit   int
	}{
		"lost":       {http.StatusBadGateway, "lost answer", exitcode.Unavailable},
		"null data":  {0, `{"data":null}`, exitcode.Error},
		"null node":  {0, `{"data":{"retireSpec":{"node":null,"successorEdge":null}}}`, exitcode.Error},
		"wrong node": {0, `{"data":{"retireSpec":{"node":{"id":"zz","loc":"a:b","revision":8,"tags":["superseded"],"abstract":null},"successorEdge":null}}}`, exitcode.Error},
		"partial":    {0, `{"data":null,"errors":[{"message":"boom","path":["retireSpec","node","abstract"],"extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Error},
		"internal":   {0, refusal1736("INTERNAL_SERVER_ERROR"), exitcode.Error},
	}
	states := map[string]struct {
		state     string
		readFails bool
		retired   any
		want      string
	}{
		"retired":     {state: retired, retired: true, want: "currently retired"},
		"not retired": {retired: false, want: "currently not retired"},
		"unknown":     {readFails: true, retired: nil, want: "current state is unknown"},
	}
	for rname, r := range replies {
		for sname, st := range states {
			for verb, args := range allVerbs1736() {
				for _, asJSON := range []bool{true, false} {
					t.Run(rname+"/"+sname+"/"+verb, func(t *testing.T) {
						s := &door1736{status: r.status, reply: r.reply, state: st.state, readFails: st.readFails}
						var extra []string
						if asJSON {
							extra = []string{"--json"}
						}
						out, err := run1736(t, s, args, extra...)
						if got := exitCodeFor(err); got != r.exit {
							t.Fatalf("exit = %d (%v), want %d", got, err, r.exit)
						}
						if s.count("RetireSpec") != 1 || s.count("GetSpecRetireState") != 1 {
							t.Fatalf("calls = %v", s.calls)
						}
						all := text1736(out, err)
						for _, want := range []string{st.want, "outcome is unknown"} {
							if !strings.Contains(all, want) {
								t.Errorf("missing %q:\n%s", want, all)
							}
						}
						if strings.Contains(all, "wrote nothing") || strings.Contains(all, "nothing written") || strings.Contains(all, "nothing was written") {
							t.Errorf("an unknown outcome claims nothing about writes:\n%s", all)
						}
						if asJSON {
							m := json1736(t, out)
							if v, ok := m["retired"]; !ok || v != st.retired || m["outcome"] != "unknown" {
								t.Errorf("retired/outcome = %v/%v, want %v/unknown", m["retired"], m["outcome"], st.retired)
							}
						}
						assertNoHistoryClaims1736(t, all)
					})
				}
			}
		}
	}
}

func TestSpec1736DryRunPrintsInputsOnly(t *testing.T) {
	for name, args := range allVerbs1736() {
		for _, asJSON := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				s := &door1736{}
				extra := []string{"--dry-run", "--reason", "ruling 7", "--summary", "short words"}
				if asJSON {
					extra = append(extra, "--json")
				}
				out, err := run1736(t, s, args, extra...)
				if err != nil {
					t.Fatalf("execute: %v\n%s", err, out)
				}
				if s.count("RetireSpec")+s.count("CreateSpecNode") != 0 {
					t.Errorf("a dry run wrote: %v", s.calls)
				}
				for _, banned := range []string{"RESCINDED (", "SUPERSEDED (", "WITHDRAWN (", "do not apply", "⚠️"} {
					if strings.Contains(out, banned) {
						t.Errorf("dry run shows server text %q:\n%s", banned, out)
					}
				}
				for _, want := range []string{"ruling 7", "short words", "7", "the server composes"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q:\n%s", want, out)
					}
				}
				if asJSON {
					m := json1736(t, out)
					if m["abstract"] != nil || m["outcome"] != "planned" || m["revision"] != 7.0 || m["reason"] != "ruling 7" || m["summary"] != "short words" {
						t.Errorf("dto = %v", m)
					}
					wantForm := "SUPERSEDE"
					if name == "rescind" {
						wantForm = "RESCIND"
					}
					if m["form"] != wantForm {
						t.Errorf("form = %v", m["form"])
					}
				}
			})
		}
	}
}

// --by sends whatever resolves: no type, placeholder, retirement or cycle rule
// of the CLI's own, and no read of the successor before the door.
func TestSpec1736BySendsWithoutClientRules(t *testing.T) {
	s := &door1736{}
	if out, err := run1736(t, s, verbBy1736); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, op := range s.calls {
		if op != "ResolveUrn" && op != "GetSpecNodeForEdit" && op != "RetireSpec" {
			t.Errorf("unexpected pre-read %q", op)
		}
	}
	self := &door1736{reply: refusal1736("NODE_WRITE_CONFLICT"), successor: spec1736("sp1", door1736Old, "mem1", 7, `["spec"]`, "null", "")}
	_, err := run1736(t, self, []string{"spec", "supersede", door1736Old, "--by", door1736Old})
	if exitCodeFor(err) != exitcode.Conflict || self.count("RetireSpec") != 1 {
		t.Errorf("self supersede is the server's refusal: %v %v", err, self.calls)
	}
}

func TestSpec1736UnsupportedProviderHasNoFallback(t *testing.T) {
	reply := `{"errors":[{"message":"Unknown type \"RetireSpecInput\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},` +
		`{"message":"Cannot query field \"retireSpec\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	for name, args := range allVerbs1736() {
		for _, status := range []int{0, http.StatusBadRequest} {
			t.Run(name, func(t *testing.T) {
				s := &door1736{reply: reply, status: status}
				out, err := run1736(t, s, args)
				if got := exitCodeFor(err); got != exitcode.Usage {
					t.Fatalf("exit = %d (%v)", got, err)
				}
				all := text1736(out, err)
				if !strings.Contains(all, "does not support retireSpec") || !strings.Contains(all, "no fallback") {
					t.Errorf("message:\n%s", all)
				}
				if s.count("RetireSpec") != 1 || s.count("GetSpecRetireState") != 0 {
					t.Errorf("calls = %v", s.calls)
				}
				if name == "create" && !strings.Contains(all, "new1") {
					t.Errorf("the created successor must be named:\n%s", all)
				}
			})
		}
	}
}

func TestSpec1736ServerRefusalsPassThrough(t *testing.T) {
	for code, exit := range map[string]int{"NODE_NOT_FOUND": exitcode.NotFound, "FORBIDDEN": exitcode.Forbidden, "BAD_USER_INPUT": exitcode.Usage} {
		for name, args := range allVerbs1736() {
			t.Run(code+"/"+name, func(t *testing.T) {
				s := &door1736{reply: refusal1736(code)}
				out, err := run1736(t, s, args)
				if got := exitCodeFor(err); got != exit {
					t.Fatalf("exit = %d (%v), want %d", got, err, exit)
				}
				if s.count("RetireSpec") != 1 || s.count("GetSpecRetireState") != 0 {
					t.Errorf("calls = %v", s.calls)
				}
				if name == "create" && !strings.Contains(text1736(out, err), "new1") {
					t.Errorf("the created successor must be named:\n%s", text1736(out, err))
				}
			})
		}
	}
}

func TestSpec1736AbstractTooLong(t *testing.T) {
	reply := `{"data":null,"errors":[{"message":"Abstract exceeds 2000-character cap","path":["retireSpec"],"extensions":{"code":"NodeAbstractTooLongError","length":2104,"max":2000}}]}`
	for name, args := range allVerbs1736() {
		t.Run(name, func(t *testing.T) {
			s := &door1736{reply: reply, abstract: strings.Repeat("x", 3000)}
			out, err := run1736(t, s, args)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d (%v)", got, err)
			}
			all := text1736(out, err)
			for _, want := range []string{"2104", "2000", "--summary", "--reason"} {
				if !strings.Contains(all, want) {
					t.Errorf("missing %q:\n%s", want, all)
				}
			}
			if s.count("RetireSpec") != 1 {
				t.Errorf("calls = %v", s.calls)
			}
		})
	}
}

func TestSpec1736AuthoredWithAndGovernedRefusal(t *testing.T) {
	claim := []string{"--authored-with", "task1", "--authored-revision", "4"}
	for _, verb := range []string{"by", "create"} {
		s := &door1736{}
		if out, err := run1736(t, s, allVerbs1736()[verb], claim...); err != nil {
			t.Fatalf("%s: %v\n%s", verb, err, out)
		}
		in := s.vars["RetireSpec"][0]["input"].(map[string]any)
		door, _ := json.Marshal(in["authoredWith"])
		if !strings.Contains(string(door), "task1") {
			t.Errorf("%s: retireSpec authoredWith = %s", verb, door)
		}
		if verb == "create" {
			made, _ := json.Marshal(s.vars["CreateSpecNode"][0]["input"].(map[string]any)["authoredWith"])
			if string(made) != string(door) {
				t.Errorf("create %s and retire %s must carry the same claim", made, door)
			}
		}
	}
	refusal := `{"data":null,"errors":[{"message":"attestation","path":["retireSpec"],"extensions":{"code":"AUTHORING_ATTESTATION_MISSING","taskAccess":"READABLE","task":"hrn:node:a.com:b:t","taskId":"task1","taskRevision":4,"load":{"cli":"hadron node get t"}}}]}`
	for name, args := range allVerbs1736() {
		s := &door1736{reply: refusal}
		out, err := run1736(t, s, args)
		if err == nil || api.AuthoringAttestationDetails(err) == nil {
			t.Errorf("%s: the attestation fields must survive: %v\n%s", name, err, out)
		}
	}
}

func TestSpec1736BlankWordingRefusedBeforeAnyRequest(t *testing.T) {
	for _, flag := range []string{"--reason", "--summary"} {
		for _, verb := range []string{"by", "create"} {
			s := &door1736{}
			_, err := run1736(t, s, allVerbs1736()[verb], flag, "  ")
			if exitCodeFor(err) != exitcode.Usage || len(s.calls) != 0 {
				t.Errorf("%s %s: %v %v", verb, flag, err, s.calls)
			}
		}
	}
}

// The local composers and the client-side successor rules are gone, not just
// unreachable.
func TestSpec1736LocalCompositionDeleted(t *testing.T) {
	entries, err := os.ReadDir("spec")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			f, err := os.Open("spec/" + e.Name())
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(&src, f)
			_ = f.Close()
		}
	}
	for _, gone := range []string{"rescindPlan", "supersededAbstract", "retireSupersededSpec", "eligibleSupersedeTarget", "checkSupersedeCycle",
		"withBanner", "supersedeLeadState", "sourceTombstoneError", "Finish retiring", "is already retired but has no readable successor", "> Superseded by"} {
		if strings.Contains(src.String(), gone) {
			t.Errorf("%q is still in internal/cmd/spec", gone)
		}
	}
}

func TestSpec1736Help(t *testing.T) {
	for verb, wants := range map[string][]string{
		"rescind":   {"retireSpec", "server composes", "exit 5", "exit 0", "manual review", "currently"},
		"supersede": {"retireSpec", "server composes", "exit 5", "manual review", "resume", "currently"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(f.IOStreams.Out)
		root.SetArgs([]string{"spec", verb, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range wants {
			if !strings.Contains(help, want) {
				t.Errorf("%s help missing %q", verb, want)
			}
		}
	}
}
