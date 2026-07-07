package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryEncrypt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"EncryptMemory": `{"data":{"encryptMemory":{"id":"mem1","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("super-secret-data-key\n") // piped key, trimmed
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "mem1", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EncryptMemory"], &vars)
	if vars["memoryId"] != "mem1" || vars["dataKey"] != "super-secret-data-key" {
		t.Errorf("encrypt vars: %v (dataKey must be read from stdin, trimmed)", vars)
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
	gql, captured := captureGraphQL(t, map[string]string{
		"EncryptMemory": `{"data":{"encryptMemory":{"id":"mem1","urn":"acme.com::kb","name":"KB","isEncrypted":true}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "mem1", "--data-key", "  padded-key  ", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EncryptMemory"], &vars)
	if vars["dataKey"] != "padded-key" {
		t.Errorf("literal key should be trimmed, got %q", vars["dataKey"])
	}
}

// Irreversible + rewrites all content → gated like a destructive op.
func TestMemoryEncryptRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "mem1", "--data-key", "k", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestMemoryEncryptRequiresDataKey(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "encrypt", "mem1", "--yes", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "data key is required") {
		t.Fatalf("expected data-key-required error, got %v", err)
	}
}
