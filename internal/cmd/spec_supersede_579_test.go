package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		case "AuthContext":
			data["authContext"] = nil
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
				if (in["ref"] != nil && v["id"] == in["ref"]) || (in["loc"] != nil && v["loc"] == in["loc"]) {
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
		case "SpecLintBatch": // #3127: the server finds nothing to lint here
			data["nodeBatch"] = map[string]any{"nodes": []any{}, "truncated": false, "omitted": []any{}, "unavailable": []any{}}
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
