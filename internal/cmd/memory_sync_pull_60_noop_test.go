package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// LOCAL P2: these are successful GraphQL responses, not completed syncs.
// Gate6670029964 requires exit1 after a returned non-OK status.
func TestMemorySyncPull60DoesNotCertifyIncompleteStatus(t *testing.T) {
	for _, status := range []string{"PENDING", "SYNCING"} {
		for _, report := range []*string{nil, sp(partialReport())} {
			for _, asJSON := range []bool{false, true} {
				t.Run(status+"/"+map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
					gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, report, status) + `}}`})
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := []string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL}
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					if exitCodeFor(err) != 1 {
						t.Errorf("incomplete %s did not return exit1: %s", status, out)
					}
					if asJSON && out.Len() > 0 {
						var dto map[string]any
						if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
							t.Fatal(e)
						}
						if dto["clean"] == true {
							t.Errorf("incomplete status certified clean: %v", dto)
						}
					}
					if !asJSON && strings.HasPrefix(out.String(), "synced") {
						t.Errorf("incomplete status claimed synced: %s", out)
					}
				})
			}
		}
	}
}

func TestMemorySyncPull60StoredErrorGuards(t *testing.T) {
	for _, status := range []string{"OK", "ERROR"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(status+"/"+map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
				gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, sp(gitSource), sp(syncedAt), sp("clone failed https://user:tok@host/repo"), nil, status) + `}}`})
				f, out := testFactory(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				root := NewRootCmd(f)
				args := []string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if strings.Contains(out.String()+stderr.String(), "user:tok") {
					t.Fatal("stored URL credential leaked", out, stderr)
				}
				if !strings.Contains(out.String(), "[redacted]") {
					t.Fatal("stored error not rendered with mask", out)
				}
				if asJSON {
					var dto map[string]any
					if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
						t.Fatal(e)
					}
					if dto["clean"] != false {
						t.Fatal("error certified clean", dto)
					}
				}
				if status == "ERROR" && exitCodeFor(err) != 1 {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestMemorySyncPull60TerminalErrorGuard(t *testing.T) {
	message := "bad \x1b[2J\x1b]0;pwn\a text"
	body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "SYNC_FAILED"}}}})
	gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": string(body)})
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--server", gql.URL})
	err := root.Execute()
	if renderError(f, err) != 1 {
		t.Fatal(err)
	}
	if strings.ContainsAny(stderr.String(), "\x1b\a") || !strings.Contains(stderr.String(), "bad") || out.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestMemorySyncPull60StoredFailedAndUnknownCode(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		want         int
		status       string
	}{
		{"failed", `{"version":1,"outcome":"failed","recordedAt":"` + recordedAt + `","entries":[],"code":"SYNC_FAILED"}`, 1, "recorded"},
		{"newcode", `{"version":1,"outcome":"unknown","recordedAt":"` + recordedAt + `","entries":[],"code":"NEW_CODE"}`, 0, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(tc.report), "OK") + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--json", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.want {
				t.Fatal(err, out)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
				t.Fatal(e)
			}
			if dto["clean"] != false || dto["syncReportStatus"] != tc.status {
				t.Fatal(dto)
			}
		})
	}
}

// Gate6670195918: Confirm -> narrow preflight -> conditional mutation.
// Encryption is judged by the server before the no-source skip.
func TestMemorySyncPull60PrerequisiteShapes(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		for _, source := range []*string{nil, sp(""), sp("https://user:PRECHECK_SECRET@host/repo")} {
			t.Run(map[bool]string{false: "plain", true: "encrypted"}[encrypted], func(t *testing.T) {
				preflight, _ := json.Marshal(map[string]any{"data": map[string]any{"memory": map[string]any{"source": source, "isEncrypted": encrypted}}})
				mutation := `{"data":{"syncMemory":` + syncMemoryJSON(t, nil, sp(syncedAt), nil, nil, "OK") + `}}`
				if encrypted {
					mutation = `{"errors":[{"message":"encrypted; do not unlock/export","extensions":{"code":"ENCRYPTED_GIT_SYNC_UNAVAILABLE"}}]}`
				}
				gql, calls := captureSyncPull60(t, map[string]string{"MemorySyncPrerequisites": string(preflight), "SyncMemory": mutation})
				f, out := testFactory(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				root := NewRootCmd(f)
				root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--json", "--server", gql.URL})
				err := root.Execute()
				if calls["MemorySyncPrerequisites"] == nil {
					t.Fatal("no prerequisite read")
				}
				if !encrypted && (source == nil || *source == "") {
					if exitCodeFor(err) != 2 || calls["SyncMemory"] != nil || len(calls) != 1 {
						t.Fatalf("no-source err=%v calls=%v", err, calls)
					}
					if strings.Contains(err.Error(), "encrypted") {
						t.Fatal("generic source refusal claimed encryption", err)
					}
				} else {
					want := 0
					if encrypted {
						want = 2
					}
					if exitCodeFor(err) != want || calls["SyncMemory"] == nil || len(calls) != 2 {
						t.Fatalf("delegation err=%v calls=%v", err, calls)
					}
					if encrypted {
						renderError(f, err)
						if !strings.Contains(out.String(), "ENCRYPTED_GIT_SYNC_UNAVAILABLE") {
							t.Fatal("lost server refusal", out)
						}
					}
				}
				combined := out.String() + stderr.String()
				if err != nil {
					combined += err.Error()
				}
				for _, s := range []string{"PRECHECK_SECRET", "user:", "isEncrypted", "\"source\""} {
					if strings.Contains(combined, s) {
						t.Fatalf("preflight value leaked: %s", combined)
					}
				}
				var vars map[string]any
				if e := json.Unmarshal(calls["MemorySyncPrerequisites"], &vars); e != nil {
					t.Fatal(e)
				}
				if len(vars) != 1 || vars["ref"] != "hrn:mem:acme.com:kb" {
					t.Fatal(vars)
				}
			})
		}
	}
}
