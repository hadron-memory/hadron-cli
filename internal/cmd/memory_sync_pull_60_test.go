package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMemorySyncPull60(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report *string
		at     *string
		clean  bool
		status string
	}{
		{"clean", nil, sp(syncedAt), true, "none"},
		{"partial", sp(partialReport()), sp(syncedAt), false, "recorded"},
		{"unknown", sp(`{"version":1,"outcome":"unknown","recordedAt":"` + recordedAt + `","entries":[]}`), sp(syncedAt), false, "recorded"},
		{"malformed", sp(`{`), sp(syncedAt), false, "unavailable"},
		{"incomplete v2", sp(`{"version":2}`), sp(syncedAt), false, "unavailable"},
		{"outcome", sp(`{"version":1,"outcome":"new","recordedAt":"` + recordedAt + `","entries":[]}`), sp(syncedAt), false, "unavailable"},
		{"never", nil, nil, false, "none"},
		{"kind", sp(strings.Replace(partialReport(), "protected", "new_kind", 1)), sp(syncedAt), false, "unavailable"},
		{"code", sp(strings.Replace(partialReport(), "PROTECTED_LOC", "NEW_CODE", 1)), sp(syncedAt), false, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, asJSON := range []bool{false, true} {
				gql, calls := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, nil, tc.at, nil, tc.report, "OK") + `}}`})
				f, out := testFactory(t)
				warnings := &strings.Builder{}
				f.IOStreams.ErrOut = warnings
				root := NewRootCmd(f)
				args := []string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if !tc.clean && !strings.Contains(warnings.String(), "Warning:") {
					t.Fatal("missing warning")
				}
				var vars map[string]any
				if err := json.Unmarshal(calls["SyncMemory"], &vars); err != nil {
					t.Fatal(err)
				}
				if len(vars) != 1 || vars["id"] != "hrn:mem:acme.com:kb" {
					t.Fatalf("variables: %v", vars)
				}
				if asJSON {
					var dto map[string]any
					if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
						t.Fatal(err)
					}
					if dto["clean"] != tc.clean || dto["syncReportStatus"] != tc.status || dto["operation"] != "pull" {
						t.Fatalf("dto: %v", dto)
					}
					if tc.name == "partial" {
						rep := dto["syncReport"].(map[string]any)
						if len(rep["entries"].([]any)) != 2 {
							t.Fatal(rep)
						}
					}
				} else {
					if tc.name == "partial" {
						for _, s := range []string{"skipped or held", "a:b", "PROTECTED_LOC", "new:place", "MOVE_HELD"} {
							if !strings.Contains(out.String(), s) {
								t.Fatalf("missing %s: %s", s, out)
							}
						}
					}
					if !tc.clean && strings.Contains(out.String(), "clean sync") {
						t.Fatal(out)
					}
				}
			}
		})
	}
}

func TestMemorySyncPull60Confirm(t *testing.T) {
	for _, tty := range []bool{false, true} {
		gql, calls := captureSyncPull60(t, nil)
		f, out := testFactory(t)
		if tty {
			f, out, _ = testFactoryTTY(t, "n\n")
		}
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--server", gql.URL})
		err := root.Execute()
		want := 2
		if tty {
			want = 6
		}
		if exitCodeFor(err) != want || len(calls) != 0 || strings.Contains(out.String(), "synced") {
			t.Fatalf("err %v calls %v out %s", err, calls, out)
		}
	}
}

func TestMemorySyncPull60Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"ENCRYPTED_GIT_SYNC_UNAVAILABLE", 2}, {"SYNC_FAILED", 1}, {"BAD_USER_INPUT", 2}, {"FORBIDDEN", 8}, {"NOT_FOUND", 4}, {"", 1}} {
		t.Run(tc.code, func(t *testing.T) {
			response := `{"errors":[{"message":"failed https://user:tok@host/repo","extensions":{"code":"` + tc.code + `"}}]}`
			gql, calls := captureSyncPull60(t, map[string]string{"SyncMemory": response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.exit || len(calls) != 2 || calls["MemorySyncPrerequisites"] == nil || calls["SyncMemory"] == nil {
				t.Fatalf("err %v calls %v", err, calls)
			}
			if strings.Contains(err.Error(), "user:tok") || strings.Contains(out.String(), "user:tok") {
				t.Fatal("credential leak")
			}
			if tc.code == "SYNC_FAILED" && !strings.Contains(err.Error(), "[redacted]") {
				t.Fatal(err)
			}
			if tc.code == "ENCRYPTED_GIT_SYNC_UNAVAILABLE" {
				for _, s := range []string{"unlock", "export"} {
					if strings.Contains(err.Error(), s) {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestMemorySyncPull60OneMutation(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, want int
	}{
		{"clean", `{"data":{"syncMemory":{"syncStatus":"OK","lastSyncedAt":"` + syncedAt + `","lastSyncReport":null,"syncError":null}}}`, 200, 0},
		{"encrypted", `{"errors":[{"message":"encrypted","extensions":{"code":"ENCRYPTED_GIT_SYNC_UNAVAILABLE"}}]}`, 200, 2},
		{"transport", "upstream unavailable", 503, 7},
		{"empty", `{"data":{}}`, 200, 7},
		{"storedfailed", `{"data":{"syncMemory":{"syncStatus":"ERROR","lastSyncedAt":"` + syncedAt + `","lastSyncReport":null,"syncError":null}}}`, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/graphql" {
					http.NotFound(w, r)
					return
				}
				n := count.Add(1)
				var request struct {
					OperationName string `json:"operationName"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.OperationName == "MemorySyncPrerequisites" {
					if n != 1 {
						t.Errorf("preflight out of order: %d", n)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"data":{"memory":{"source":"https://github.com/acme/kb","isEncrypted":false}}}`)
					return
				}
				if n != 2 || request.OperationName != "SyncMemory" {
					t.Errorf("mutation order: %d %s", n, request.OperationName)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.want || count.Load() != 2 {
				t.Fatalf("err %v calls %d out %s", err, count.Load(), out)
			}
			if tc.want == 7 && !strings.Contains(err.Error(), "may have happened") {
				t.Fatal(err)
			}
		})
	}
}

func TestMemorySyncPull60Selection(t *testing.T) {
	fields := map[string]bool{}
	for _, line := range strings.Split(gen.SyncMemory_Operation, "\n") {
		fields[strings.TrimSpace(line)] = true
	}
	for _, field := range []string{"syncStatus", "syncError", "lastSyncedAt", "lastSyncReport"} {
		if !fields[field] {
			t.Errorf("missing selected field %s", field)
		}
	}
	for _, line := range strings.Split(gen.SyncMemory_Operation, "\n") {
		if strings.Contains(strings.ToLower(line), "token") || strings.Contains(strings.ToLower(line), "secret") {
			t.Fatal("secret-bearing selection", line)
		}
	}
}

func TestMemorySyncPull60JSONError(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{{"SYNC_FAILED", 1}, {"ENCRYPTED_GIT_SYNC_UNAVAILABLE", 2}, {"INTERNAL_SERVER_ERROR", 1}} {
		t.Run(tc.code, func(t *testing.T) {
			gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"errors":[{"message":"failed https://user:tok@host/repo","extensions":{"code":"` + tc.code + `","requestId":"req-sync-60"}}]}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--json", "--server", gql.URL})
			err := root.Execute()
			if renderError(f, err) != tc.want {
				t.Fatal(err)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
				t.Fatal(e, out)
			}
			envelope, ok := dto["error"].(map[string]any)
			if !ok {
				t.Fatal(dto)
			}
			if strings.Contains(out.String(), "user:tok") || envelope["serverCode"] != tc.code {
				t.Fatal(dto)
			}
			if tc.code == "INTERNAL_SERVER_ERROR" && envelope["requestId"] != "req-sync-60" {
				t.Fatal(dto)
			}
		})
	}
}

// A configured, unencrypted memory unless a test explicitly supplies another
// preflight. This is separate from the mutation response: old stored metadata
// can never substitute for a source prerequisite read.
func captureSyncPull60(t *testing.T, responses map[string]string) (*httptest.Server, map[string]json.RawMessage) {
	t.Helper()
	all := map[string]string{"MemorySyncPrerequisites": `{"data":{"memory":{"source":"https://github.com/acme/kb","isEncrypted":false}}}`}
	for k, v := range responses {
		all[k] = v
	}
	return captureGraphQL(t, all)
}
