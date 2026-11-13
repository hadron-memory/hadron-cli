package team

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// Both deliverer constructors must hand delivery the registered directories:
// without them the plain-directory claim checks (b) and (c) are inert
// (cli#1989, §10 P2-2). Each site is pinned to the config's resolved paths.
func TestBothDelivererSitesWireRegisteredFromTheConfig(t *testing.T) {
	root := t.TempDir()
	body := "app: app1\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\n    worktrees: [plain/iris]\nkey_file: key\nworktrees_root: " + root + "\ndelivery:\n  enabled: true\n  orca_bin: /bin/true\n"
	path := filepath.Join(root, "host.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := hostpoller.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "plain", "iris")
	s, _ := deliveryFake(t, map[string][]string{})
	st := &hostpoller.State{}
	p := hostpoller.New(cfg, nil, nil, st, "", func(hostpoller.Event) {})
	for name, reg := range map[string]func() map[string][]string{
		"poller": func() map[string][]string {
			d := wireDelivery(p, cfg, s, func(hostpoller.Event) {})
			if d.Registered == nil {
				return nil
			}
			return d.Registered()
		},
		"C2 path": func() map[string][]string {
			d := newPostHostDeliverer(s, cfg, st)
			if d.Registered == nil {
				return nil
			}
			return d.Registered()
		},
	} {
		got := reg()
		if len(got["iris"]) != 1 || got["iris"][0] != want {
			t.Fatalf("%s: Registered = %v, want iris -> [%s]", name, got, want)
		}
	}
}
