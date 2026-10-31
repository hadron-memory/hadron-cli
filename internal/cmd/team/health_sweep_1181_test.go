package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const health1181Status = `[{"contract":"terminal-status@1","worker":"Iris","terminal":"term_fake","status":"locked","prompt":"menu","reason":"menu_or_question","lastTurnAt":null,"checkedAt":"2026-10-31T02:00:00Z","detail":"\u001b]52;c;PRIVATE_SCREEN\u0007 hrn:node:secret Markdown"}]`

func TestHealth1181ContractWhitelist(t *testing.T) {
	rows, err := decodeHealthStatus([]byte(health1181Status))
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "PRIVATE_SCREEN") || strings.Contains(string(raw), "term_fake") || strings.Contains(string(raw), "detail") {
		t.Fatalf("foreign text retained: %s", raw)
	}
	for _, bad := range []string{
		strings.Replace(health1181Status, "terminal-status@1", "terminal-status@99", 1),
		strings.Replace(health1181Status, "menu_or_question", "PRIVATE_SCREEN", 1),
		strings.Replace(health1181Status, "locked", "invented", 1),
		strings.Replace(health1181Status, "2026-10-31T02:00:00Z", "not-a-time", 1),
		`null`,
	} {
		if _, err := decodeHealthStatus([]byte(bad)); err == nil {
			t.Fatalf("accepted malformed contract %s", bad)
		} else if strings.Contains(err.Error(), "PRIVATE_SCREEN") {
			t.Fatalf("raw diagnostic: %v", err)
		}
	}
}

func health1181Run(t *testing.T, wire string, statusCode int) (string, int, map[string]any, error) {
	return health1181RunMode(t, wire, statusCode, true)
}
func health1181RunMode(t *testing.T, wire string, statusCode int, jsonMode bool) (string, int, map[string]any, error) {
	return health1181RunFixture(t, wire, statusCode, jsonMode, "", health1181Status, `{"hostId":"own"}`)
}

const health1181Registrations = `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"w1","worker":{"id":"w1","name":"Iris","urn":"hrn:worker:test:team:iris"},"worktrees":["iris"]}]}]}}`

func health1181RunFixture(t *testing.T, wire string, statusCode int, jsonMode bool, registrations, status, state string) (string, int, map[string]any, error) {
	if registrations == "" {
		registrations = health1181Registrations
	}
	t.Helper()
	t.Setenv("HADRON_TOKEN", "AMBIENT_MUST_NOT_BE_USED")
	dir := t.TempDir()
	key := "SYNTHETIC_HOST_KEY_1181"
	var mu sync.Mutex
	writes := 0
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("wrong authority %q", r.Header.Get("Authorization"))
		}
		var q struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "HostRegistrations":
			_, _ = io.WriteString(w, registrations)
		case "HealthSweepSetTerminals":
			mu.Lock()
			writes++
			sent = q.Variables
			mu.Unlock()
			if wire == "DROP" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			_, _ = io.WriteString(w, wire)
		default:
			t.Errorf("unexpected %s", q.OperationName)
		}
	}))
	defer srv.Close()
	for n, s := range map[string]string{"key": key, "state.json": state} {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [iris]\n", srv.URL, dir)
	path := filepath.Join(dir, "host.yaml")
	if e := os.WriteFile(path, []byte(cfg), 0600); e != nil {
		t.Fatal(e)
	}
	f := cmdutil.NewFactory()
	streams, out, stderr := output.Test()
	f.IOStreams = streams
	f.JSON = jsonMode
	c := newCmdTeamHealthWithReader(f, func(context.Context, string, *hostpoller.Config) ([]byte, int, error) {
		return []byte(status), statusCode, nil
	})
	c.SetArgs([]string{"sweep", "--config", path})
	err := c.Execute()
	all := out.String() + stderr.String()
	if strings.Contains(all, key) || strings.Contains(all, "PRIVATE_SCREEN") {
		t.Fatalf("secret in sink %q", all)
	}
	mu.Lock()
	defer mu.Unlock()
	return out.String(), writes, sent, err
}

func TestHealth1181NoCoordinatorAndLegacyVerdictIgnored(t *testing.T) {
	base := `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"hrn:worker:test:team:iris"}],"refused":[]EXTRA}}}`
	var first string
	for i, x := range []string{"", `,"alarm":[{"secret":"PRIVATE_SCREEN"}],"noCoordinator":true`} {
		out, n, sent, err := health1181Run(t, strings.Replace(base, "EXTRA", x, 1), 0)
		if err != nil || n != 1 {
			t.Fatalf("err=%v writes=%d", err, n)
		}
		raw, _ := json.Marshal(sent)
		if strings.Contains(string(raw), "PRIVATE_SCREEN") || strings.Contains(string(raw), "detail") {
			t.Fatalf("screen in write %s", raw)
		}
		if i == 0 {
			first = out
		} else if out != first {
			t.Fatalf("legacy verdict changes output %q != %q", out, first)
		}
		var got map[string]any
		if json.Unmarshal([]byte(out), &got) != nil || len(got) != 2 || got["reports"] == nil || got["refused"] == nil {
			t.Fatalf("bad DTO %s", out)
		}
	}
}
func TestHealth1181RefusalsAreNotRetried(t *testing.T) {
	out, n, _, err := health1181Run(t, `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"w1","code":"TERMINAL_REPORT_STALE"}]}}}`, 0)
	if err != nil || n != 1 || !strings.Contains(out, "TERMINAL_REPORT_STALE") {
		t.Fatalf("%v %d %s", err, n, out)
	}
}
func TestHealth1181WriteAndStatusExits(t *testing.T) {
	for _, tc := range []struct {
		wire                 string
		status, want, writes int
	}{
		{`{"errors":[{"message":"PRIVATE_SCREEN","extensions":{"code":"UNAUTHENTICATED"}}]}`, 0, 3, 1},
		{`{"errors":[{"message":"PRIVATE_SCREEN","extensions":{"code":"FORBIDDEN"}}]}`, 0, 8, 1},
		{`{"errors":[{"message":"PRIVATE_SCREEN","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, 0, 1, 1},
		{`{}`, 10, 10, 0},
		{`DROP`, 0, 7, 1},
		{`{}`, 3, 3, 0},
		{`{}`, 7, 7, 0},
		{`{}`, 8, 8, 0},
	} {
		out, n, _, err := health1181Run(t, tc.wire, tc.status)
		if exitcode.FromError(err) != tc.want || n != tc.writes {
			t.Fatalf("exit %d want%d writes%d out%s err%v", exitcode.FromError(err), tc.want, n, out, err)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE_SCREEN") {
			t.Fatalf("secret in chain %v", err)
		}
	}
}

func TestHealth1181ExactPayloadAndTextGolden(t *testing.T) {
	wire := `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"hrn:worker:test:team:iris"}],"refused":[]}}}`
	out, n, sent, err := health1181RunMode(t, wire, 0, false)
	if err != nil || n != 1 || out != "Iris  locked  menu_or_question\n" {
		t.Fatalf("text %q err%v n%d", out, err, n)
	}
	want := map[string]any{"input": map[string]any{"appRef": "hrn:app:test:team", "reports": []any{map[string]any{"worker": "w1", "status": "locked", "prompt": "menu", "reason": "menu_or_question", "checkedAt": "2026-10-31T02:00:00Z"}}}}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("wire %#v want %#v", sent, want)
	}
	out, n, _, err = health1181Run(t, wire, 0)
	if err != nil || n != 1 {
		t.Fatal(err, n)
	}
	const golden = `{
  "reports": [
    {
      "worker": "w1",
      "status": "locked",
      "prompt": "menu",
      "reason": "menu_or_question",
      "lastTurnAt": null,
      "checkedAt": "2026-10-31T02:00:00Z"
    }
  ],
  "refused": []
}
`
	if out != golden {
		t.Fatalf("JSON golden drift %q", out)
	}
}
func TestHealth1181DoesNotInventWriteSuccess(t *testing.T) {
	for _, wire := range []string{`{"data":{"setHostWorkerTerminals":null}}`, `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[]}}}`} {
		_, n, _, err := health1181Run(t, wire, 0)
		if n != 1 || exitcode.FromError(err) != 1 {
			t.Fatalf("err%v n%d", err, n)
		}
	}
}
func TestHealth1181ClosedStatusCodes(t *testing.T) {
	for _, tc := range []struct{ s, p, r string }{{"idle", "empty", ""}, {"busy", "not_empty", ""}, {"dark", "unknown", "no_terminal"}, {"locked", "menu", "menu_or_question"}} {
		reason := "null"
		if tc.r != "" {
			reason = fmt.Sprintf("%q", tc.r)
		}
		wire := fmt.Sprintf(`[{"contract":"terminal-status@1","worker":"Iris","status":%q,"prompt":%q,"reason":%s,"lastTurnAt":null,"checkedAt":"2026-10-31T02:00:00Z"}]`, tc.s, tc.p, reason)
		rows, e := decodeHealthStatus([]byte(wire))
		if e != nil || rows[0].Status != tc.s || rows[0].Prompt != tc.p {
			t.Fatal(e, rows)
		}
	}
}

func TestHealth1181StatusSubprocessBoundary(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "AMBIENT_SECRET_1181")
	t.Setenv("HADRON_LAUNCH_GRANT", "GRANT_SECRET_1181")
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-status")
	script := "#!/bin/sh\n[ -z \"$HADRON_TOKEN\" ] || exit 41\n[ -z \"$HADRON_LAUNCH_GRANT\" ] || exit 42\n[ \"$1 $2 $3 $4 $5 $6\" = 'orca terminal status --all --json --config' ] || exit 43\n[ \"$7\" = '" + filepath.Join(dir, "host.yaml") + "' ] || exit 44\nprintf '[]'\nprintf 'PRIVATE_SCREEN' >&2\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	raw, code, err := runHealthStatusCommandEnv(context.Background(), bin, filepath.Join(dir, "host.yaml"), hostpoller.ScrubbedEnv(os.Environ(), "SYNTHETIC_BOUNDARY_KEY"))
	if err != nil || code != 0 || string(raw) != "[]" {
		t.Fatalf("code %d err %v output %q", code, err, raw)
	}
}

func TestHealth1181StatusOutputBound(t *testing.T) {
	cancelled := false
	b := healthStatusBuffer{onOverflow: func() { cancelled = true }}
	if _, err := b.Write(make([]byte, 8<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("PRIVATE_SCREEN")); err == nil {
		t.Fatal("unbounded subprocess output")
	}
	if !cancelled {
		t.Fatal("overflow did not cancel child")
	}
	if b.Len() != 8<<20 {
		t.Fatal("overflow retained")
	}
}

func TestHealth1181RegistrationRefusals(t *testing.T) {
	ok := `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1"}],"refused":[]}}}`
	for _, tc := range []struct {
		name, registrations, status, state string
		code                               int
	}{
		{"worker null", strings.Replace(health1181Registrations, `{"id":"w1","name":"Iris","urn":"hrn:worker:test:team:iris"}`, `null`, 1), health1181Status, `{"hostId":"own"}`, 1},
		{"worker mismatched", strings.Replace(health1181Registrations, `"id":"w1"`, `"id":"w2"`, 1), health1181Status, `{"hostId":"own"}`, 1},
		{"missing status", "", `[]`, `{"hostId":"own"}`, 1},
		{"non-live", strings.Replace(health1181Registrations, `"live":true`, `"live":false`, 1), health1181Status, `{"hostId":"own"}`, 8},
		{"missing host", "", health1181Status, `{"hostId":"missing"}`, 8},
		{"empty state", "", health1181Status, ``, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, n, _, err := health1181RunFixture(t, ok, 0, true, tc.registrations, tc.status, tc.state)
			if n != 0 || exitcode.FromError(err) != tc.code {
				t.Fatalf("writes %d err %v", n, err)
			}
		})
	}
}

func TestHealth1181InvalidServerResults(t *testing.T) {
	for _, result := range []string{
		`{"stored":[{"id":"w1"},{"id":"w1"}],"refused":[]}`,
		`{"stored":[{"id":"other"}],"refused":[]}`,
		`{"stored":[],"refused":[{"worker":"w1","code":"PRIVATE_SCREEN"}]}`,
		`{"stored":[{"id":"w1"}],"refused":[{"worker":"w1","code":"WORKER_NOT_FOUND"}]}`,
	} {
		_, n, _, err := health1181Run(t, `{"data":{"setHostWorkerTerminals":`+result+`}}`, 0)
		if n != 1 || exitcode.FromError(err) != 1 {
			t.Fatalf("writes %d err %v", n, err)
		}
	}
}

func TestHealth1181DecoderNegativeControls(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(health1181Status, `"locked"`, `"idle"`, 1),
		strings.Replace(health1181Status, `"reason":"menu_or_question"`, `"reason":null`, 1),
		strings.Replace(health1181Status, `"lastTurnAt":null`, `"lastTurnAt":"PRIVATE_SCREEN"`, 1),
		strings.Replace(health1181Status, `"lastTurnAt":null`, `"lastTurnAt":"2026-11-01T00:00:00Z"`, 1),
		strings.TrimSuffix(health1181Status, `]`) + `,` + strings.TrimPrefix(health1181Status, `[`),
	} {
		if _, err := decodeHealthStatus([]byte(raw)); err == nil {
			t.Fatal("invalid contract accepted")
		}
	}
}

func TestHealth1181LargeAndAmbiguousRegistration(t *testing.T) {
	var workers []any
	var rows []any
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("w%d", i)
		name := fmt.Sprintf("Worker%d", i)
		workers = append(workers, map[string]any{"workerId": id, "worker": map[string]any{"id": id, "name": name}})
		rows = append(rows, map[string]any{"contract": "terminal-status@1", "worker": name, "status": "dark", "prompt": "unknown", "reason": "no_terminal", "lastTurnAt": nil, "checkedAt": "2026-10-31T02:00:00Z"})
	}
	check := func(workers, rows []any) {
		t.Helper()
		reg, _ := json.Marshal(map[string]any{"data": map[string]any{"hostRegistrations": []any{map[string]any{"hostId": "own", "live": true, "workers": workers}}}})
		status, _ := json.Marshal(rows)
		_, n, _, err := health1181RunFixture(t, `{}`, 0, true, string(reg), string(status), `{"hostId":"own"}`)
		if n != 0 || exitcode.FromError(err) != 1 {
			t.Fatalf("writes %d err %v", n, err)
		}
	}
	check(workers, rows)
	workers = workers[:2]
	rows = rows[:2]
	workers[1].(map[string]any)["worker"].(map[string]any)["name"] = "Worker0"
	check(workers, rows)
	workers[1] = workers[0]
	check(workers, rows)
}

func TestHealth1181IgnoresOtherHostsStatusRows(t *testing.T) {
	status := strings.TrimSuffix(health1181Status, `]`) + `,` + strings.TrimPrefix(strings.Replace(health1181Status, `"Iris"`, `"OtherHostWorker"`, 1), `[`)
	out, n, sent, err := health1181RunFixture(t, `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1"}],"refused":[]}}}`, 0, true, "", status, `{"hostId":"own"}`)
	if err != nil || n != 1 {
		t.Fatal(err, n)
	}
	wire, _ := json.Marshal(sent)
	if strings.Contains(out+string(wire), "OtherHostWorker") {
		t.Fatal("foreign worker retained")
	}
}
