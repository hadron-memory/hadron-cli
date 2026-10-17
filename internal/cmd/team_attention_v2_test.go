package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTeamAttentionV2Frontiers(t *testing.T) {
	teamGitDir(t)
	for _, frontier := range []string{"42", "null"} {
		t.Run(frontier, func(t *testing.T) {
			response := strings.Replace(attentionJSON, `"lastSeq":43`, `"lastRelevantSeq":`+frontier+`,"lastSeq":43`, 1)
			srv, _ := attnServer(t, map[string]string{"TeamAttentionPage": response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--server", srv.URL, "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto struct {
				Workers []struct{ Channels []map[string]json.RawMessage }
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			c := dto.Workers[0].Channels[0]
			if string(c["lastRelevantSeq"]) != frontier {
				t.Fatalf("frontier: %s, want %s; output %s", c["lastRelevantSeq"], frontier, out.String())
			}
			if string(c["firstUnreadSeq"]) != "41" || string(c["lastSeq"]) != "43" {
				t.Fatalf("frontiers conflated: %v", c)
			}
		})
	}
}

func TestTeamAttentionHumanDoesNotPrintToken(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{"TeamAttentionPage": attentionJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "tok-2") || !strings.Contains(out.String(), "token:") || !strings.Contains(out.String(), "--since") {
		t.Fatalf("token notice: %s", out.String())
	}
}

func TestTeamAttentionV2LaterPageFailureHasNoOutput(t *testing.T) {
	teamGitDir(t)
	for _, tail := range []string{
		`{"errors":[{"message":"later page failed"}]}`,
		`{"data":{"teamAttentionPage":{"scanComplete":false,"nextPage":null,"adoptableSince":null,"items":[]}}}`,
	} {
		t.Run(tail, func(t *testing.T) {
			f, out := testFactory(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Query     string
					Variables map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if !strings.Contains(request.Query, "\n      lastRelevantSeq\n") {
					t.Error("query omits relevant frontier")
				}
				if out.Len() != 0 {
					t.Error("partial report emitted before scan completion")
				}
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					first := strings.Replace(attentionJSON, `"scanComplete":true,"nextPage":null,"adoptableSince":"tok-2"`, `"scanComplete":false,"nextPage":"next","adoptableSince":null`, 1)
					_, _ = w.Write([]byte(first))
				} else {
					if request.Variables["page"] != "next" {
						t.Error("missing continuation")
					}
					_, _ = w.Write([]byte(tail))
				}
			}))
			defer srv.Close()
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--server", srv.URL, "--json"})
			if err := root.Execute(); err == nil {
				t.Fatal("incomplete scan succeeded")
			}
			if calls != 2 || out.Len() != 0 {
				t.Fatalf("calls=%d partial output=%s", calls, out.String())
			}
		})
	}
}

func TestTeamAttentionV2LegacyFrontierIsUnknown(t *testing.T) {
	teamGitDir(t)
	srv, _ := attnServer(t, map[string]string{"TeamAttentionPage": missingFieldJSON("teamAttentionPage"), "TeamAttention": legacyAttentionJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "attention", "--app", "acme.com:eng-team", "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"lastRelevantSeq": null`) {
		t.Fatalf("legacy invented frontier: %s", out.String())
	}
}
