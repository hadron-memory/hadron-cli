package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

type lintWarningPageClient struct {
	pageJSON string
	calls    int
	endless  bool
}

func (c *lintWarningPageClient) MakeRequest(_ context.Context, _ *graphql.Request, r *graphql.Response) error {
	c.calls++
	page := c.pageJSON
	if c.endless {
		page = fmt.Sprintf(`{"items":[{"kind":"BROKEN_INCLUDE","position":1,"text":"x"}],"total":1001,"nextCursor":"c%d"}`, c.calls)
	}
	return json.Unmarshal([]byte(`{"node":{"lintWarnings":`+page+`}}`), r.Data)
}
func TestLintWarningsMalformedPagesStayUnavailable(t *testing.T) {
	for name, page := range map[string]string{"negative total": `{"items":[],"total":-1,"nextCursor":null}`, "nil item": `{"items":[null],"total":1,"nextCursor":null}`, "over total": `{"items":[{"kind":"BROKEN_INCLUDE","text":"x"}],"total":0,"nextCursor":null}`} {
		t.Run(name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Bool("lint-warnings", true, "")
			cmd.SetContext(context.Background())
			c := &lintWarningPageClient{pageJSON: page}
			slot := loadLintWarnings(cmd, c, "n")
			if slot == nil || *slot != nil || c.calls != 1 {
				t.Fatalf("malformed answer looked complete: %#v calls%d", slot, c.calls)
			}
		})
	}
}
func TestLintWarningsRequestCapStopsEndlessPages(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("lint-warnings", true, "")
	cmd.SetContext(context.Background())
	c := &lintWarningPageClient{endless: true}
	slot := loadLintWarnings(cmd, c, "n")
	if slot == nil || *slot != nil || c.calls != lintWarningMaxRequests {
		t.Fatalf("cap not enforced: %#v calls%d", slot, c.calls)
	}
}
func TestLintWarningsUnavailableClassOnce(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("lint-warnings", true, "")
	cmd.SetContext(context.Background())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	c := &lintBudgetClient{t: t}
	for _, ref := range []string{"first", "second"} {
		loadLintWarnings(cmd, c, ref)
	}
	if stderr.String() != "lint warnings: unavailable (advisory read did not complete)\n" {
		t.Fatalf("stderr class=%q", stderr.String())
	}
}
