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
	"strings"
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

func health1181Run(t *testing.T, wire string, statusCode int) (string, error, int, map[string]any) {
	t.Helper()
	t.Setenv("HADRON_TOKEN", "AMBIENT_MUST_NOT_BE_USED")
	dir := t.TempDir()
	key := "SYNTHETIC_HOST_KEY_1181"
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
			writes++
			sent = q.Variables
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
	f.JSON = true
	c := newCmdTeamHealthWithReader(f, func(context.Context, *hostpoller.Config) ([]byte, int, error) {
		return []byte(health1181Status), statusCode, nil
	})
	c.SetArgs([]string{"sweep", "--config", path})
	err := c.Execute()
	all := out.String() + stderr.String()
	if strings.Contains(all, key) || strings.Contains(all, "PRIVATE_SCREEN") {
		t.Fatalf("secret in sink %q", all)
	}
	return out.String(), err, writes, sent
}

func TestHealth1181NoCoordinatorAndLegacyVerdictIgnored(t *testing.T) {
	base := `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"hrn:worker:test:team:iris"}],"refused":[]EXTRA}}}`
	var first string
	for i, x := range []string{"", `,"alarm":[{"secret":"PRIVATE_SCREEN"}],"noCoordinator":true`} {
		out, err, n, sent := health1181Run(t, strings.Replace(base, "EXTRA", x, 1), 0)
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
	out, err, n, _ := health1181Run(t, `{"data":{"setHostWorkerTerminals":{"stored":[],"refused":[{"worker":"w1","code":"TERMINAL_REPORT_STALE"}]}}}`, 0)
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
	} {
		out, err, n, _ := health1181Run(t, tc.wire, tc.status)
		if exitcode.FromError(err) != tc.want || n != tc.writes {
			t.Fatalf("exit %d want%d writes%d out%s err%v", exitcode.FromError(err), tc.want, n, out, err)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE_SCREEN") {
			t.Fatalf("secret in chain %v", err)
		}
	}
}
