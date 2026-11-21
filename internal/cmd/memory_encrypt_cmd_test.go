package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryEncrypt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryEncryptionPrincipal": `{"data":{"authContext":{"principalType":"USER"}}}`,
		"EncryptMemory":             `{"data":{"encryptMemory":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("super-secret-data-key\n") // piped key, trimmed
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EncryptMemory"], &vars)
	if vars["memoryRef"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || len(vars) != 1 {
		t.Errorf("encrypt vars: %v (only the target belongs in variables)", vars)
	}
	var dto struct {
		IsEncrypted bool `json:"isEncrypted"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.IsEncrypted {
		t.Errorf("expected isEncrypted true, got %s", out.String())
	}
}

// A literal --data-key is trimmed too (not just the stdin path), so stray
// copy-paste whitespace can't silently corrupt the key.
func TestMemoryEncryptTrimsLiteralKey(t *testing.T) {
	gql, captured := captureLinkUserGraphQL(t, map[string]string{
		"MemoryEncryptionPrincipal": `{"data":{"authContext":{"principalType":"USER"}}}`,
		"EncryptMemory":             `{"data":{"encryptMemory":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--data-key", "  padded-key  ", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, r := range *captured {
		if r.Op == "EncryptMemory" {
			var keys []map[string]string
			if err := json.Unmarshal([]byte(r.MemoryKeys), &keys); err != nil || len(keys) != 1 || keys[0]["dataKey"] != "padded-key" {
				t.Fatal("literal key was not trimmed in the header")
			}
			return
		}
	}
	t.Fatal("no encryption request")
}

// Irreversible + rewrites all content → gated like a destructive op.
func TestMemoryEncryptRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--data-key", "k", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestMemoryEncryptRequiresDataKey(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--yes", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "data key is required") {
		t.Fatalf("expected data-key-required error, got %v", err)
	}
}
