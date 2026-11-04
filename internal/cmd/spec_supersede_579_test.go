package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A stateful corpus fake, not a canned success: writes alter subsequent raw,
// detail and lint reads, and the abstract fingerprint follows the actual write.
type corpus579 struct {
	nodes               map[string]map[string]any
	calls               []string
	writes              int
	creates             int
	targetReads         int
	renameOnReread      bool
	placeholderOnReread bool
	concurrentUpdate    bool
	lastUpdate          map[string]any
	edgeMode            string
	updateMode          string
	competingAfterEdge  bool
	updateAttempted     bool
	onRead              func(op string, c *corpus579)
}

func node579(id, loc string) map[string]any {
	body := "# Historical {{rule}}\n"
	return map[string]any{"id": id, "memoryId": "mem1", "loc": loc, "name": "Name " + loc, "nodeType": "info", "role": "spec.rule", "tags": []any{"spec"}, "abstract": "Original summary.", "abstractOriginHash": specOriginHash(body), "content": body, "outgoingEdges": []any{}, "incomingEdges": []any{}, "revision": 1, "isPlaceholder": false, "deletedAt": nil}
}
func server579(t *testing.T) (*httptest.Server, *corpus579) {
	t.Helper()
	c := &corpus579{nodes: map[string]map[string]any{"old": node579("old", "app:sch:020"), "new": node579("new", "srv:sch:030")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		c.calls = append(c.calls, q.OperationName)
		data := map[string]any{}
		switch q.OperationName {
		case "SpecCorpusState", "GetMemory":
			data["memory"] = map[string]any{"id": "mem1", "urn": "hrn:mem:micromentor.org:platform-specs", "corpusState": "MINTED", "vectorIndexEnabled": true}
		case "Memories":
			var response map[string]any
			_ = json.Unmarshal([]byte(memListMicromentorJSON), &response)
			data = response["data"].(map[string]any)
		case "ResolveUrn":
			urn, _ := q.Variables["urn"].(string)
			var found map[string]any
			for _, n := range c.nodes {
				if strings.HasSuffix(urn, "::"+n["loc"].(string)) {
					found = n
					break
				}
			}
			data["resolveUrn"] = nil
			if found != nil {
				data["resolveUrn"] = map[string]any{"id": found["id"], "kind": "node", "memoryId": found["memoryId"]}
			}
		case "GetNode", "GetSpecNodeRaw", "GetSpecNodeForEdit", "GetSpecSupersedeTarget":
			ref, _ := q.Variables["ref"].(string)
			if c.onRead != nil {
				c.onRead(q.OperationName, c)
			}
			n := c.nodes[ref]
			if ref == "new" {
				c.targetReads++
				if c.placeholderOnReread && c.targetReads > 1 {
					n["isPlaceholder"] = true
				}
				if c.renameOnReread && c.targetReads > 1 {
					n["name"] = "Fresh target name"
				}
			}
			data["node"] = n
		case "CreateEdge":
			if c.edgeMode == "lost-before" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("lost answer"))
				return
			}
			c.writes++
			from, _ := q.Variables["sourceRef"].(string)
			to, _ := q.Variables["targetRef"].(string)
			name, _ := q.Variables["name"].(string)
			if c.nodes[from] == nil || c.nodes[to] == nil {
				t.Errorf("bad edge vars %+v", q.Variables)
				return
			}
			e := map[string]any{"id": "e1", "name": name, "target": map[string]any{"id": to, "loc": c.nodes[to]["loc"], "memoryId": c.nodes[to]["memoryId"]}}
			c.nodes[from]["outgoingEdges"] = append(c.nodes[from]["outgoingEdges"].([]any), e)
			data["createEdge"] = e
			if c.competingAfterEdge {
				c.nodes["other"] = node579("other", "srv:sch:040")
				other := map[string]any{"id": "e2", "name": "superseded-by", "target": map[string]any{"id": "other", "loc": "srv:sch:040", "memoryId": "mem1"}}
				c.nodes[from]["outgoingEdges"] = append(c.nodes[from]["outgoingEdges"].([]any), other)
			}
			if c.edgeMode == "lost-after" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("lost answer"))
				return
			}
		case "UpdateSpecNode":
			c.updateAttempted = true
			if c.updateMode == "lost-before" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("lost answer"))
				return
			}
			in := q.Variables["input"].(map[string]any)
			c.lastUpdate = in
			var n map[string]any
			for _, v := range c.nodes {
				if (in["id"] != nil && v["id"] == in["id"]) || (in["loc"] != nil && v["loc"] == in["loc"]) {
					n = v
					break
				}
			}
			if n == nil {
				t.Errorf("update missing node %+v", in)
				return
			}
			if c.concurrentUpdate {
				n["revision"] = 2
				n["abstract"] = "Concurrent summary"
				c.concurrentUpdate = false
			}
			if expected, ok := in["expectedRevision"]; ok && expected != float64(n["revision"].(int)) {
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "revision changed", "extensions": map[string]any{"code": "NODE_WRITE_CONFLICT"}}}})
				return
			}
			c.writes++
			for _, k := range []string{"abstract", "content", "tags"} {
				if v, ok := in[k]; ok && (k != "abstract" || c.updateMode != "tag-only") {
					n[k] = v
				}
			}
			if _, ok := in["abstract"]; ok {
				n["abstractOriginHash"] = specOriginHash(n["content"].(string))
			}
			data["updateSpecNode"] = n
			if c.updateMode == "lost-after" || c.updateMode == "tag-only" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("lost answer"))
				return
			}
		case "CreateSpecNode":
			c.writes++
			c.creates++
			in := q.Variables["input"].(map[string]any)
			n := node579("created", in["loc"].(string))
			n["name"] = in["name"]
			c.nodes["created"] = n
			data["createSpecNode"] = n
		case "NodeBatch", "NodeBatchContent":
			var ns []any
			for _, ref := range q.Variables["refs"].([]any) {
				if n := c.nodes[ref.(string)]; n != nil {
					ns = append(ns, n)
				}
			}
			data["nodeBatch"] = map[string]any{"nodes": ns, "truncated": false, "omitted": []any{}, "unavailable": []any{}}
		default:
			t.Errorf("unexpected operation %s", q.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, c
}
func args579(url string, extra ...string) []string {
	return append([]string{"spec", "supersede", "app:sch:020", "-m", specMem, "--yes", "--json", "--server", url}, extra...)
}
func run579(t *testing.T, url string, extra ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs(args579(url, extra...))
	err := r.Execute()
	if err != nil && strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("gated flag is not implemented: %v", err)
	}
	return out.String(), err
}
func edge579(c *corpus579, from, to string) {
	c.nodes[from]["outgoingEdges"] = []any{map[string]any{"id": "edge", "name": "superseded-by", "target": map[string]any{"id": to, "loc": c.nodes[to]["loc"], "memoryId": c.nodes[to]["memoryId"]}}}
}

func TestSpec579ExistingSuccessorAndAbstract(t *testing.T) {
	for _, same := range []bool{false, true} {
		for _, summary := range []string{"", "A shorter history."} {
			t.Run(fmt.Sprint(same, summary), func(t *testing.T) {
				srv, c := server579(t)
				if same {
					c.nodes["new"]["loc"] = "app:sch:030"
				}
				c.renameOnReread = true
				extra := []string{"--by", c.nodes["new"]["loc"].(string)}
				if summary != "" {
					extra = append(extra, "--summary", summary)
				}
				before := time.Now().UTC().Format("2006-01-02")
				out, err := run579(t, srv.URL, extra...)
				if err != nil {
					t.Fatal(err)
				}
				if _, present := c.lastUpdate["content"]; present {
					t.Fatal("--by rewrote content", c.lastUpdate)
				}
				if c.lastUpdate["id"] != "old" || c.lastUpdate["expectedRevision"] != float64(1) {
					t.Fatalf("unguarded update %+v", c.lastUpdate)
				}
				if c.creates != 0 || len(c.nodes) != 2 || c.writes != 2 {
					t.Fatalf("writes %d creates %d nodes %d", c.writes, c.creates, len(c.nodes))
				}
				historical := "Original summary."
				if summary != "" {
					historical = summary
				}
				want := "SUPERSEDED (" + before + ") by " + c.nodes["new"]["loc"].(string) + " (Fresh target name); do not apply. Historical: " + historical
				if c.nodes["old"]["abstract"] != want {
					t.Fatalf("abstract %v want %s", c.nodes["old"]["abstract"], want)
				}
				if c.nodes["old"]["content"] != "# Historical {{rule}}\n" {
					t.Fatal("stored template lost")
				}
				var dto map[string]any
				if json.Unmarshal([]byte(out), &dto) != nil || dto["new"] != c.nodes["new"]["loc"] || dto["retired"] != true {
					t.Fatal(out)
				}
				f, lintOut := testFactory(t)
				r := NewRootCmd(f)
				r.SetArgs([]string{"spec", "lint", "app:sch:020", "-m", specMem, "--strict", "--json", "--server", srv.URL})
				if err := r.Execute(); err != nil {
					t.Fatalf("lint %v %s", err, lintOut.String())
				}
			})
		}
	}
}
func TestSpec579RefusalsBeforeWrites(t *testing.T) {
	for _, kind := range []string{"missing", "not-spec", "retired", "placeholder", "deleted", "foreign", "self", "cycle", "unreadable-chain"} {
		t.Run(kind, func(t *testing.T) {
			srv, c := server579(t)
			by := "srv:sch:030"
			switch kind {
			case "missing":
				delete(c.nodes, "new")
			case "not-spec":
				c.nodes["new"]["role"] = "info"
				c.nodes["new"]["tags"] = []any{}
			case "retired":
				c.nodes["new"]["tags"] = []any{"spec", "superseded"}
			case "placeholder":
				c.nodes["new"]["isPlaceholder"] = true
			case "deleted":
				c.nodes["new"]["deletedAt"] = "2026-01-01T00:00:00Z"
			case "foreign":
				c.nodes["new"]["memoryId"] = "mem2"
			case "self":
				by = "app:sch:020"
			case "cycle":
				edge579(c, "new", "old")
			case "unreadable-chain":
				c.nodes["new"]["outgoingEdges"] = []any{map[string]any{"id": "hidden", "name": "superseded-by", "target": nil}}
			}
			_, err := run579(t, srv.URL, "--by", by)
			if err == nil {
				t.Fatal("refusal missing")
			}
			if c.writes != 0 {
				t.Fatalf("refusal wrote %d", c.writes)
			}
		})
	}
}
func TestSpec579PresenceConflictsBeforeHTTP(t *testing.T) {
	for _, flags := range [][]string{{"--by", "x", "--title", "T"}, {"--by", "x", "--to", "other"}, {"--by", "x", "--feature", "020"}, {"--by", "x", "--rule-after", "03"}, {"--by", "x", "--copy-body"}, {"--by", ""}, {"--by", " "}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			srv, c := server579(t)
			_, err := run579(t, srv.URL, flags...)
			if exitCodeFor(err) != 2 || len(c.calls) != 0 {
				t.Fatalf("err %v calls %v", err, c.calls)
			}
		})
	}
}
func TestSpec579Reruns(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprint(different), func(t *testing.T) {
			srv, c := server579(t)
			edge579(c, "old", "new")
			c.nodes["old"]["tags"] = []any{"spec", "superseded"}
			target := "srv:sch:030"
			if different {
				target = "srv:sch:031"
			}
			c.nodes["old"]["abstract"] = "SUPERSEDED (2026-01-01) by " + target + " (Original name); do not apply. Historical: Old."
			out, err := run579(t, srv.URL, "--by", "srv:sch:030")
			if different && err == nil {
				t.Fatal("different successor accepted")
			}
			if !different && err != nil {
				t.Fatal(err)
			}
			if c.writes != 0 {
				t.Fatal("rerun wrote")
			}
			if !different && !strings.Contains(out, `"retired": true`) {
				t.Fatal(out)
			}
		})
	}
}
func TestSpec579CreatePathAbstract(t *testing.T) {
	srv, c := server579(t)
	_, err := run579(t, srv.URL, "--to", "app:sch:next", "--title", "New rule")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.nodes["old"]["abstract"].(string)
	if !regexp.MustCompile(`^SUPERSEDED \(\d{4}-\d{2}-\d{2}\) by app:sch:next \(.+\); do not apply\. Historical: Original summary\.$`).MatchString(a) {
		t.Fatal(a)
	}
}
func TestSpec579LintRetirementLead(t *testing.T) {
	srv, c := server579(t)
	c.nodes["old"]["tags"] = []any{"spec", "superseded"}
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"spec", "lint", "app:sch:020", "-m", specMem, "--strict", "--json", "--server", srv.URL})
	if err := r.Execute(); exitCodeFor(err) != 5 || !strings.Contains(out.String(), "superseded-abstract") {
		t.Fatalf("lint %v %s", err, out.String())
	}
}

func TestSpec579TargetRevalidatedAndAbstractBudget(t *testing.T) {
	t.Run("target becomes placeholder", func(t *testing.T) {
		srv, c := server579(t)
		c.placeholderOnReread = true
		_, err := run579(t, srv.URL, "--by", "srv:sch:030")
		if err == nil || c.writes != 0 {
			t.Fatalf("err %v writes %d", err, c.writes)
		}
	})
	for _, mode := range []string{"existing", "create"} {
		t.Run(mode, func(t *testing.T) {
			srv, c := server579(t)
			c.nodes["old"]["abstract"] = strings.Repeat("x", 1990)
			args := []string{"--by", "srv:sch:030"}
			if mode == "create" {
				args = []string{"--to", "app:sch:next", "--title", "Next"}
			}
			_, err := run579(t, srv.URL, args...)
			if exitCodeFor(err) != 2 || c.writes != 0 {
				t.Fatalf("err %v writes %d", err, c.writes)
			}
			_, err = run579(t, srv.URL, append(args, "--summary", "Short history.")...)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestSpec579CreateRerunNoWrite(t *testing.T) {
	srv, c := server579(t)
	args := []string{"--to", "app:sch:next", "--title", "Next"}
	if _, err := run579(t, srv.URL, args...); err != nil {
		t.Fatal(err)
	}
	before := c.writes
	out, err := run579(t, srv.URL, args...)
	if err != nil || c.writes != before || !strings.Contains(out, `"unchanged": true`) {
		t.Fatalf("err %v writes %d/%d %s", err, c.writes, before, out)
	}
}

func TestSpec579AbstractCapCountsUTF16(t *testing.T) {
	srv, c := server579(t)
	c.nodes["old"]["abstract"] = strings.Repeat("😀", 990)
	_, err := run579(t, srv.URL, "--by", "srv:sch:030")
	if exitCodeFor(err) != 2 || c.writes != 0 {
		t.Fatalf("err %v writes %d", err, c.writes)
	}
}

func TestSpec579HelpAndContract(t *testing.T) {
	for _, args := range [][]string{{"spec", "supersede", "--help"}, {"agentic-usage"}} {
		f, out := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(args)
		if err := r.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"--by", "--summary", "SUPERSEDED ("} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("args %v missing %s", args, want)
			}
		}
	}
}
func TestSpec579LintWordBoundary(t *testing.T) {
	srv, c := server579(t)
	c.nodes["old"]["tags"] = []any{"spec", "superseded"}
	c.nodes["old"]["abstract"] = "This rule is unsuperseded."
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"spec", "lint", "app:sch:020", "-m", specMem, "--strict", "--json", "--server", srv.URL})
	if err := r.Execute(); exitCodeFor(err) != 5 || !strings.Contains(out.String(), "superseded-abstract") {
		t.Fatalf("err %v %s", err, out.String())
	}
}

func TestSpec579ConcurrentEditNotOverwritten(t *testing.T) {
	srv, c := server579(t)
	c.concurrentUpdate = true
	out, err := run579(t, srv.URL, "--by", "srv:sch:030")
	if err == nil || c.nodes["old"]["abstract"] != "Concurrent summary" || c.creates != 0 || c.writes != 1 {
		t.Fatalf("err %v writes %d abstract %v %s", err, c.writes, c.nodes["old"]["abstract"], out)
	}
}

func TestSpec579InconsistentLeadCompleted(t *testing.T) {
	for _, mode := range []string{"by", "create"} {
		for _, missing := range []string{"edge", "tag", "edge-and-tag"} {
			if mode == "create" && missing != "tag" {
				continue
			}
			t.Run(mode+"/"+missing, func(t *testing.T) {
				srv, c := server579(t)
				c.nodes["old"]["abstract"] = "SUPERSEDED (2026-01-01) by srv:sch:030 (N); do not apply. Historical: Old."
				if missing == "tag" {
					edge579(c, "old", "new")
				}
				if missing == "edge" {
					c.nodes["old"]["tags"] = []any{"spec", "superseded"}
				}
				flags := []string{"--by", "srv:sch:030"}
				if mode == "create" {
					flags = []string{"--title", "N"}
				}
				beforeAbstract := c.nodes["old"]["abstract"]
				out, err := run579(t, srv.URL, flags...)
				if c.nodes["old"]["abstract"] != beforeAbstract {
					t.Fatal("matching abstract changed")
				}
				wantWrites := 1
				if missing != "tag" {
					wantWrites = 2
				}
				if err != nil || c.writes != wantWrites || strings.Contains(out, `"unchanged": true`) || len(c.nodes["old"]["outgoingEdges"].([]any)) != 1 || !strings.Contains(out, `"retired": true`) {
					t.Fatalf("inconsistent state was not completed: err=%v writes=%d out=%s", err, c.writes, out)
				}
			})
		}
	}
}

func TestSpec579ByReasonRefusedBeforeRequest(t *testing.T) {
	for _, reason := range []string{"Audit reason", ""} {
		srv, c := server579(t)
		_, err := run579(t, srv.URL, "--by", "srv:sch:030", "--reason", reason)
		if exitCodeFor(err) != 2 || len(c.calls) != 0 {
			t.Fatalf("reason silently discarded: err=%v calls=%v", err, c.calls)
		}
	}
}

func TestSpec579LegacyRepairAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			srv, c := server579(t)
			edge579(c, "old", "new")
			c.nodes["old"]["tags"] = []any{"spec", "superseded"}
			before := c.nodes["old"]["content"]
			flags := []string{"--by", "srv:sch:030"}
			if dry {
				flags = append(flags, "--dry-run")
			}
			out, err := run579(t, srv.URL, flags...)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if dry {
				want = 0
			}
			if c.writes != want || c.creates != 0 || c.nodes["old"]["content"] != before {
				t.Fatalf("writes=%d creates=%d out=%s", c.writes, c.creates, out)
			}
			if !dry && (!strings.HasPrefix(c.nodes["old"]["abstract"].(string), "SUPERSEDED (") || c.lastUpdate["expectedRevision"] != float64(1)) {
				t.Fatal("legacy repair not guarded", c.lastUpdate)
			}
		})
	}
}

func TestSpec579ByPartialWrites(t *testing.T) {
	for _, tc := range []struct {
		name, edgeMode, updateMode string
		compete                    bool
		wantErr                    bool
		wantWrites                 int
		retired                    any
	}{
		{"edge-missing-after-lost", "lost-before", "", false, true, 0, false},
		{"edge-landed-after-lost", "lost-after", "", false, false, 2, true},
		{"competing-edge", "", "", true, true, 1, false},
		{"retirement-landed-after-lost", "", "lost-after", false, false, 2, true},
		{"retirement-not-confirmed", "", "lost-before", false, true, 1, nil},
		{"tag-only-not-success", "", "tag-only", false, true, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, c := server579(t)
			c.edgeMode = tc.edgeMode
			c.updateMode = tc.updateMode
			c.competingAfterEdge = tc.compete
			out, err := run579(t, srv.URL, "--by", "srv:sch:030")
			if (err != nil) != tc.wantErr || c.writes != tc.wantWrites || c.creates != 0 {
				t.Fatalf("err=%v writes=%d out=%s", err, c.writes, out)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out), &dto); e != nil {
				t.Fatal(e, out)
			}
			if dto["retired"] != tc.retired {
				t.Fatalf("retired=%v want=%v err=%v", dto["retired"], tc.retired, err)
			}
			if tc.compete && (exitCodeFor(err) != 5 || c.updateAttempted) {
				t.Fatalf("competing link retired: %v", err)
			}
			if tc.edgeMode == "lost-before" && (c.updateAttempted || !strings.Contains(out, `"status": "unknown"`)) {
				t.Fatalf("unconfirmed edge was retired: %s", out)
			}
			updates, edges := 0, 0
			for _, op := range c.calls {
				if op == "UpdateSpecNode" {
					updates++
				}
				if op == "CreateEdge" {
					edges++
				}
			}
			if updates > 1 || edges > 1 {
				t.Fatalf("retry: %v", c.calls)
			}
		})
	}
}

func TestSpec579ExistingOtherAndPartialRerun(t *testing.T) {
	for _, other := range []bool{false, true} {
		t.Run(fmt.Sprint(other), func(t *testing.T) {
			srv, c := server579(t)
			if other {
				c.nodes["other"] = node579("other", "srv:sch:040")
				edge579(c, "old", "other")
			} else {
				edge579(c, "old", "new")
			}
			_, err := run579(t, srv.URL, "--by", "srv:sch:030")
			if other {
				if exitCodeFor(err) != 5 || c.writes != 0 {
					t.Fatalf("other accepted: %v", err)
				}
			} else {
				if err != nil || c.writes != 1 {
					t.Fatalf("partial not repaired: %v writes=%d", err, c.writes)
				}
				for _, op := range c.calls {
					if op == "CreateEdge" {
						t.Fatal("duplicated edge")
					}
				}
			}
		})
	}
}

func TestSpec579HumanAndFreshDryRun(t *testing.T) {
	srv, c := server579(t)
	c.nodes["new"]["name"] = "Target\x1b[31m\nlabel"
	invoke := func(extra ...string) (string, error) {
		f, out := testFactory(t)
		r := NewRootCmd(f)
		args := args579(srv.URL, append([]string{"--by", "srv:sch:030"}, extra...)...)
		clean := []string{}
		for _, a := range args {
			if a != "--json" {
				clean = append(clean, a)
			}
		}
		r.SetArgs(clean)
		err := r.Execute()
		return out.String(), err
	}
	out, err := invoke("--dry-run")
	if err != nil || c.writes != 0 {
		t.Fatalf("dry run wrote: %v %s", err, out)
	}
	out, err = invoke()
	if err != nil || strings.Contains(out, "\x1b") || strings.Contains(out, "Target\n") {
		t.Fatalf("unsafe output: %v %q", err, out)
	}
	out, err = invoke()
	if err != nil || !strings.Contains(out, "unchanged") || c.writes != 2 {
		t.Fatalf("human rerun: %v %q writes=%d", err, out, c.writes)
	}
}

func TestSpec579RescindedAndWithdrawnLint(t *testing.T) {
	for _, lead := range []string{"RESCINDED (2026-01-01); do not apply. Historical: Old.", "WITHDRAWN (2026-01-01); do not apply. Historical: Old."} {
		t.Run(lead[:9], func(t *testing.T) {
			srv, c := server579(t)
			c.nodes["old"]["tags"] = []any{"spec", "superseded"}
			c.nodes["old"]["abstract"] = lead
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"spec", "lint", "app:sch:020", "-m", specMem, "--json", "--strict", "--server", srv.URL})
			err := r.Execute()
			if err != nil || strings.Contains(out.String(), "superseded-abstract") {
				t.Fatalf("valid retirement flagged: err=%v %s", err, out)
			}
		})
	}
}
