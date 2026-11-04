package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#809 (split A, Vera 2026-10-31): `spec rescind` retires a spec WITHOUT a
// successor. One guarded updateNode (immutable node id + expectedRevision) sets
// the `superseded` tag, the banner (body) and the abstract together; there is no
// edge, no allocation and no mint. Synthetic transport only.

const rescindBody = "# msg:010:02 — W2\n\nThe rule text, with {{name}}.\n"

// rescindServer answers each operation by name. `update` chooses the write's
// answer ("" = success); `after` is the node the post-failure re-read sees.
type rescindServer struct {
	t              *testing.T
	tags           string // JSON array of the node's tags
	abstr          string // JSON value of the abstract
	edges          string // outgoing edges (JSON, no brackets)
	revDiff        bool   // the raw read reports a different node id than GetNode
	update         func(call int) (int, string)
	reread         string // tags JSON for the re-read after a failed write, "" = same as tags
	rereadAbstract string // abstract JSON for that re-read, "" = same as abstr
	calls          map[string]int
	vars           map[string][]json.RawMessage
}

func (s *rescindServer) serve() *httptest.Server {
	s.calls, s.vars = map[string]int{}, map[string][]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		s.calls[body.OperationName]++
		s.vars[body.OperationName] = append(s.vars[body.OperationName], body.Variables)
		w.Header().Set("Content-Type", "application/json")
		content, _ := json.Marshal(rescindBody)
		id := "sp1"
		if s.revDiff {
			id = "sp2"
		}
		switch body.OperationName {
		case "ResolveUrn":
			_, _ = w.Write([]byte(resolveSpecJSON))
		case "GetNode":
			_, _ = w.Write([]byte(`{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","description":null,"abstract":` + s.abstr +
				`,"abstractOriginHash":null,"nodeType":"info","tags":` + s.tags + `,"role":null,"content":` + string(content) + `,"data":null,"seq":null,` +
				`"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z","outgoingEdges":[` + s.edges + `],"incomingEdges":[]}}}`))
		case "GetSpecNodeForEdit":
			tags, abstr := s.tags, s.abstr
			if s.calls["UpdateSpecNode"] > 0 && s.reread != "" {
				tags = s.reread
			}
			if s.calls["UpdateSpecNode"] > 0 && s.rereadAbstract != "" {
				abstr = s.rereadAbstract
			}
			_, _ = w.Write([]byte(`{"data":{"node":{"id":"` + id + `","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","description":null,"tags":` + tags +
				`,"role":null,"content":` + string(content) + `,"abstract":` + abstr + `,"abstractOriginHash":null,"revision":7}}}`))
		case "UpdateSpecNode":
			status, answer := http.StatusOK, guardWriteOK
			if s.update != nil {
				status, answer = s.update(s.calls["UpdateSpecNode"])
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(answer))
		default:
			s.t.Errorf("unexpected operation %q (rescind makes one guarded write and no edge or create)", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	s.t.Cleanup(srv.Close)
	return srv
}

func runRescind(t *testing.T, s *rescindServer, stdin string, args ...string) (string, *rescindServer, error) {
	t.Helper()
	if s.tags == "" {
		s.tags = `["spec"]`
	}
	if s.abstr == "" {
		s.abstr = "null"
	}
	s.t = t
	srv := s.serve()
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "rescind", "msg:010:02", "-m", specMem, "--server", srv.URL}, args...))
	err := root.Execute()
	return out.String(), s, err
}

type rescindWrite struct {
	Input struct {
		ID               *string  `json:"id"`
		MemoryID         *string  `json:"memoryId"`
		Loc              *string  `json:"loc"`
		ExpectedRevision *int     `json:"expectedRevision"`
		Tags             []string `json:"tags"`
		Content          *string  `json:"content"`
		Abstract         *string  `json:"abstract"`
		Edges            any      `json:"edges"`
	} `json:"input"`
}

func sentWrite(t *testing.T, s *rescindServer) rescindWrite {
	t.Helper()
	if len(s.vars["UpdateSpecNode"]) != 1 {
		t.Fatalf("want exactly one write, got %d", len(s.vars["UpdateSpecNode"]))
	}
	var w rescindWrite
	if err := json.Unmarshal(s.vars["UpdateSpecNode"][0], &w); err != nil {
		t.Fatal(err)
	}
	return w
}

var (
	rescindArgs    = []string{"--reason", "ruling 0099 A", "--summary", "A memory was once drafted at creation.", "--yes"}
	rescindDateRe  = regexp.MustCompile(`RESCINDED \(\d{4}-\d{2}-\d{2}\); do not apply\. ruling 0099 A Historical: A memory was once drafted at creation\.`)
	rescindBannerR = regexp.MustCompile(`> \*\*⚠️ Rescinded\*\* \(\d{4}-\d{2}-\d{2}, ruling 0099 A\), no successor\.`)
)

// One guarded write carries the tag, the banner and the abstract together,
// and nothing else: no edge, no create, no mint.
func TestSpecRescindWritesTagBannerAndAbstractInOneGuardedUpdate(t *testing.T) {
	out, s, err := runRescind(t, &rescindServer{}, "", rescindArgs...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	w := sentWrite(t, s)
	if w.Input.ID == nil || *w.Input.ID != "sp1" || w.Input.MemoryID != nil || w.Input.Loc != nil {
		t.Errorf("the write must select the read node by immutable id alone, got id=%v memoryId=%v loc=%v", w.Input.ID, w.Input.MemoryID, w.Input.Loc)
	}
	if w.Input.ExpectedRevision == nil || *w.Input.ExpectedRevision != 7 {
		t.Errorf("expectedRevision = %v, want 7 (the revision read with the body)", w.Input.ExpectedRevision)
	}
	if !contains(w.Input.Tags, "spec") || !contains(w.Input.Tags, "superseded") {
		t.Errorf("tags = %v, want spec + superseded", w.Input.Tags)
	}
	if w.Input.Content == nil || !rescindBannerR.MatchString(*w.Input.Content) || !strings.Contains(*w.Input.Content, "{{name}}") {
		t.Errorf("content = %v, want the Rescinded banner kept above the STORED body, placeholders intact", w.Input.Content)
	}
	if w.Input.Abstract == nil || !rescindDateRe.MatchString(*w.Input.Abstract) {
		t.Errorf("abstract = %v, want %q-shaped RESCINDED prefix + reason + Historical summary", w.Input.Abstract, "RESCINDED (<date>); do not apply. <reason> Historical: <summary>")
	}
	if w.Input.Edges != nil {
		t.Errorf("no forward edge may be written, got %v", w.Input.Edges)
	}
}

func TestSpecRescindWithdrawnUsesTheNeverInForceWording(t *testing.T) {
	out, s, err := runRescind(t, &rescindServer{}, "", append([]string{"--withdrawn"}, rescindArgs...)...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	w := sentWrite(t, s)
	if w.Input.Content == nil || !strings.Contains(*w.Input.Content, "Withdrawn, never in force") || strings.Contains(*w.Input.Content, "no successor") {
		t.Errorf("content = %v, want the Withdrawn banner", w.Input.Content)
	}
	if w.Input.Abstract == nil || !strings.HasPrefix(*w.Input.Abstract, "WITHDRAWN (") {
		t.Errorf("abstract = %v, want a WITHDRAWN prefix", w.Input.Abstract)
	}
}

// --reason and a non-empty --summary are required; both refuse before ANY request.
func TestSpecRescindRefusesMissingReasonOrEmptySummaryBeforeAnyRequest(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no reason":     {[]string{"--summary", "x", "--yes"}, "--reason"},
		"blank reason":  {[]string{"--reason", "  ", "--summary", "x", "--yes"}, "--reason"},
		"no summary":    {[]string{"--reason", "r", "--yes"}, "--summary"},
		"blank summary": {[]string{"--reason", "r", "--summary", "   ", "--yes"}, "--summary"},
	} {
		t.Run(name, func(t *testing.T) {
			_, s, err := runRescind(t, &rescindServer{}, "", c.args...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (%v)", got, err)
			}
			// Not cobra's unknown-command refusal, which is also exit 2: the verb's own message.
			if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "unknown command") {
				t.Errorf("want the verb's own refusal naming %s, got %v", c.want, err)
			}
			if len(s.calls) != 0 {
				t.Errorf("no request may be sent, got %v", s.calls)
			}
		})
	}
}

func TestSpecRescindReadsTheSummaryFromStdin(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{}, "Text from stdin.\n", "--reason", "r", "--summary", "-", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if w := sentWrite(t, s); w.Input.Abstract == nil || !strings.Contains(*w.Input.Abstract, "Historical: Text from stdin.") {
		t.Errorf("abstract = %v, want the stdin summary", w.Input.Abstract)
	}
}

// Refused with NO write: a non-spec node, an abstract past the 2000-character cap,
// a spec already retired in favour of a successor, a tagged-but-not-rescinded spec.
func TestSpecRescindRefusals(t *testing.T) {
	succ := `{"id":"e1","name":"superseded-by","loc":"l","description":null,"isRunnable":false,"priority":0,"condition":null,"target":{"id":"t1","loc":"msg:010:03","memoryId":"mem1"}}`
	cases := []struct {
		name string
		s    *rescindServer
		args []string
		code int
		want string
	}{
		{"not a spec", &rescindServer{tags: `["info"]`}, rescindArgs, exitcode.Usage, "not a spec"},
		{"has a successor", &rescindServer{tags: `["spec","superseded"]`, edges: succ}, rescindArgs, exitcode.Usage, "msg:010:03"},
		{"superseded tag without a rescind prefix", &rescindServer{tags: `["spec","superseded"]`, abstr: `"An ordinary abstract."`}, rescindArgs, exitcode.Conflict, "superseded"},
		{"abstract too long", &rescindServer{}, []string{"--reason", "r", "--summary", strings.Repeat("x", 2100), "--yes"}, exitcode.Usage, "2000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, s, err := runRescind(t, c.s, "", c.args...)
			if got := exitCodeFor(err); got != c.code {
				t.Fatalf("exit = %d, want %d (%v)", got, c.code, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
			if s.calls["UpdateSpecNode"] != 0 {
				t.Error("a refused rescind must not write")
			}
		})
	}
	// A successor refusal points at the verb that owns that case.
	_, _, err := runRescind(t, &rescindServer{tags: `["spec","superseded"]`, edges: succ}, "", rescindArgs...)
	if err == nil || !strings.Contains(err.Error(), "supersede") {
		t.Errorf("a spec with a successor must point at `spec supersede`, got %v", err)
	}
}

// A rerun on an already-rescinded spec writes nothing and says so.
func TestSpecRescindIsANoOpOnAnAlreadyRescindedSpec(t *testing.T) {
	s := &rescindServer{tags: `["spec","superseded"]`, abstr: `"RESCINDED (2026-10-03); do not apply. An older reason. Historical: before."`}
	out, s, err := runRescind(t, s, "", append(rescindArgs, "--json")...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if s.calls["UpdateSpecNode"] != 0 {
		t.Error("an already-rescinded spec must not be written again")
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	if dto["alreadyRescinded"] != true || dto["retired"] != true {
		t.Errorf("dto = %v, want alreadyRescinded and retired true", dto)
	}
	text, _, _ := runRescind(t, &rescindServer{tags: `["spec","superseded"]`, abstr: `"WITHDRAWN (2026-10-03); do not apply. r Historical: h."`}, "", rescindArgs...)
	if !strings.Contains(text, "already rescinded") {
		t.Errorf("text output should say already rescinded, got %q", text)
	}
}

// --dry-run reads only, shows what it would guard on, and never writes.
func TestSpecRescindDryRunWritesNothingAndNamesTheGuard(t *testing.T) {
	out, s, err := runRescind(t, &rescindServer{}, "", append([]string{"--dry-run", "--json"}, rescindArgs...)...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if s.calls["UpdateSpecNode"] != 0 {
		t.Error("--dry-run must not write")
	}
	var dto struct {
		Loc      string `json:"loc"`
		NodeID   string `json:"nodeId"`
		Revision int    `json:"revision"`
		Wording  string `json:"wording"`
		DryRun   bool   `json:"dryRun"`
		Retired  *bool  `json:"retired"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	if dto.Loc != "msg:010:02" || dto.NodeID != "sp1" || dto.Revision != 7 || !dto.DryRun || dto.Retired == nil || *dto.Retired {
		t.Errorf("dry-run dto = %+v, want loc, node id sp1, revision 7, dryRun, retired false", dto)
	}
	if !strings.Contains(dto.Wording, "Rescinded") {
		t.Errorf("wording = %q, want the planned banner wording", dto.Wording)
	}
}

// Without --yes a non-interactive run refuses before writing.
func TestSpecRescindNeedsYesWhenNonInteractive(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{}, "", "--reason", "r", "--summary", "s")
	if got := exitCodeFor(err); got != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit = %d, want 2 naming --yes (%v)", got, err)
	}
	if s.calls["UpdateSpecNode"] != 0 {
		t.Error("an unconfirmed rescind must not write")
	}
}

// A stale revision is NODE_WRITE_CONFLICT: exit 5, one attempt, nothing else written,
// and the message says a rerun starts from a fresh read.
func TestSpecRescindStaleRevisionIsAConflictWithNoOtherWrite(t *testing.T) {
	s := &rescindServer{update: func(int) (int, string) {
		return http.StatusOK, `{"errors":[{"message":"stale","extensions":{"code":"NODE_WRITE_CONFLICT"}}]}`
	}}
	_, s, err := runRescind(t, s, "", rescindArgs...)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (%v)", got, err)
	}
	if s.calls["UpdateSpecNode"] != 1 || s.calls["CreateEdge"] != 0 {
		t.Errorf("want exactly one guarded attempt and nothing else, got %v", s.calls)
	}
	if !strings.Contains(err.Error(), "nothing was written") && !strings.Contains(err.Error(), "not applied") {
		t.Errorf("the conflict should say nothing was written, got %v", err)
	}
}

// A write with no answer is checked by re-reading: the tag AND the RESCINDED prefix there
// mean rescinded; anything less is unknown, and the output names the check.
func TestSpecRescindLostResponseIsResolvedByReReading(t *testing.T) {
	lost := func(int) (int, string) { return http.StatusBadGateway, "upstream went away" }
	proven := &rescindServer{update: lost, reread: `["spec","superseded"]`,
		rereadAbstract: `"RESCINDED (2026-11-04); do not apply. ruling 0099 A Historical: A memory was once drafted at creation."`}
	out, s, err := runRescind(t, proven, "", append([]string{"--json"}, rescindArgs...)...)
	if err != nil {
		t.Fatalf("a re-read that shows the tag and the prefix proves the write: %v\n%s", err, out)
	}
	if s.calls["UpdateSpecNode"] != 1 {
		t.Errorf("want one write attempt, got %d", s.calls["UpdateSpecNode"])
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || dto["retired"] != true {
		t.Errorf("retired must be true once the re-read proves it, got %v (%v)", dto, jerr)
	}
	// The tag alone (old abstract) is not proof.
	_, s2, err2 := runRescind(t, &rescindServer{update: lost, reread: `["spec","superseded"]`}, "", rescindArgs...)
	if err2 == nil {
		t.Fatal("a write that got no answer and cannot be confirmed must fail")
	}
	if s2.calls["UpdateSpecNode"] != 1 {
		t.Errorf("want one write attempt, got %d", s2.calls["UpdateSpecNode"])
	}
	if !strings.Contains(err2.Error(), "unknown") || !strings.Contains(err2.Error(), "hadron spec get") {
		t.Errorf("an unconfirmed write must say the state is unknown and name the check, got %v", err2)
	}
}

// The node read for the edge check and the raw read for the write must be the same node.
func TestSpecRescindRefusesWhenTheReadsDisagreeOnTheNode(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{revDiff: true}, "", rescindArgs...)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (%v)", got, err)
	}
	if s.calls["UpdateSpecNode"] != 0 {
		t.Error("no write may follow a node-identity mismatch")
	}
}

// --json is a stable DTO with the documented keys.
func TestSpecRescindJSONContract(t *testing.T) {
	out, _, err := runRescind(t, &rescindServer{}, "", append([]string{"--json"}, rescindArgs...)...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	for _, k := range []string{"loc", "memoryId", "nodeId", "revision", "retired", "wording", "withdrawn", "dryRun", "alreadyRescinded", "tags"} {
		if _, ok := dto[k]; !ok {
			t.Errorf("--json is missing %q: %v", k, dto)
		}
	}
	if dto["retired"] != true || dto["withdrawn"] != false {
		t.Errorf("dto = %v, want retired true, withdrawn false", dto)
	}
	if tags, ok := dto["tags"].([]any); !ok || len(tags) == 0 {
		t.Errorf("tags must be a non-empty array, got %v", dto["tags"])
	}
}
