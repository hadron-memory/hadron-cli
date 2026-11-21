package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestEncryptionKeyRefusals1650(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  MemoryKey
		app  bool
	}{
		{"neither", MemoryKey{Memory: "m"}, false},
		{"both", MemoryKey{Memory: "m", Passphrase: "p", DataKey: "k"}, false},
		{"app-passphrase", MemoryKey{Memory: "m", Passphrase: "p"}, true},
		{"app-invalid-key", MemoryKey{Memory: "m", DataKey: "invalid"}, true},
		{"header-limit", MemoryKey{Memory: "m", Passphrase: strings.Repeat("x", MaxMemoryKeysBytes)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := WithEncryptionKey(context.Background(), tc.key, tc.app)
			if err == nil || exitcode.FromError(err) != exitcode.Usage {
				t.Fatalf("want usage refusal: %v", err)
			}
			if HasMemoryKeys(ctx) || operationKeyFrom(ctx) != "" {
				t.Fatal("refused context retained key material")
			}
		})
	}
}
func TestEncryptionKeyContextAndEmptySecretBases1650(t *testing.T) {
	base, err := WithMemoryKeys(context.Background(), []MemoryKey{{Memory: "other", Passphrase: "ambient"}})
	if err != nil {
		t.Fatal(err)
	}
	base = WithOperationKey(base, "ambient-op")
	key := strings.Repeat("A", 43) + "="
	for _, tc := range []struct {
		name string
		key  MemoryKey
		app  bool
	}{
		{"owner-key", MemoryKey{Memory: "target", DataKey: key}, false},
		{"owner-passphrase", MemoryKey{Memory: "target", Passphrase: "private-sentinel"}, false},
		{"app-key", MemoryKey{Memory: "target", DataKey: key}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := WithEncryptionKey(base, tc.key, tc.app)
			if err != nil {
				t.Fatal(err)
			}
			keys, has := memoryKeysFrom(ctx)
			op := operationKeyFrom(ctx)
			if tc.app {
				if has || op != key {
					t.Fatal("App did not use only operation key")
				}
			} else {
				if !has || op != "" {
					t.Fatal("owner did not use only memory key")
				}
				var entries []MemoryKey
				if json.Unmarshal([]byte(keys.header), &entries) != nil || len(entries) != 1 || entries[0] != tc.key {
					t.Fatal("ambient key merged into encryption")
				}
				for _, s := range keys.secrets {
					if s == "" {
						t.Fatal("empty redaction secret")
					}
				}
			}
			if RedactEncryptionText(ctx, "ordinary unrelated output") != "ordinary unrelated output" {
				t.Fatal("empty secret polluted all text")
			}
		})
	}
	clean := WithoutKeyMaterial(base)
	if HasMemoryKeys(clean) || operationKeyFrom(clean) != "" {
		t.Fatal("lookup context retained key material")
	}
	if !HasMemoryKeys(base) || operationKeyFrom(base) != "ambient-op" {
		t.Fatal("child changed the parent context")
	}
}
