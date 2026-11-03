package cmd

import (
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
