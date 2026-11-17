package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisposeFindingBinding1585(t *testing.T) {
	for _, mode := range []string{"bound", "unbound", "other-App", "foreign-server"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "unbound" {
				teamGitDir(t)
			} else {
				writeTeamBinding(t)
			}
			responses := map[string]string{"AgentTaskDisposeFinding": dispositionResponse1556("fixed", nil)}
			responses["TeamAppIdentity"] = teamAppIdentityJSON
			if mode == "other-App" {
				responses["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp200000000000000000000")
			}
			srv, calls := attnServer(t, responses)
			if mode != "unbound" {
				setTeamBindingServer(t, srv.URL)
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			server := srv.URL
			if mode == "foreign-server" {
				setTeamBindingServer(t, "https://elsewhere.example.test")
			}
			app := "capp100000000000000000000"
			if mode == "other-App" {
				app = "capp200000000000000000000"
			}
			root.SetArgs([]string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json", "--server", server, "--app", app})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := ""
			if mode == "bound" {
				want = "s-new"
			}
			n := 0
			for _, call := range *calls {
				if call.Op == "AgentTaskDisposeFinding" {
					n++
					if call.Session != want {
						t.Errorf("mutation session=%q want=%q", call.Session, want)
					}
				} else if call.Session != "" {
					t.Errorf("lookup attributed: %+v", call)
				}
			}
			if n != 1 {
				t.Fatalf("mutation count=%d calls=%+v", n, *calls)
			}
			cmd, _, err := root.Find([]string{"team", "task", "dispose-finding"})
			if err != nil {
				t.Fatal(err)
			}
			if cmd.Flags().Lookup("as-person") != nil {
				t.Fatal("new as-person flag requires an explicit contract test")
			}
		})
	}
}

func TestAdditionalTaskWriteBindings1585(t *testing.T) {
	for _, op := range []string{"AgentTaskClone", "AgentTaskCreateBatch", "AgentTaskUpdate"} {
		for _, mode := range []string{"bound", "unbound", "other-App"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				if mode == "unbound" {
					teamGitDir(t)
				} else {
					writeTeamBinding(t)
				}
				responses := taskResponses()
				responses["AgentTaskClone"] = `{"data":{"cloneAgentTask":` + taskFieldsJSON + `}}`
				responses["AgentTaskUpdate"] = `{"data":{"updateAgentTask":` + taskFieldsJSON + `}}`
				responses["AgentTaskCreateBatch"] = `{"data":{"createAgentTasks":[` + taskFieldsJSON + `]}}`
				responses["TeamAppIdentity"] = teamAppIdentityJSON
				if mode == "other-App" {
					responses["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp200000000000000000000")
				}
				srv, calls := attnServer(t, responses)
				if mode != "unbound" {
					setTeamBindingServer(t, srv.URL)
				}
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "task"}
				switch op {
				case "AgentTaskClone":
					args = append(args, "clone", "task1")
				case "AgentTaskUpdate":
					args = append(args, "update", "task1", "--title", "Edited")
				case "AgentTaskCreateBatch":
					path := filepath.Join(t.TempDir(), "batch.json")
					if err := os.WriteFile(path, []byte(`[{"task":{"title":"Batch"}}]`), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "create", "--batch-file", path)
				}
				app := "capp100000000000000000000"
				if mode == "other-App" {
					app = "capp200000000000000000000"
				}
				root.SetArgs(append(args, "--server", srv.URL, "--app", app, "--json"))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				want := ""
				if mode == "bound" {
					want = "s-new"
				}
				n := 0
				for _, call := range *calls {
					if call.Op == op {
						n++
						if call.Session != want {
							t.Errorf("session=%q want=%q", call.Session, want)
						}
					} else if call.Session != "" {
						t.Errorf("lookup attributed: %+v", call)
					}
				}
				if n != 1 {
					t.Fatalf("%s count=%d", op, n)
				}
			})
		}
	}
}
