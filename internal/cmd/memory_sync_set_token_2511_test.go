package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const sentinel2511 = "ghp_SENTINEL_2511_secret"

const setTokenOK2511 = `{"data":{"setMemorySourceToken":{"id":"01a000000000000000000000000000aa","urn":"hrn:mem:acme.com:kb"}}}`

func runSetToken2511(t *testing.T, stdin string, responses map[string]string, args ...string) (stdout, stderr string, calls map[string]json.RawMessage, err error) {
	t.Helper()
	gql, calls := captureGraphQL(t, responses)
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"memory", "sync", "set-token"}, append(args, "--server", gql.URL)...))
	err = root.Execute()
	return out.String(), errOut.String(), calls, err
}

func TestSetToken2511SendsOnceAndNeverEchoes(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		args := []string{"acme.com:kb"}
		if asJSON {
			args = append(args, "--json")
		}
		out, errOut, calls, err := runSetToken2511(t, sentinel2511+"\n", map[string]string{"SetMemorySourceToken": setTokenOK2511}, args...)
		if err != nil {
			t.Fatal(err)
		}
		var vars map[string]any
		if e := json.Unmarshal(calls["SetMemorySourceToken"], &vars); e != nil {
			t.Fatal(e)
		}
		if vars["token"] != sentinel2511 || vars["ref"] != "hrn:mem:acme.com:kb" {
			t.Fatalf("variables: %v", vars)
		}
		if _, has := vars["expiresAt"]; has {
			t.Fatalf("expiresAt must be omitted when not given: %v", vars)
		}
		for _, op := range []string{"SyncMemory", "PushMemoryToGit", "MemorySyncPrerequisites"} {
			if _, sent := calls[op]; sent {
				t.Fatalf("%s must not be sent", op)
			}
		}
		if strings.Contains(out, sentinel2511) || strings.Contains(errOut, sentinel2511) {
			t.Fatalf("token leaked: %q %q", out, errOut)
		}
		if asJSON {
			var dto map[string]any
			if e := json.Unmarshal([]byte(out), &dto); e != nil {
				t.Fatal(e)
			}
			if dto["operation"] != "set-token" || dto["tokenSet"] != true {
				t.Fatalf("dto: %v", dto)
			}
		}
	}
}

func TestSetToken2511ExpiresAtAndLocalRefusals(t *testing.T) {
	_, _, calls, err := runSetToken2511(t, sentinel2511, map[string]string{"SetMemorySourceToken": setTokenOK2511}, "acme.com:kb", "--expires-at", "2027-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(calls["SetMemorySourceToken"], &vars)
	if vars["expiresAt"] != "2027-01-02T03:04:05Z" {
		t.Fatalf("variables: %v", vars)
	}
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"empty token":    {"\n", []string{"acme.com:kb"}},
		"bad expires-at": {sentinel2511, []string{"acme.com:kb", "--expires-at", "tomorrow"}},
		"bad ref":        {sentinel2511, []string{"not a ref"}},
	} {
		out, errOut, calls, err := runSetToken2511(t, tc.stdin, map[string]string{"SetMemorySourceToken": setTokenOK2511}, tc.args...)
		if err == nil {
			t.Fatalf("%s: want usage error", name)
		}
		if len(calls) != 0 {
			t.Fatalf("%s: request sent: %v", name, calls)
		}
		if strings.Contains(out+errOut+err.Error(), sentinel2511) {
			t.Fatalf("%s: token leaked", name)
		}
	}
}

func TestSetToken2511RefusalMapsWithoutToken(t *testing.T) {
	// A hostile server that reflects the token in its message and code.
	resp := `{"errors":[{"message":"bad token ` + sentinel2511 + `","extensions":{"code":"ENCRYPTED_GIT_SYNC_UNAVAILABLE ` + sentinel2511 + `"}}],"data":null}`
	out, errOut, calls, err := runSetToken2511(t, sentinel2511, map[string]string{"SetMemorySourceToken": resp}, "acme.com:kb", "--json")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(out+errOut+err.Error(), sentinel2511) {
		t.Fatalf("token leaked: %q %q %v", out, errOut, err)
	}
	if len(calls) != 1 {
		t.Fatalf("exactly one request, got %v", calls)
	}
}

func TestSetToken2511NullFieldIsNotASuccess(t *testing.T) {
	out, _, _, err := runSetToken2511(t, sentinel2511, map[string]string{"SetMemorySourceToken": `{"data":{"setMemorySourceToken":null}}`}, "acme.com:kb")
	if err == nil || strings.Contains(out, "token set") {
		t.Fatalf("a null answer must not report success: %q %v", out, err)
	}
}

func TestSetToken2511ErrorChainNeverHoldsToken(t *testing.T) {
	resp := `{"errors":[{"message":"bad ` + sentinel2511 + `","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	_, _, _, err := runSetToken2511(t, sentinel2511, map[string]string{"SetMemorySourceToken": resp}, "acme.com:kb")
	if err == nil {
		t.Fatal("want error")
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), sentinel2511) || strings.Contains(fmt.Sprintf("%+v", e), sentinel2511) {
			t.Fatalf("token in error chain: %v", e)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("server answer should be kept, redacted: %v", err)
	}
}

func TestSetToken2511TokenFileForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	if err := os.WriteFile(path, []byte(sentinel2511+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := map[string]string{"SetMemorySourceToken": setTokenOK2511}
	for name, flag := range map[string]string{"path": path, "@path": "@" + path, "stdin": "-"} {
		out, errOut, calls, err := runSetToken2511(t, sentinel2511, ok, "acme.com:kb", "--token-file", flag)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var vars map[string]any
		_ = json.Unmarshal(calls["SetMemorySourceToken"], &vars)
		if vars["token"] != sentinel2511 || strings.Contains(out+errOut, sentinel2511) {
			t.Fatalf("%s: %v %q %q", name, vars, out, errOut)
		}
		if !strings.Contains(out, "hrn:mem:acme.com:kb") {
			t.Fatalf("%s: receipt should name the memory: %q", name, out)
		}
	}
	for name, flag := range map[string]string{"empty file": empty, "missing file": filepath.Join(dir, "nope"), "empty flag": ""} {
		_, errOut, calls, err := runSetToken2511(t, sentinel2511, ok, "acme.com:kb", "--token-file", flag)
		if err == nil || len(calls) != 0 || strings.Contains(errOut+err.Error(), sentinel2511) {
			t.Fatalf("%s: err=%v calls=%v", name, err, calls)
		}
	}
}

// A refusal is sent exactly once: count requests, not distinct operation names.
func TestSetToken2511RefusalIsNotRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}],"data":null}`))
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(sentinel2511)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "set-token", "acme.com:kb", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("want error")
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("want exactly one request, got %d", got)
	}
}
