package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

func availabilityArgs2450(kind string, extra ...string) []string {
	target := "Rufus"
	if kind == "family" {
		target = "codex"
	}
	return append([]string{"team", "availability", "set", "--" + kind, target, "--state", "unavailable", "--reason", "OTHER"}, extra...)
}
func availabilityOp2450(kind string) string {
	if kind == "family" {
		return "ModelFamilyAvailabilitySet"
	}
	return "WorkerAvailabilitySet"
}
func availabilityAnswer2450(kind string) string { return taskResponses()[availabilityOp2450(kind)] }

func TestAvailabilityInstruction2450Wire(t *testing.T) {
	for _, kind := range []string{"worker", "family"} {
		t.Run(kind, func(t *testing.T) {
			var plainOut string
			var plainInput any
			for _, claimed := range []bool{false, true} {
				teamGitDir(t)
				op := availabilityOp2450(kind)
				if claimed {
					op += "Instructed"
				}
				srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
					if c.OperationName == "GetBoundSessionEnd" {
						return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
					}
					if c.OperationName != op {
						t.Errorf("op %s want %s", c.OperationName, op)
					}
					if claimed {
						want := map[string]any{"kind": "LIVE_CHAT", "ref": "client: 7 "}
						if !reflect.DeepEqual(c.Variables["instruction"], want) || c.Session != "s-new" || !strings.Contains(c.Query, "instruction: $instruction") {
							t.Errorf("lost claim/session: %+v", c)
						}
					} else {
						wantSession := ""
						if kind == "worker" {
							wantSession = "s-new"
						}
						wantQuery := gen.ModelFamilyAvailabilitySet_Operation
						if kind == "worker" {
							wantQuery = gen.WorkerAvailabilitySet_Operation
						}
						if c.Session != wantSession || c.Query != wantQuery || len(c.Variables) != 1 {
							t.Errorf("plain request changed: %+v", c)
						}
						plainInput = c.Variables["input"]
					}
					return availabilityAnswer2450(kind)
				})
				writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := availabilityArgs2450(kind, "--server", srv.URL, "--json")
				if claimed {
					args = append(args, "--instruction", "live-chat:client: 7 ")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				var writes []snapshotCall1614
				for _, c := range *calls {
					if strings.Contains(c.OperationName, "AvailabilitySet") {
						writes = append(writes, c)
					} else if c.Session != "" {
						t.Error("session on lookup")
					}
				}
				if len(writes) != 1 {
					t.Fatalf("calls: %+v", *calls)
				}
				if claimed {
					if !reflect.DeepEqual(writes[0].Variables["input"], plainInput) || out.String() != plainOut {
						t.Fatal("input/JSON changed apart from instruction")
					}
				} else {
					plainOut = out.String()
				}
			}
		})
	}
}

func TestAvailabilityInstruction2450UsageBeforeRequests(t *testing.T) {
	for _, kind := range []string{"worker", "family"} {
		for _, bad := range []string{"", "task:", "unknown:ref"} {
			t.Run(kind+bad, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(snapshotCall1614) string { t.Error("unexpected request"); return `{}` })
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(availabilityArgs2450(kind, "--instruction", bad, "--server", srv.URL, "--app", "capp100000000000000000000"))
				err := root.Execute()
				if exitCodeFor(err) != 2 || strings.Contains(fmt.Sprint(err), "unknown flag") || len(*calls) != 0 {
					t.Fatalf("err=%v calls=%v", err, *calls)
				}
			})
		}
	}
}

func TestAvailabilityInstruction2450FallbackAndRefusal(t *testing.T) {
	for _, kind := range []string{"worker", "family"} {
		for _, mode := range []string{"none", "wrong-server", "incomplete", "unreadable", "changed", "refused"} {
			t.Run(kind+mode, func(t *testing.T) {
				dir := teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
					if c.OperationName == "GetBoundSessionEnd" {
						return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
					}
					if c.OperationName == "TeamAppIdentity" {
						if c.Session != "" || c.Variables["instruction"] != nil {
							t.Error("lookup carried attribution")
						}
						if mode == "changed" {
							writeTeamBindingIn(t, dir, "http://changed.invalid")
						}
						return `{"data":{"app":{"id":"capp100000000000000000000"}}}`
					}
					if c.OperationName != availabilityOp2450(kind)+"Instructed" {
						t.Errorf("wrong mutation: %s", c.OperationName)
					}
					expected := ""
					if mode == "refused" {
						expected = "s-new"
					}
					if c.Session != expected || c.Variables["instruction"] == nil {
						t.Errorf("bad fallback %+v", c)
					}
					if mode == "refused" {
						return memberGQLError("instruction refused once", "AVAILABILITY_NOT_PERMITTED")
					}
					return availabilityAnswer2450(kind)
				})
				if mode != "none" {
					writeTeamBindingIn(t, dir, srv.URL)
				}
				switch mode {
				case "wrong-server":
					writeTeamBindingIn(t, dir, "http://other.invalid")
				case "incomplete":
					rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "" })
				case "unreadable":
					if err := os.WriteFile(dir+"/hadron-team-session.json", []byte("broken"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				f, out, stderr := testFactoryTTY(t, "")
				root := NewRootCmd(f)
				app := "capp100000000000000000000"
				if mode == "changed" {
					app = "acme.com:eng"
				}
				root.SetArgs(availabilityArgs2450(kind, "--app", app, "--instruction", "task:one", "--server", srv.URL, "--json"))
				err := root.Execute()
				if mode == "refused" {
					if exitCodeFor(err) != 8 || out.Len() != 0 || !strings.Contains(fmt.Sprint(err), "instruction refused once") {
						t.Fatalf("err %v out %s", err, out.String())
					}
					renderError(f, err)
					var env struct {
						Error struct {
							ServerCode string `json:"serverCode"`
						}
					}
					if e := json.Unmarshal([]byte(out.String()+stderr.String()), &env); e != nil || env.Error.ServerCode != "AVAILABILITY_NOT_PERMITTED" {
						t.Fatalf("lost code %v %+v", e, env)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, c := range *calls {
					if strings.Contains(c.OperationName, "AvailabilitySet") {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("want one write, got %v", *calls)
				}
				if mode != "none" && mode != "refused" && !strings.Contains(stderr.String(), "worker session is not used") {
					t.Fatalf("missing fallback note: %s", stderr.String())
				}
			})
		}
	}
}

func TestAvailabilityInstruction2450UnknownOutcome(t *testing.T) {
	for _, kind := range []string{"worker", "family"} {
		for _, body := range []string{`{"data":{}}`, `{"data":{"setWorkerAvailability":null,"setModelFamilyAvailability":null}}`} {
			t.Run(kind+body, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(snapshotCall1614) string { return body })
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(availabilityArgs2450(kind, "--app", "capp100000000000000000000", "--instruction", "task:one", "--server", srv.URL, "--json"))
				err := root.Execute()
				if exitCodeFor(err) != 1 || len(*calls) != 1 || out.Len() != 0 || !strings.Contains(fmt.Sprint(err), "outcome unknown") {
					t.Fatalf("err=%v calls=%d out=%s", err, len(*calls), out.String())
				}
			})
		}
	}
}
func TestAvailabilityInstruction2450UnboundDirectWorkerNeedsNoApp(t *testing.T) {
	teamGitDir(t)
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
		if c.Session != "" || c.Variables["instruction"] == nil {
			t.Errorf("bad attribution %+v", c)
		}
		input := c.Variables["input"].(map[string]any)
		if input["appRef"] != nil {
			t.Errorf("invented scope %v", input)
		}
		return availabilityAnswer2450("worker")
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set", "--worker", "hrn:worker:acme.com:eng:iris", "--state", "unavailable", "--reason", "OTHER", "--instruction", "task:one", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatal(*calls)
	}
}

func TestAvailabilityInstruction2450Help(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "availability", "set", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Without --instruction", "signed-in person", "With --instruction", "App-manager", "server decides", "ba077810", "--instruction"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing help %q", want)
		}
	}
	if strings.Contains(out.String(), "always made as the person") || strings.Contains(out.String(), "17ef5905") {
		t.Fatal("old family advice remains")
	}
}
