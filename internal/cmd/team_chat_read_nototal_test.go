package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1470 (server#2980): `chat read` never reads `total`, and selecting it
// forces the server to count every message in the range — a whole-history scan
// on the unbounded tail read behind the standing `--mentions-me --limit 1`
// check. Every page and thread document the read sends must leave it out.
func TestTeamChatReadDocumentsDoNotSelectTotal(t *testing.T) {
	cases := map[string][]string{
		"tail mentions": {"--mentions", "Ada", "--limit", "1"},
		"forward --all": {"--since", "0", "--all"},
		"thread":        {"--thread", "1", "--since", "0"},
	}
	for _, attributed := range []bool{true, false} {
		for name, args := range cases {
			t.Run(map[bool]string{true: "attributed ", false: "legacy "}[attributed]+name, func(t *testing.T) {
				teamGitDir(t)
				r := chatReadResponses()
				r["TeamChatThread"] = r["TeamChatMessages"]
				r["TeamChatReadHead"] = `{"data":{"teamChatMessages":{"total":2,"items":[{"seq":2}]}}}`
				if attributed {
					writeTeamBinding(t)
				}
				srv, calls := attnServer(t, r)
				if attributed {
					setTeamBindingServer(t, srv.URL)
				}
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL}, args...))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				pages := 0
				for _, c := range *calls {
					if c.Op != "TeamChatMessages" && c.Op != "TeamChatThread" {
						continue
					}
					pages++
					if attributed != strings.HasSuffix(c.RawOp, "Attributed") {
						t.Fatalf("path mismatch: attributed=%v op=%s", attributed, c.RawOp)
					}
					if strings.Contains(c.Query, "total") {
						t.Errorf("%s selects total:\n%s", c.RawOp, c.Query)
					}
				}
				if pages == 0 {
					t.Fatal("no page request observed")
				}
			})
		}
	}
}

// The session watermark summary prints exact unread/mention counts from a
// bounded range (sinceSeq = watermark), so its document keeps `total`.
func TestWatermarkSummaryDocumentStillSelectsTotal(t *testing.T) {
	if !strings.Contains(gen.TeamChatMessages_Operation, "total") {
		t.Fatal("TeamChatMessages must keep total for the session watermark summary")
	}
}
