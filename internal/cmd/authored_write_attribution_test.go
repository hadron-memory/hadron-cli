package cmd

import (
	"context"
	"encoding/json"
	"github.com/Khan/genqlient/graphql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

func TestAuthoredNodeCreateCarriesOnlyVerifiedWorkerBinding(t *testing.T) {
	for _, mode := range []string{"bound", "unbound", "different deployment", "missing deployment", "different App", "same App URN", "missing worker"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "unbound" {
				teamGitDir(t)
			} else {
				writeTeamBinding(t)
			}
			r := map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`, "TeamAppIdentity": teamAppIdentityJSON}
			if mode == "different App" {
				r["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp200000000000000000000")
			}
			srv, calls := attnServer(t, r)
			if mode != "unbound" && mode != "missing deployment" {
				setTeamBindingServer(t, srv.URL)
			}
			if mode == "different deployment" || mode == "missing worker" {
				p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				_ = json.Unmarshal(b, &obj)
				if mode == "different deployment" {
					obj["server"] = "https://elsewhere.example.test"
				} else {
					obj["workerId"] = ""
				}
				b, _ = json.Marshal(obj)
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "create", "-m", "acme.com:kb", "--loc", "test:authored", "--name", "Authored", "--server", srv.URL, "--json"}
			switch mode {
			case "different App":
				args = append(args, "--app", "capp200000000000000000000")
			case "same App URN":
				args = append(args, "--app", "hrn:app:acme.com:team")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := ""
			if mode == "bound" || mode == "same App URN" {
				want = "s-new"
			}
			seen := false
			for _, c := range *calls {
				if c.Op == "CreateNode" {
					seen = true
					if c.Session != want {
						t.Fatalf("session=%q want=%q", c.Session, want)
					}
				} else if c.Session != "" {
					t.Fatalf("lookup attributed %+v", c)
				}
			}
			if !seen {
				t.Fatal("no authored request")
			}
		})
	}
}

func TestAuthoredWriteRechecksAfterLookupAndAcrossWrites(t *testing.T) {
	for _, mode := range []string{"rebind at lookup", "end at lookup", "rebind after write", "App rewrite", "deployment rewrite"} {
		t.Run(mode, func(t *testing.T) {
			writeTeamBinding(t)
			r := map[string]string{"GetNode": `{"data":{"node":` + nodeDetailJSON + `}}`, "UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`}
			writes := 0
			srv, calls := attnServerHook(t, r, func(op string) {
				change := op == "GetNode" && mode != "rebind after write"
				if op == "UpdateNode" {
					writes++
					if mode == "rebind after write" && writes == 1 {
						change = true
					}
				}
				if !change {
					return
				}
				p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
				if mode == "end at lookup" {
					if err := os.Remove(p); err != nil {
						t.Fatal(err)
					}
					return
				}
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				_ = json.Unmarshal(b, &obj)
				switch mode {
				case "App rewrite":
					obj["appId"] = "another-app"
				case "deployment rewrite":
					obj["server"] = "https://elsewhere.example.test"
				default:
					obj["sessionId"] = "replacement"
				}
				b, _ = json.Marshal(obj)
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			})
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			_ = NewRootCmd(f)
			f.ServerFlag = srv.URL
			client, err := f.GraphQLClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := gen.GetNode(ctx, client, "n1"); err != nil {
				t.Fatal(err)
			}
			input := &gen.UpdateNodeInput{Id: writeTestPtr("n1"), Name: writeTestPtr("Edited")}
			for i := 0; i < 2; i++ {
				if _, err := gen.UpdateNode(ctx, client, input); err != nil {
					t.Fatal(err)
				}
			}
			for _, c := range *calls {
				if c.Op == "GetNode" && c.Session != "" {
					t.Fatal("lookup attributed")
				}
				if c.Op == "UpdateNode" {
					want := ""
					if mode == "rebind after write" {
						want = "s-new"
						mode = "second write"
					}
					if c.Session != want {
						t.Fatalf("session=%q want=%q calls=%+v", c.Session, want, *calls)
					}
				}
			}
		})
	}
}

func TestAuthoredWriteDoesNotReplayRefusedMutation(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"CreateNode": gqlErrorJSON("FORBIDDEN")})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "create", "-m", "acme.com:kb", "--loc", "x", "--name", "X", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("refusal hidden")
	}
	if len(*calls) != 1 || (*calls)[0].Session != "s-new" {
		t.Fatalf("mutation retried or unattributed %+v", *calls)
	}
}

func TestAuthoredWriteRawAPIStaysHeaderless(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"": `{"data":{"ok":true}}`})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", `mutation RawMutation { createNode(input:{memoryId:"m",loc:"x",name:"x"}){id} }`, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("raw calls=%d", len(*calls))
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatal("raw request attributed")
		}
	}
}

func writeTestPtr(s string) *string { return &s }

func TestAuthoredEdgeWriteAndMaintenanceHaveDifferentContexts(t *testing.T) {
	writeTeamBinding(t)
	responses := map[string]string{"CreateEdge": `{"data":{}}`, "ApproveNode": `{"data":{}}`, "MintMemoryNodes": `{"data":{}}`}
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	_ = NewRootCmd(f)
	f.ServerFlag = srv.URL
	client, err := f.GraphQLClient()
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*graphql.Request{
		{OpName: "CreateEdge", Query: gen.CreateEdge_Operation},
		{OpName: "ApproveNode", Query: gen.ApproveNode_Operation},
		{OpName: "MintMemoryNodes", Query: gen.MintMemoryNodes_Operation},
	} {
		if err := client.MakeRequest(context.Background(), req, &graphql.Response{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range *calls {
		want := ""
		if c.Op == "CreateEdge" {
			want = "s-new"
		}
		if c.Session != want {
			t.Fatalf("%s session=%q want=%q", c.Op, c.Session, want)
		}
	}
}

func TestAuthoredWriteRebindDuringAppIdentityLookupStaysHeaderless(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`, "TeamAppIdentity": teamAppIdentityJSON}, func(op string) {
		if op == "TeamAppIdentity" {
			p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "s-new", "replacement")), 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "create", "-m", "acme.com:kb", "--loc", "x", "--name", "X", "--app", "hrn:app:acme.com:team", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls=%+v", *calls)
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatalf("lookup raced a rebind %+v", c)
		}
	}
}

func TestAuthoredWriteCorruptBindingIsUnattributedWithOneDiagnostic(t *testing.T) {
	writeTeamBinding(t)
	p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	if err := os.WriteFile(p, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	srv, calls := attnServer(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`})
	f, _ := testFactory(t)
	var diagnostic strings.Builder
	f.IOStreams.ErrOut = &diagnostic
	_ = NewRootCmd(f)
	f.ServerFlag = srv.URL
	client, err := f.GraphQLClient()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := gen.CreateNode(context.Background(), client, &gen.CreateNodeInput{MemoryId: "m1", Loc: "x", Name: "X"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(*calls) != 2 || strings.Count(diagnostic.String(), "worker attribution unavailable") != 1 {
		t.Fatalf("calls=%+v stderr=%s", *calls, diagnostic.String())
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatal("corrupt binding attributed")
		}
	}
}

// noWorktreeCwd puts the process in a directory that is not a git worktree and
// clears the git-dir override, so the binding read fails with errNoWorktree
// (cli#1855).
func noWorktreeCwd(t *testing.T) {
	t.Helper()
	t.Setenv(team.GitDirEnv, "")
	t.Setenv("GIT_DIR", "")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	t.Chdir(t.TempDir())
}

func twoAuthoredCreates(t *testing.T, f interface {
	GraphQLClient() (graphql.Client, error)
}) {
	t.Helper()
	client, err := f.GraphQLClient()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := gen.CreateNode(context.Background(), client, &gen.CreateNodeInput{MemoryId: "m1", Loc: "x", Name: "X"}); err != nil {
			t.Fatal(err)
		}
	}
}

// cli#1855: an authored write from a cwd with no worktree at all goes out as
// the signed-in person; say so once on stderr, never refuse, never attribute.
func TestAuthoredWriteNoWorktreePrintsOneNoteAndStillWrites(t *testing.T) {
	noWorktreeCwd(t)
	srv, calls := attnServer(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`})
	f, _ := testFactory(t)
	var diagnostic strings.Builder
	f.IOStreams.ErrOut = &diagnostic
	_ = NewRootCmd(f)
	f.ServerFlag = srv.URL
	twoAuthoredCreates(t, f)
	if len(*calls) != 2 {
		t.Fatalf("write was blocked or repeated: %+v", *calls)
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatalf("no-worktree write attributed: %+v", c)
		}
	}
	out := diagnostic.String()
	if strings.Count(out, "worker attribution unavailable") != 1 || !strings.Contains(out, "not inside a git worktree") || !strings.Contains(out, "signed-in person") {
		t.Fatalf("want one note naming the no-worktree cause and the person fallback, stderr=%q", out)
	}
}

// The bound-worktree path stays silent and attributed.
func TestAuthoredWriteBoundWorktreePrintsNoNote(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`, "TeamAppIdentity": teamAppIdentityJSON})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	var diagnostic strings.Builder
	f.IOStreams.ErrOut = &diagnostic
	_ = NewRootCmd(f)
	f.ServerFlag = srv.URL
	twoAuthoredCreates(t, f)
	if diagnostic.Len() != 0 {
		t.Fatalf("bound worktree printed a note: %q", diagnostic.String())
	}
	for _, c := range *calls {
		if c.Op == "CreateNode" && c.Session != "s-new" {
			t.Fatalf("bound write lost its session: %+v", c)
		}
	}
}

// cli#1855 acceptance, end to end: `team task finish` from a cwd with no
// worktree prints the note once on stderr (stdout untouched), still sends the
// finish with no session, and the server's answer is what the caller gets.
func TestTeamTaskFinishNoWorktreePrintsNoteAndSendsHeaderless(t *testing.T) {
	noWorktreeCwd(t)
	srv, calls := attnServer(t, taskResponses())
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "task", "finish", "task1", "--outcome", "succeeded", "--summary", "s", "--evidence", "https://ci.acme.com/1"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	sent := false
	for _, c := range *calls {
		if c.Op == "AgentTaskFinish" {
			sent = true
			if c.Session != "" {
				t.Fatalf("attributed: %q", c.Session)
			}
		}
	}
	if !sent {
		t.Fatal("finish was not sent: the note must never refuse")
	}
	if n := strings.Count(stderr.String(), "worker attribution unavailable"); n != 1 {
		t.Fatalf("want one note, got %d: %q", n, stderr.String())
	}
	if strings.Contains(out.String(), "attribution") {
		t.Fatalf("note leaked to stdout: %q", out.String())
	}
}

// The FIT boundary (cor:agt:020:03): a chat post from a cwd with no worktree
// keeps its typed refusal, sends nothing, and the new note does not replace it.
func TestTeamChatPostNoWorktreeStillRefusesWithoutTheNote(t *testing.T) {
	noWorktreeCwd(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
		"TeamAppIdentity":       teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hi", "--server", gql.URL, "--app", "acme.com:eng-team"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--as-me") {
		t.Fatalf("post must keep its refusal naming --as-me: %v", err)
	}
	if _, posted := captured["CreateTeamChatMessage"]; posted {
		t.Fatal("an unbound post reached the server")
	}
	if strings.Contains(stderr.String(), "worker attribution unavailable") {
		t.Fatalf("the note replaced/accompanied the refusal: %q", stderr.String())
	}
}
