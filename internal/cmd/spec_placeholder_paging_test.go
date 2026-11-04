package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// cli#967: the draft placeholder scan must read past a page the server clamps
// below nodesPageSize, or a placeholder behind it is reported as resolved.
func TestSpecDescribePlaceholderScanPastAClampedPage(t *testing.T) {
	rows := make([]string, 130)
	for i := range rows {
		ph := "false"
		if i == 100 {
			ph = "true"
		}
		rows[i] = fmt.Sprintf(`{"node":{"loc":"msg:010:%03d","isPlaceholder":%s}}`, i, ph)
	}
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"SpecPlaceholderScan": {rows: rows, serve: 50, wrap: func(page []string, _ int) string {
			return `{"data":{"findNodes":{"hits":[` + strings.Join(page, ",") + `]}}}`
		}},
	}, map[string]string{
		"Memories":        memListJSON,
		"GetMemory":       memGetJSON(`null`),
		"FindNodes":       `{"data":{"nodes":[` + specNodeList("msg:010:100", `["spec"]`) + `]}}`,
		"SpecCorpusState": draftStateJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		PlaceholderCount *int `json:"placeholderCount"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.PlaceholderCount == nil || *got.PlaceholderCount != 1 {
		t.Fatalf("placeholderCount = %v, want 1 (a placeholder behind a clamped page was missed)", got.PlaceholderCount)
	}
}
