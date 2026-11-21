package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#741, the rest: `node move --dry-run` asks the server's planner
// (planNodeMove, cor:api:190:03) and renders its plan. The plan replaces the
// advisory read; it does not extend it. The CLI predicts nothing itself: the
// rebased URNs, truncation, link warnings and refusals are the server's.

type planFake741 struct {
	ops    []string
	vars   []map[string]any
	plan   map[string]any // the planNodeMove payload; nil means refuse
	refuse string         // extension code of the refusal
}

func (p *planFake741) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"capabilities":[]}`)
			return
		}
		var q struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		p.ops = append(p.ops, q.OperationName)
		reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": v}) }
		switch q.OperationName {
		case "ResolveUrn":
			reply(map[string]any{"resolveUrn": map[string]any{"id": "n1", "kind": "node", "memoryId": "m1"}})
		case "PlanNodeMove":
			p.vars = append(p.vars, q.Variables)
			if p.refuse != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []any{map[string]any{
					"message": "server says " + p.refuse, "path": []string{"planNodeMove"},
					"extensions": map[string]any{"code": p.refuse},
				}}})
				return
			}
			reply(map[string]any{"planNodeMove": p.plan})
		default:
			t.Errorf("unexpected operation under --dry-run: %s", q.OperationName)
			fmt.Fprint(w, `{"errors":[{"message":"unexpected operation"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func plan741(n, total int, truncated bool, warnings any, withheld bool) map[string]any {
	nodes := []any{}
	for i := 0; i < n; i++ {
		loc := "a:b"
		if i > 0 {
			loc = fmt.Sprintf("a:b:c%d", i)
		}
		nodes = append(nodes, map[string]any{
			"id":         fmt.Sprintf("id%d", i),
			"currentUrn": "hrn:node:acme.com:kb:" + loc,
			"newUrn":     "hrn:node:acme.com:archive:x" + strings.TrimPrefix(loc, "a:b"),
		})
	}
	return map[string]any{
		"nodes": nodes, "totalCount": total, "truncated": truncated, "pointInTime": true,
		"linkWarnings": warnings, "linkWarningsWithheld": withheld,
	}
}

func run741(t *testing.T, p *planFake741, extra ...string) (string, error) {
	t.Helper()
	srv := p.server(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{"node", "move", "hrn:node:acme.com:kb:a:b", "--dry-run", "--server", srv.URL}, extra...)
	root.SetArgs(args)
	err := root.Execute()
	for _, op := range p.ops {
		if op == "MoveNode" {
			t.Fatal("--dry-run sent MoveNode")
		}
	}
	return out.String(), err
}

func onlyPlan741(t *testing.T, p *planFake741) map[string]any {
	t.Helper()
	plans := 0
	for _, op := range p.ops {
		if op == "PlanNodeMove" {
			plans++
		} else if op != "ResolveUrn" {
			t.Fatalf("--dry-run sent %s; it must send only the plan (ops: %v)", op, p.ops)
		}
	}
	if plans != 1 {
		t.Fatalf("want exactly one PlanNodeMove, got %d (ops: %v)", plans, p.ops)
	}
	return p.vars[0]
}

func TestNodeMovePlan741CompleteText(t *testing.T) {
	p := &planFake741{plan: plan741(3, 3, false, []any{}, false)}
	out, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x")
	if err != nil {
		t.Fatalf("a complete plan exits 0: %v", err)
	}
	v := onlyPlan741(t, p)
	if v["targetUrn"] != "hrn:node:acme.com:archive:x" || v["sourceRef"] == nil || v["sourceRef"] == "" {
		t.Fatalf("plan variables = %v", v)
	}
	if _, ok := v["targetMemoryRef"]; ok {
		t.Fatalf("--to-urn must omit targetMemoryRef, got %v", v)
	}
	if _, ok := v["limit"]; ok {
		t.Fatalf("without --limit the server's default applies; the CLI sent %v", v["limit"])
	}
	for _, s := range []string{
		"COMPLETE", "3 nodes",
		"hrn:node:acme.com:kb:a:b", "hrn:node:acme.com:archive:x",
		"hrn:node:acme.com:kb:a:b:c2", "hrn:node:acme.com:archive:x:c2",
		"Link warnings: none", "Nothing was written",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("text plan missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "Advisory") {
		t.Errorf("the server plan replaces the advisory read; the advisory header is still printed:\n%s", out)
	}
}

func TestNodeMovePlan741CompleteJSON(t *testing.T) {
	warn := []any{map[string]any{"nodeId": "id1", "nodeLoc": "a:b:c1", "raw": "[[../gone]]", "status": "MISSING", "target": "../gone"}}
	p := &planFake741{plan: plan741(2, 2, false, warn, false)}
	out, err := run741(t, p, "--to-memory", "hrn:mem:acme.com:archive", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := onlyPlan741(t, p)
	if v["targetMemoryRef"] != "hrn:mem:acme.com:archive" {
		t.Fatalf("plan variables = %v", v)
	}
	if _, ok := v["targetUrn"]; ok {
		t.Fatalf("--to-memory must omit targetUrn, got %v", v)
	}
	var dto struct {
		Status      string `json:"status"`
		Complete    *bool  `json:"complete"`
		TotalCount  *int   `json:"totalCount"`
		Truncated   *bool  `json:"truncated"`
		PointInTime *bool  `json:"pointInTime"`
		Nodes       []struct {
			ID         string `json:"id"`
			CurrentURN string `json:"currentUrn"`
			NewURN     string `json:"newUrn"`
		} `json:"nodes"`
		LinkWarnings []struct {
			NodeID  string `json:"nodeId"`
			NodeLoc string `json:"nodeLoc"`
			Raw     string `json:"raw"`
			Status  string `json:"status"`
			Target  string `json:"target"`
		} `json:"linkWarnings"`
		LinkWarningsWithheld *bool `json:"linkWarningsWithheld"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if dto.Status != "COMPLETE" || dto.Complete == nil || !*dto.Complete || dto.TotalCount == nil || *dto.TotalCount != 2 ||
		dto.Truncated == nil || *dto.Truncated || dto.PointInTime == nil || !*dto.PointInTime ||
		dto.LinkWarningsWithheld == nil || *dto.LinkWarningsWithheld {
		t.Fatalf("plan JSON = %s", out)
	}
	if len(dto.Nodes) != 2 || dto.Nodes[1].ID != "id1" || dto.Nodes[1].CurrentURN != "hrn:node:acme.com:kb:a:b:c1" || dto.Nodes[1].NewURN != "hrn:node:acme.com:archive:x:c1" {
		t.Fatalf("plan nodes = %s", out)
	}
	if len(dto.LinkWarnings) != 1 || dto.LinkWarnings[0] != (struct {
		NodeID  string `json:"nodeId"`
		NodeLoc string `json:"nodeLoc"`
		Raw     string `json:"raw"`
		Status  string `json:"status"`
		Target  string `json:"target"`
	}{"id1", "a:b:c1", "[[../gone]]", "MISSING", "../gone"}) {
		t.Fatalf("link warnings = %s", out)
	}
}

func TestNodeMovePlan741LinkWarningsText(t *testing.T) {
	t.Run("listed", func(t *testing.T) {
		warn := []any{map[string]any{"nodeId": "id1", "nodeLoc": "a:b:c1", "raw": "[[../gone]]", "status": "RETARGETED", "target": "../gone"}}
		out, err := run741(t, &planFake741{plan: plan741(2, 2, false, warn, false)}, "--to-memory", "hrn:mem:acme.com:archive")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"Link warnings: 1", "a:b:c1", "[[../gone]]", "RETARGETED", "../gone"} {
			if !strings.Contains(out, s) {
				t.Errorf("missing %q:\n%s", s, out)
			}
		}
	})
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("withheld json=%v", asJSON), func(t *testing.T) {
			extra := []string{"--to-memory", "hrn:mem:acme.com:archive"}
			if asJSON {
				extra = append(extra, "--json")
			}
			out, err := run741(t, &planFake741{plan: plan741(1, 1, false, nil, true)}, extra...)
			if err != nil {
				t.Fatal(err)
			}
			if !asJSON {
				if !strings.Contains(out, "Link warnings: withheld") || strings.Contains(out, "Link warnings: none") {
					t.Errorf("withheld warnings must say withheld, never none:\n%s", out)
				}
				return
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if w, ok := dto["linkWarnings"]; !ok || w != nil || dto["linkWarningsWithheld"] != true {
				t.Errorf("withheld JSON must carry linkWarnings null and linkWarningsWithheld true: %s", out)
			}
		})
	}
}

// A null linkWarnings without linkWarningsWithheld is the server not reporting
// at all: the CLI says so (never "none", never "withheld") and --json keeps the
// list null (cli#2400).
func TestNodeMovePlan741LinkWarningsNotReported(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			extra := []string{"--to-memory", "hrn:mem:acme.com:archive"}
			if asJSON {
				extra = append(extra, "--json")
			}
			out, err := run741(t, &planFake741{plan: plan741(1, 1, false, nil, false)}, extra...)
			if err != nil {
				t.Fatal(err)
			}
			if !asJSON {
				if !strings.Contains(out, "Link warnings: not reported by the server") ||
					strings.Contains(out, "Link warnings: none") || strings.Contains(out, "Link warnings: withheld") {
					t.Errorf("null warnings without withheld must say not reported:\n%s", out)
				}
				return
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if w, ok := dto["linkWarnings"]; !ok || w != nil || dto["linkWarningsWithheld"] != false {
				t.Errorf("not-reported JSON must carry linkWarnings null and linkWarningsWithheld false: %s", out)
			}
		})
	}
}

// A truncated plan is shown with its label and the full count, and exits 1:
// it is never presented as the whole subtree.
func TestNodeMovePlan741Truncated(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			extra := []string{"--to-urn", "hrn:node:acme.com:archive:x", "--limit", "5"}
			if asJSON {
				extra = append(extra, "--json")
			}
			p := &planFake741{plan: plan741(5, 250, true, []any{}, false)}
			out, err := run741(t, p, extra...)
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("a truncated plan exits 1, got %d (%v)", got, err)
			}
			// #2271 BLIND P2 (Gabriel): never promise --limit <total>; the server
			// bounds --limit, so the hint is a larger limit, not the total.
			if err == nil || !strings.Contains(err.Error(), "larger --limit") || strings.Contains(err.Error(), "--limit 250") {
				t.Errorf("the error must suggest a larger --limit without promising the total: %v", err)
			}
			if v := onlyPlan741(t, p); v["limit"] != float64(5) {
				t.Fatalf("--limit must reach the server: %v", v)
			}
			if !asJSON {
				for _, s := range []string{"TRUNCATED", "5 of 250"} {
					if !strings.Contains(out, s) {
						t.Errorf("missing %q:\n%s", s, out)
					}
				}
				if strings.Contains(out, "COMPLETE") {
					t.Errorf("a truncated plan reads as complete:\n%s", out)
				}
				return
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["status"] != "TRUNCATED" || dto["complete"] != false || dto["truncated"] != true || dto["totalCount"] != float64(250) {
				t.Errorf("truncated JSON = %s", out)
			}
			if nodes, _ := dto["nodes"].([]any); len(nodes) != 5 {
				t.Errorf("truncated JSON must carry the nodes the server listed: %s", out)
			}
		})
	}
}

// #2271 BLIND P2 (Gabriel): when the server lists fewer nodes than --limit
// asked for, that is its bound. The error says a plan cannot list this subtree
// in full, and never sends the user to rerun with a limit it will clamp.
func TestNodeMovePlan741TruncatedAtServerBound(t *testing.T) {
	p := &planFake741{plan: plan741(1000, 2500, true, []any{}, false)}
	_, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x", "--limit", "5000")
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if v := onlyPlan741(t, p); v["limit"] != float64(5000) {
		t.Fatalf("--limit must reach the server unchanged: %v", v)
	}
	msg := err.Error()
	if !strings.Contains(msg, "server's bound") || !strings.Contains(msg, "1000 of 2500") || strings.Contains(msg, "larger --limit") || strings.Contains(msg, "--limit 2500") {
		t.Fatalf("at the server's bound the error must say so, not suggest a rerun: %v", msg)
	}
}

// #2271 BLIND P3: --limit below 1 is refused before any request.
func TestNodeMovePlan741LimitBelowOne(t *testing.T) {
	for _, l := range []string{"0", "-3"} {
		t.Run(l, func(t *testing.T) {
			p := &planFake741{}
			_, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x", "--limit="+l)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage (%v)", got, err)
			}
			if len(p.ops) != 0 {
				t.Fatalf("a refused --limit sent %v", p.ops)
			}
		})
	}
}

// #2271 BLIND P3: a null plan is an error, never an empty COMPLETE plan.
func TestNodeMovePlan741NullPlan(t *testing.T) {
	p := &planFake741{}
	out, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x")
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if strings.Contains(out, "COMPLETE") {
		t.Fatalf("a null plan printed a plan:\n%s", out)
	}
}

// Either signal alone makes the plan TRUNCATED: the server's flag, or a list
// shorter than the count it reports.
func TestNodeMovePlan741TruncatedEitherSignal(t *testing.T) {
	for name, plan := range map[string]map[string]any{
		"flag only":  plan741(3, 3, true, []any{}, false),
		"count only": plan741(2, 3, false, []any{}, false),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := run741(t, &planFake741{plan: plan}, "--to-urn", "hrn:node:acme.com:archive:x")
			if got := exitCodeFor(err); got != exitcode.Error || !strings.Contains(out, "TRUNCATED") || strings.Contains(out, "COMPLETE") {
				t.Fatalf("exit %d, output:\n%s", got, out)
			}
		})
	}
}

// The plan raises the move's own refusal; the CLI exits as the real move would
// and prints no plan.
func TestNodeMovePlan741Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"NODE_ALREADY_EXISTS", exitcode.Conflict},
		{"BAD_USER_INPUT", exitcode.Usage},
		{"NODE_NOT_FOUND", exitcode.NotFound},
		{"COMMENT_MOVE_UNCOMMENTABLE", exitcode.Usage},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s json=%v", tc.code, asJSON), func(t *testing.T) {
				extra := []string{"--to-urn", "hrn:node:acme.com:archive:x"}
				if asJSON {
					extra = append(extra, "--json")
				}
				p := &planFake741{refuse: tc.code}
				out, err := run741(t, p, extra...)
				onlyPlan741(t, p)
				if got := exitCodeFor(err); got != tc.exit {
					t.Fatalf("exit = %d, want %d as the real move (%v)", got, tc.exit, err)
				}
				if err == nil || !strings.Contains(err.Error(), "server says "+tc.code) {
					t.Errorf("the refusal must carry the server's message: %v", err)
				}
				if strings.Contains(out, "COMPLETE") || strings.Contains(out, "currentUrn") || strings.Contains(out, "Nothing was written") {
					t.Errorf("a refused plan printed a plan:\n%s", out)
				}
			})
		}
	}
}

// --limit only means something for a plan; a real move refuses it before any
// request.
func TestNodeMovePlan741LimitNeedsDryRun(t *testing.T) {
	p := &planFake741{}
	srv := p.server(t)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", "hrn:node:acme.com:kb:a:b", "--to-urn", "hrn:node:acme.com:archive:x", "--limit", "5", "--server", srv.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage (%v)", got, err)
	}
	if len(p.ops) != 0 {
		t.Fatalf("a refused flag combination sent %v", p.ops)
	}
}

// A bare <loc> with -m still names the source, as the real move resolves it.
func TestNodeMovePlan741BareLoc(t *testing.T) {
	p := &planFake741{plan: plan741(1, 1, false, []any{}, false)}
	srv := p.server(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "move", "a:b", "-m", "acme.com:kb", "--to-memory", "hrn:mem:acme.com:archive", "--dry-run", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	v := onlyPlan741(t, p)
	if v["sourceRef"] == nil || v["sourceRef"] == "" {
		t.Fatalf("plan variables = %v", v)
	}
	if !strings.Contains(out.String(), "COMPLETE") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// #2290 D1: when the server lists exactly --limit nodes of a larger subtree,
// the CLI cannot tell whether --limit is at the server's bound, so the hint is
// conditional: no firm claim either way, no bound copied, still TRUNCATED with
// the listed and total counts (cor:api:240:01, cor:api:190:03).
func TestNodeMovePlan2290TruncatedAtExactlyTheLimitIsConditional(t *testing.T) {
	p := &planFake741{plan: plan741(1000, 2500, true, []any{}, false)}
	out, err := run741(t, p, "--to-urn", "hrn:node:acme.com:archive:x", "--limit", "1000")
	if got := exitCodeFor(err); got != exitcode.Error || err == nil {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if v := onlyPlan741(t, p); v["limit"] != float64(1000) {
		t.Fatalf("--limit must reach the server unchanged: %v", v)
	}
	msg := err.Error()
	for _, want := range []string{"TRUNCATED", "1000 of 2500", "larger --limit", "if --limit is already at the server's bound", "a plan cannot list this subtree in full"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %v", want, msg)
		}
	}
	// No firm claim: "that is the server's bound" is for listed < limit only,
	// and no copied bound value.
	if strings.Contains(msg, "that is the server's bound") || strings.Contains(msg, "--limit 2500") {
		t.Errorf("a firm claim at listed == limit: %v", msg)
	}
	if !strings.Contains(out, "TRUNCATED") || strings.Contains(out, "COMPLETE") {
		t.Errorf("the receipt must read TRUNCATED:\n%s", out)
	}
}

// #2290 D2: the DTO comment describes what renderMovePlan prints for a null
// linkWarnings without the withheld flag.
func TestMovePlanDTOCommentMatchesTheRenderer2290(t *testing.T) {
	b, err := os.ReadFile("node/move_plan.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\n// ", " ")
	if strings.Contains(src, "the server reported none") {
		t.Error("the movePlanDTO comment still says the server reported none")
	}
	if !strings.Contains(src, `the text says "not reported by the server"`) {
		t.Error("the movePlanDTO comment does not quote the renderer's wording")
	}
}
