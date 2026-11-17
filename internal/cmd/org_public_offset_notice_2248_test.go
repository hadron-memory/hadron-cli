package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// cli#2248: `org public` text output says when a list is partial for any
// reason, not only when the server says more rows follow. A short last page
// reached with --offset, or an offset past the end, no longer reads as a
// complete list or as "(none)" while the server counts rows. --json is
// unchanged. Rule (cor:api:120:04, spec FIT on the issue): partial means
// hasMore || rows < total; "(none)" only when total is 0.

type publicPage2248 struct {
	rows    int
	total   int
	hasMore bool
}

func publicOrgReply2248(memories, agents publicPage2248) string {
	mk := func(prefix string, p publicPage2248) map[string]any {
		items := make([]map[string]any, p.rows)
		for i := range items {
			items[i] = map[string]any{"id": fmt.Sprint(i), "name": fmt.Sprintf("%s%02d", prefix, i), "urn": fmt.Sprintf("acme.com::%s%d", prefix, i), "description": nil}
		}
		return map[string]any{"items": items, "total": p.total, "hasMore": p.hasMore}
	}
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"publicOrganization": map[string]any{
		"id": "o", "urn": "acme.com", "name": "Acme", "listedOnMarketplace": true,
		"publicMemoriesPage": mk("Mem", memories), "publicAgentsPage": mk("Agent", agents),
	}}})
	return string(body)
}

func runOrgPublic2248(t *testing.T, reply string, args ...string) string {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{"PublicOrganization": reply})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"org", "public", "acme.com", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// listBlock2248 is the text of one list: its header line and the rows under it.
func listBlock2248(t *testing.T, out, label string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  "+label+":") {
			block := []string{l}
			for _, r := range lines[i+1:] {
				if !strings.HasPrefix(r, "    - ") {
					break
				}
				block = append(block, r)
			}
			return strings.Join(block, "\n")
		}
	}
	t.Fatalf("no %q list in:\n%s", label, out)
	return ""
}

func TestOrgPublicOffsetNotice2248(t *testing.T) {
	full := publicPage2248{rows: 3, total: 3}
	for _, tc := range []struct {
		name     string
		memories publicPage2248
		args     []string
		header   string // the first line of the memories list
		rows     int
	}{
		{"short last page", publicPage2248{rows: 5, total: 45}, []string{"--offset", "40"}, "  public memories: showing 5 of 45 (offset 40)", 5},
		{"offset past the end", publicPage2248{rows: 0, total: 45}, []string{"--offset", "100"}, "  public memories: showing 0 of 45 (offset 100)", 0},
		{"counts only", publicPage2248{rows: 0, total: 45}, []string{"--limit", "0"}, "  public memories: showing 0 of 45", 0},
		{"counts only, server says more", publicPage2248{rows: 0, total: 45, hasMore: true}, []string{"--limit", "0"}, "  public memories: showing 0 of 45", 0},
		{"more follow, offset given", publicPage2248{rows: 20, total: 45, hasMore: true}, []string{"--offset", "20"}, "  public memories: showing 20 of 45 (offset 20)", 20},
		{"more follow, no offset", publicPage2248{rows: 20, total: 45, hasMore: true}, nil, "  public memories: showing 20 of 45", 20},
		{"empty, offset given", publicPage2248{rows: 0, total: 0}, []string{"--offset", "5"}, "  public memories: (none)", 0},
		{"empty", publicPage2248{rows: 0, total: 0}, nil, "  public memories: (none)", 0},
		{"complete list", publicPage2248{rows: 3, total: 3}, nil, "  public memories:", 3},
		{"complete list, offset zero given", publicPage2248{rows: 3, total: 3}, []string{"--offset", "0"}, "  public memories:", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrgPublic2248(t, publicOrgReply2248(tc.memories, full), tc.args...)
			block := listBlock2248(t, out, "public memories")
			lines := strings.Split(block, "\n")
			if lines[0] != tc.header || len(lines)-1 != tc.rows {
				t.Fatalf("memories block:\n%s\nwant header %q and %d rows", block, tc.header, tc.rows)
			}
			// The other list is a complete page and says nothing about paging.
			agents := listBlock2248(t, out, "public agents")
			if strings.Contains(agents, "showing") || strings.Contains(agents, "(none)") {
				t.Fatalf("a complete list grew a notice:\n%s", agents)
			}
		})
	}
}

// Each list is judged on its own counts, and --json keeps exactly the keys it
// had: the notice is a text-only refinement.
func TestOrgPublicOffsetNoticeIsPerListAndJSONUnchanged2248(t *testing.T) {
	reply := publicOrgReply2248(publicPage2248{rows: 5, total: 45}, publicPage2248{rows: 0, total: 45})
	out := runOrgPublic2248(t, reply, "--offset", "40")
	if !strings.Contains(out, "public memories: showing 5 of 45 (offset 40)") || !strings.Contains(out, "public agents: showing 0 of 45 (offset 40)") {
		t.Fatalf("each list needs its own notice:\n%s", out)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(runOrgPublic2248(t, reply, "--offset", "40", "--json")), &got); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "id,listedOnMarketplace,name,publicAgents,publicAgentsHasMore,publicAgentsTotal,publicMemories,publicMemoriesHasMore,publicMemoriesTotal,urn"
	if strings.Join(keys, ",") != want || string(got["publicMemoriesTotal"]) != "45" || string(got["publicMemoriesHasMore"]) != "false" {
		t.Fatalf("--json changed: %v\n%v", keys, got)
	}
}
