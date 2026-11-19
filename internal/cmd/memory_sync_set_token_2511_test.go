package cmd

import (
	"encoding/json"
	"strings"
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
		if vars["token"] != sentinel2511 || vars["id"] != "hrn:mem:acme.com:kb" {
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
