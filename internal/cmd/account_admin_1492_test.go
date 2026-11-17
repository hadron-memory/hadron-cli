package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// cli#1492: pins the #1488 reviewers left as optional P3s. Each one is red
// against a mutant of the guard it protects.

// An ESC [2J (clear screen) in server-stored, partly user-chosen text: the
// target's display name, the admin's display name and the free-text reason.
const esc1492 = "\x1b[2J"

func session1492(id, reason, targetName string) string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return fmt.Sprintf(`{"id":%s,"createdAt":"2026-03-01T00:00:00Z","expiresAt":"2026-03-01T01:00:00Z","endedAt":null,"endedBy":null,"isActive":true,"reason":%s,
 "adminUser":{"id":"a1","handle":null,"name":%s},"targetUser":{"id":"u1","handle":null,"name":%s},"organization":{"id":"o1","urn":"acme.com","name":"Acme"}}`,
		q(id), q(reason), q("Admin"+esc1492+"Name"), q(targetName))
}

// Item 1 (Rico, mutants A2/A3/A4): the ADMIN, TARGET and REASON cells of both
// impersonation tables go through output.SafeCell. Without it, the escape
// reaches the terminal verbatim.
func TestAdminImpersonationTablesSanitize1492(t *testing.T) {
	sess := session1492("s1", "reason"+esc1492+"text", "Target"+esc1492+"Name")
	for name, tc := range map[string]struct {
		op, resp string
		args     []string
	}{
		"list": {"ImpersonationSessions", `{"data":{"impersonationSessions":{"total":1,"items":[` + sess + `]}}}`, []string{"admin", "impersonation", "list"}},
		"get":  {"ImpersonationSession", `{"data":{"impersonationSession":` + sess + `}}`, []string{"admin", "impersonation", "get", "s1"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := exec1344(t, map[string]string{tc.op: tc.resp}, tc.op, tc.args...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if strings.Contains(r.out, "\x1b") {
				t.Fatalf("a terminal escape reached the table:\n%q", r.out)
			}
			// The cells are still shown (sanitized), not dropped.
			for _, want := range []string{"Target", "Admin", "reason"} {
				if !strings.Contains(r.out, want) {
					t.Errorf("%s cell missing from table:\n%s", want, r.out)
				}
			}
			// --json carries the original value (SafeCell is text-mode only).
			j := exec1344(t, map[string]string{tc.op: tc.resp}, tc.op, append(tc.args, "--json")...)
			if j.err != nil || !strings.Contains(j.out, `\u001b[2J`) {
				t.Errorf("--json must keep the server's value: %v %q", j.err, j.out)
			}
		})
	}
}

// Item 2 (Rico, mutant D7): user delete never repeats --reason, in the
// success line, in --json, or on stderr.
func TestUserDeleteNeverEchoesReason1492(t *testing.T) {
	const reason = "GDPR-ticket-7731-private-note"
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonMode), func(t *testing.T) {
			gql, calls := captureGraphQL(t, map[string]string{"DeleteUser": `{"data":{"deleteUser":true}}`})
			f, out := testFactory(t)
			errOut := &strings.Builder{}
			f.IOStreams.ErrOut = errOut
			args := []string{"user", "delete", "--user", "@old", "--reason", reason, "--yes", "--server", gql.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(calls["DeleteUser"]), reason) {
				t.Fatalf("the reason must still be sent: %s", calls["DeleteUser"])
			}
			if strings.TrimSpace(out.String()) == "" {
				t.Fatal("no success output")
			}
			if strings.Contains(out.String(), reason) || strings.Contains(errOut.String(), reason) {
				t.Fatalf("reason echoed: stdout %q stderr %q", out.String(), errOut.String())
			}
		})
	}
}

// Item 3 (Gizelle): the default listing drains every page. A fake with two
// distinct pages pins the offsets sent and that each row appears exactly once,
// so stopping after page one, repeating an offset or dropping later rows fails.
func TestAdminImpersonationListDrainsPages1492(t *testing.T) {
	pages := map[int][]string{
		0: {session1492("s1", "r1", "T1"), session1492("s2", "r2", "T2")},
		2: {session1492("s3", "r3", "T3")},
	}
	var mu sync.Mutex
	var offsets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "ImpersonationSessions" {
			t.Errorf("unexpected operation %q", body.OperationName)
			return
		}
		off := 0
		if body.Variables.Offset != nil {
			off = *body.Variables.Offset
		}
		mu.Lock()
		offsets = append(offsets, off)
		mu.Unlock()
		items, ok := pages[off]
		if !ok {
			items = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"impersonationSessions":{"total":3,"items":[%s]}}}`, strings.Join(items, ","))
	}))
	defer srv.Close()

	for _, jsonMode := range []bool{true, false} {
		t.Run(fmt.Sprintf("json=%v", jsonMode), func(t *testing.T) {
			offsets = nil
			f, out := testFactory(t)
			args := []string{"admin", "impersonation", "list", "--org", "acme.com", "--server", srv.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(offsets) != "[0 2]" {
				t.Errorf("offsets sent = %v, want [0 2]", offsets)
			}
			for _, id := range []string{"s1", "s2", "s3"} {
				needle := id
				if jsonMode {
					needle = `"id": "` + id + `"`
				}
				if n := strings.Count(out.String(), needle); n != 1 {
					t.Errorf("row %s appears %d times, want 1:\n%s", id, n, out.String())
				}
			}
			if jsonMode {
				var rows []map[string]any
				if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 3 {
					t.Fatalf("want 3 rows: %v %s", err, out.String())
				}
			}
		})
	}
}
