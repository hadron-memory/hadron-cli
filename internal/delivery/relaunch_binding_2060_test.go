package delivery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRelaunchBindingRequiresWholeIdentity2060(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		usable    bool
	}{
		{"complete", `{"workerId":"w","sessionId":"s","appId":"a","server":"https://private.invalid"}`, true},
		{"legacy", `{"workerId":"w","sessionId":"s"}`, false},
		{"missing worker", `{"sessionId":"s","appId":"a","server":"https://private.invalid"}`, false},
		{"missing session", `{"workerId":"w","appId":"a","server":"https://private.invalid"}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			cache := filepath.Join(dir, plainCacheName)
			if err := os.Mkdir(cache, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cache, bindingFileName), []byte(test.raw), 0600); err != nil {
				t.Fatal(err)
			}
			b := WorktreeBindingIdentity(dir)
			if b.Usable != test.usable {
				t.Fatalf("%+v", b)
			}
			if b.Usable && (b.AppID != "a" || b.Server != "https://private.invalid" || b.WorkerID != "w" || b.SessionID != "s") {
				t.Fatal(b)
			}
		})
	}
}
