package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

const registerRows1577 = `[{"code":"z.mapped","state":"MAPPED","actionUrn":"hrn:action:acme.com:team:z.mapped","entryUrn":"hrn:node:acme.com:team:register:z.mapped","definedBy":"hrn:agent:acme.com:team","taskLoc":"tasks:finish","taskUrn":"hrn:node:acme.com:coder:tasks:finish"},{"code":"a.unresolved","state":"UNRESOLVED","actionUrn":"action","entryUrn":"entry","definedBy":"agent","taskLoc":"tasks:missing","taskUrn":null},{"code":"b.unmapped","state":"UNMAPPED","actionUrn":null,"entryUrn":null,"definedBy":null,"taskLoc":null,"taskUrn":null}]`

func registerResponses1577(items string, total int, more bool) map[string]string {
	r := taskResponses()
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"actionRegisterIndex": map[string]any{"entries": json.RawMessage(items), "total": total, "hasMore": more}}})
	r["TeamActionRegisterIndex"] = string(b)
	return r
}

func TestTeamRegister1577WireAndJSON(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		args := []string{"team", "register", "--app", "hrn:app:acme.com:eng", "--json"}
		want := map[string]any{"appRef": "hrn:app:acme.com:eng"}
		if explicit {
			args = append(args, "--agent", "hrn:agent:acme.com:coder", "--task-loc", "tasks:finish", "--code", "z.mapped", "--limit", "201", "--offset", "3")
			want["agentRef"] = "hrn:agent:acme.com:coder"
			want["taskLoc"] = "tasks:finish"
			want["code"] = "z.mapped"
			want["limit"] = float64(201)
			want["offset"] = float64(3)
		}
		calls, out, err := runTeamTask(t, registerResponses1577(registerRows1577, 9, true), args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || (*calls)[0].Op != "TeamActionRegisterIndex" {
			t.Fatalf("calls %#v", *calls)
		}
		a, _ := json.Marshal((*calls)[0].Vars)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Fatalf("vars %s want %s", a, b)
		}
		var page map[string]json.RawMessage
		if err = json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		if len(page) != 3 || string(page["total"]) != "9" || string(page["hasMore"]) != "true" {
			t.Fatalf("page %s", out)
		}
		var rows []map[string]any
		if err = json.Unmarshal(page["entries"], &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 || len(rows[2]) != 7 || rows[2]["code"] != "b.unmapped" || rows[2]["taskLoc"] != nil {
			t.Fatalf("rows %s", out)
		}
	}
}

func TestTeamRegister1577TextPagingEmptyAndCount(t *testing.T) {
	_, out, err := runTeamTask(t, registerResponses1577(registerRows1577, 9, true), "team", "register")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"z.mapped", "MAPPED", "a.unresolved", "UNRESOLVED", "b.unmapped", "UNMAPPED", "no runnable task found at tasks:missing", "entry is malformed", "9", "more: true"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q: %s", s, out)
		}
	}
	calls, out, err := runTeamTask(t, registerResponses1577(`[]`, 7, true), "team", "register", "--limit", "0", "--offset", "0")
	if err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].Vars["limit"] != float64(0) || (*calls)[0].Vars["offset"] != float64(0) || !strings.Contains(out, "7") {
		t.Fatalf("count %s %#v", out, *calls)
	}
	_, out, err = runTeamTask(t, registerResponses1577(`[]`, 0, false), "team", "register", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Items []any `json:"entries"`
	}
	if json.Unmarshal([]byte(out), &p) != nil || p.Items == nil {
		t.Fatalf("empty %s", out)
	}
	_, out, err = runTeamTask(t, registerResponses1577(`[]`, 0, false), "team", "register")
	if err != nil || !strings.Contains(out, "no register entries returned") {
		t.Fatalf("empty %v %s", err, out)
	}
}

func TestTeamRegister1577RefusalsAndBadFlags(t *testing.T) {
	for code, exit := range map[string]int{"APP_NOT_FOUND": 4, "AGENT_NOT_FOUND": 4, "BAD_USER_INPUT": 2} {
		r := registerResponses1577(`[]`, 0, false)
		r["TeamActionRegisterIndex"] = `{"errors":[{"message":"server refusal text","extensions":{"code":"` + code + `"}}]}`
		calls, out, err := runTeamTask(t, r, "team", "register", "--json")
		if err == nil || exitCodeFor(err) != exit || !strings.Contains(err.Error(), "server refusal text") || !strings.Contains(err.Error(), code) || out != "" || len(*calls) != 1 {
			t.Fatalf("%s: %v %s %#v", code, err, out, *calls)
		}
	}
	for _, flags := range [][]string{{"--limit", "-1"}, {"--offset", "-1"}, {"--agent="}, {"--task-loc="}, {"--code="}} {
		args := append([]string{"team", "register"}, flags...)
		calls, _, err := runTeamTask(t, registerResponses1577(`[]`, 0, false), args...)
		if err == nil || exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("%v: %v %#v", flags, err, *calls)
		}
	}
}

func TestTeamRegister1577SafeTextOriginalJSON(t *testing.T) {
	rows := strings.ReplaceAll(registerRows1577, "z.mapped", `bad\u001b[31m\ncode`)
	for _, js := range []bool{false, true} {
		args := []string{"team", "register"}
		if js {
			args = append(args, "--json")
		}
		_, out, err := runTeamTask(t, registerResponses1577(rows, 3, false), args...)
		if err != nil {
			t.Fatal(err)
		}
		if js {
			if !strings.Contains(out, `\u001b`) {
				t.Fatalf("original lost %s", out)
			}
		} else if strings.Contains(out, "\x1b") || strings.Contains(out, "bad\ncode") {
			t.Fatalf("unsafe %q", out)
		}
	}
}

func TestTeamRegister1577AmbientAndDegradedScope(t *testing.T) {
	for _, degraded := range []bool{false, true} {
		r := registerResponses1577(`[]`, 0, false)
		if degraded {
			r["TeamAppIdentity"] = `{"data":{"app":null}}`
		}
		calls, out, err := runTeamTask(t, r, "team", "register")
		if err != nil || len(*calls) != 2 || !strings.Contains(out, "from the worktree binding") || !strings.HasPrefix(out, "app: ") {
			t.Fatalf("scope %v %s %#v", err, out, *calls)
		}
		if degraded && !strings.Contains(out, "capp100000000000000000000") {
			t.Fatalf("fallback %s", out)
		}
	}
}

func TestTeamRegister1577ErrorHumanAndJSONSink(t *testing.T) {
	for code, want := range map[string]int{"APP_NOT_FOUND": 4, "AGENT_NOT_FOUND": 4, "BAD_USER_INPUT": 2} {
		for _, js := range []bool{false, true} {
			r := registerResponses1577(`[]`, 0, false)
			r["TeamActionRegisterIndex"] = `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
			args := []string{"team", "register"}
			if js {
				args = append(args, "--json")
			}
			_, _, err := runTeamTask(t, r, args...)
			f, out := testFactory(t)
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			got := renderFailure(f, args, err)
			if got != want {
				t.Fatalf("exit %d want %d", got, want)
			}
			// Include both real sink streams; JSON errors may use stdout.
			if js {
				var envelope struct {
					Error struct {
						ServerCode string `json:"serverCode"`
					} `json:"error"`
				}
				if json.Unmarshal([]byte(out.String()), &envelope) != nil || envelope.Error.ServerCode != code {
					t.Fatalf("serverCode lost: %s", out.String())
				}
			}
			if !strings.Contains(out.String()+stderr.String(), code) {
				t.Fatalf("code missing: %s", out.String())
			}
		}
	}
}

func TestTeamRegister1577Document(t *testing.T) {
	q := gen.TeamActionRegisterIndex_Operation
	for _, arg := range []string{"agentRef: $agentRef", "taskLoc: $taskLoc", "code: $code", "limit: $limit", "offset: $offset"} {
		if !strings.Contains(q, arg) {
			t.Fatalf("missing %s in %s", arg, q)
		}
	}
	if strings.Contains(q, " = ") || strings.Contains(q, ": null") || strings.Contains(q, "mutation ") {
		t.Fatalf("changed default or write %s", q)
	}
}

func TestTeamRegister1577SessionAttribution(t *testing.T) {
	for _, mode := range []string{"bound", "unbound", "other-server", "missing-server", "empty-session", "rebound"} {
		t.Run(mode, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := attnServer(t, registerResponses1577(`[]`, 0, false))
			if mode != "missing-server" {
				setTeamBindingServer(t, srv.URL)
			}
			path := filepath.Join(os.Getenv("HADRON_TEAM_GIT_DIR"), "hadron-team-session.json")
			if mode == "unbound" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				text := string(b)
				if mode == "other-server" {
					text = strings.Replace(text, srv.URL, "http://127.0.0.1:1", 1)
				}
				if mode == "empty-session" {
					text = strings.Replace(text, `"sessionId":"s-new"`, `"sessionId":""`, 1)
				}
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, out := testFactory(t)
			if mode == "rebound" {
				t.Setenv("HADRON_TOKEN", "")
				f.TokenStoreFn = func() store.Store {
					body, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					body = []byte(strings.Replace(string(body), `"sessionId":"s-new"`, `"sessionId":"replacement"`, 1))
					if err := os.WriteFile(path, body, 0600); err != nil {
						t.Fatal(err)
					}
					return memStore{auth.Host(srv.URL): "hdr_user_test"}
				}
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "--app", "hrn:app:acme.com:eng", "team", "register", "--json"})
			if err := root.Execute(); err != nil {
				t.Fatalf("%v %s", err, out.String())
			}
			if len(*calls) != 1 {
				t.Fatalf("unexpected calls %#v", *calls)
			}
			want := ""
			if mode == "bound" {
				want = "s-new"
			}
			if (*calls)[0].Session != want {
				t.Fatalf("session %q want %q", (*calls)[0].Session, want)
			}
			if _, ok := (*calls)[0].Vars["agentRef"]; ok {
				t.Fatalf("session attribution must not invent agentRef: %#v", (*calls)[0].Vars)
			}
		})
	}
}
