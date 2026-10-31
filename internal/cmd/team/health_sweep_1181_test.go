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
			_, _ = io.WriteString(w, `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"w1","worker":{"id":"w1","name":"Iris","urn":"hrn:worker:test:team:iris"},"worktrees":["iris"]}]}]}}`)
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
	for n, s := range map[string]string{"key": key, "state.json": `{"hostId":"own"}`} {
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
		return []byte(health1181Status), statusCode, nil
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
	raw, code, err := runHealthStatusCommand(context.Background(), bin, filepath.Join(dir, "host.yaml"))
	if err != nil || code != 0 || string(raw) != "[]" {
		t.Fatalf("code %d err %v output %q", code, err, raw)
	}
}

func TestHealth1181StatusOutputBound(t *testing.T) {
	var b healthStatusBuffer
	if _, err := b.Write(make([]byte, 8<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("PRIVATE_SCREEN")); err == nil {
		t.Fatal("unbounded subprocess output")
	}
	if b.Len() != 8<<20 {
		t.Fatal("overflow retained")
	}
}
