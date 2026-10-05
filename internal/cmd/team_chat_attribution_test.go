package cmd

import (
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const missingReadSuppression = `{"errors":[{"message":"Unknown argument \"advanceReadState\" on field \"Query.teamChatMessages\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

func TestTeamChatReadAttributesWithoutAcknowledgingExcludedReads(t *testing.T) {
	for _, args := range [][]string{nil, {"--mentions", "Ada"}, {"--before", "3"}, {"--since", "20"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, chatReadResponses())
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "chat", "read", "--json", "--server", srv.URL}, args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			attributed := 0
			for _, c := range *calls {
				if c.Op == "MarkOwnTeamChatRead" {
					t.Fatal("excluded read acknowledged")
				}
				if c.Op == "TeamChatMessages" || c.Op == "TeamChatReadHead" {
					attributed++
					if c.Session != "s-new" || !strings.HasSuffix(c.RawOp, "Attributed") || !strings.Contains(c.Query, "advanceReadState: false") {
						t.Fatalf("unsafe read %+v", c)
					}
				} else if c.Session != "" {
					t.Fatalf("diagnostic attributed %+v", c)
				}
			}
			if attributed != 2 {
				t.Fatalf("attributed %d requests", attributed)
			}
		})
	}
}

func TestTeamChatReadSuppressionFallbackIsHeaderlessAndCommandLocal(t *testing.T) {
	for _, op := range []string{"TeamChatReadHeadAttributed", "TeamChatMessagesAttributed"} {
		t.Run(op, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r[op] = missingReadSuppression
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			rejected := false
			legacy := 0
			for _, c := range *calls {
				if c.RawOp == op {
					rejected = true
					continue
				}
				if c.Op == "TeamChatMessages" || c.Op == "TeamChatReadHead" {
					if rejected {
						legacy++
						if c.Session != "" || strings.Contains(c.Query, "advanceReadState") {
							t.Fatalf("unsafe fallback %+v", c)
						}
					}
				}
			}
			if !rejected || legacy == 0 {
				t.Fatalf("missing fallback %+v", *calls)
			}
		})
	}
}

func TestTeamChatReadRebindDuringHeadStopsAttribution(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, chatReadResponses(), func(op string) {
		if op == "TeamChatReadHead" {
			p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "s-new", "s-other")), 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "TeamChatMessages" && c.Session != "" {
			t.Fatalf("old session used after rebind %+v", c)
		}
	}
}

func TestTeamChatReadNeverRetriesOtherAttributionErrors(t *testing.T) {
	for _, op := range []string{"TeamChatReadHeadAttributed", "TeamChatMessagesAttributed"} {
		for _, body := range []string{gqlErrorJSON("FORBIDDEN"), strings.ReplaceAll(missingReadSuppression, "Query.teamChatMessages", "Query.channelMessages"), strings.ReplaceAll(missingReadSuppression, "advanceReadState", "other")} {
			t.Run(op+body, func(t *testing.T) {
				writeTeamBinding(t)
				r := chatReadResponses()
				r[op] = body
				srv, calls := attnServer(t, r)
				setTeamBindingServer(t, srv.URL)
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
				if err := root.Execute(); err == nil {
					t.Fatal("refusal masked")
				}
				for _, c := range *calls {
					if c.Op == "MarkOwnTeamChatRead" || c.RawOp == "TeamChatReadHead" || c.RawOp == "TeamChatMessages" {
						t.Fatalf("unexpected retry/mark %+v", c)
					}
				}
			})
		}
	}
}

func TestTeamChatReadOutsideBindingScopeStaysHeaderless(t *testing.T) {
	for _, kind := range []string{"unbound", "other deployment", "other App", "no session"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "unbound" {
				teamGitDir(t)
			} else {
				writeTeamBinding(t)
			}
			r := chatReadResponses()
			if kind == "other App" {
				r["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp200000000000000000000")
			}
			srv, calls := attnServer(t, r)
			if kind == "other deployment" {
				setTeamBindingServer(t, "https://different.example.test")
			}
			if kind != "unbound" && kind != "other deployment" {
				setTeamBindingServer(t, srv.URL)
			}
			if kind == "no session" {
				p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "s-new", "")), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "chat", "read", "--json", "--server", srv.URL}
			if kind == "unbound" || kind == "other App" {
				args = append(args, "--app", "capp200000000000000000000")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.Session != "" {
					t.Fatalf("out-of-scope attribution %+v", c)
				}
			}
		})
	}
}

func TestTeamChatReadAllAttributesEveryPageWithoutEarlyMark(t *testing.T) {
	writeTeamBinding(t)
	seqs := make([]int, 200)
	for i := range seqs {
		seqs[i] = i + 1
	}
	srv, seen := chatServer(t, teamChatPage(201, seqs...), teamChatPage(201, 201))
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--all", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 2 {
		t.Fatalf("pages=%d", len(*seen))
	}
	for _, p := range *seen {
		if p.Session != "s-new" || !strings.Contains(p.Query, "advanceReadState: false") {
			t.Fatalf("unsafe page %+v", p)
		}
	}
}
