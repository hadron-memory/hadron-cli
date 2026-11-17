package team

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

func TestAckMixed1229ConfigWiringAndStatus(t *testing.T) {
	for _, c := range []struct {
		name, key string
		want      bool
	}{{"omitted", "", false}, {"false", "  ack_mixed: false\n", false}, {"true", "  ack_mixed: true\n", true}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.yaml")
			body := "app: app1\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\nkey_file: key\nworktrees_root: " + dir + "\ndelivery:\n  enabled: true\n  orca_bin: /bin/true\n" + c.key
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := hostpoller.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := deliveryFake(t, map[string][]string{})
			p := hostpoller.New(cfg, nil, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
			d := wireDelivery(p, cfg, s, func(hostpoller.Event) {})
			if d.AckMixed != c.want {
				t.Fatalf("AckMixed=%v want %v", d.AckMixed, c.want)
			}
			status := hostDeliveryStatus(cfg, time.Now())
			raw, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			if got["ackMixed"] != c.want {
				t.Fatalf("status %s", raw)
			}
			var out bytes.Buffer
			writeHostDelivery(&out, status)
			word := "off"
			if c.want {
				word = "on"
			}
			if !strings.Contains(out.String(), "ack-mixed "+word) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestAckMixed1229RejectsNonBoolean(t *testing.T) {
	for _, v := range []string{"maybe", "1", "[]", "{}", "\"true\"", "yes", "null"} {
		t.Run(v, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "host.yaml")
			body := "app: app1\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\nkey_file: key\nworktrees_root: " + dir + "\ndelivery:\n  ack_mixed: " + v + "\n"
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := hostpoller.LoadConfig(p); err == nil {
				t.Fatalf("accepted %s", v)
			}
		})
	}
}

func TestAckMixed1229YAMLCompatibility(t *testing.T) {
	for name, fragment := range map[string]string{
		"merged":        "delivery: {<<: &d {enabled: true, orca_bin: /bin/true}, ack_mixed: true}\n",
		"alias":         "<<: {delivery: &d {enabled: true, orca_bin: /bin/true, ack_mixed: true}}\ndelivery: *d\n",
		"boolean alias": "delivery: {enabled: &on true, orca_bin: /bin/true, ack_mixed: *on}\n",
		"boolean case":  "delivery: {enabled: true, orca_bin: /bin/true, ack_mixed: TRUE}\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.yaml")
			body := "app: app1\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\nworktrees_root: " + dir + "\n" + fragment
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := hostpoller.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			server, _ := deliveryFake(t, map[string][]string{})
			p := hostpoller.New(cfg, nil, nil, &hostpoller.State{}, "", func(hostpoller.Event) {})
			if !wireDelivery(p, cfg, server, func(hostpoller.Event) {}).AckMixed {
				t.Fatal("boolean assertion not preserved")
			}
		})
	}
}

func TestAckMixed1229KeepsKnownFieldDiagnostics(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "host.yaml")
	body := "app: app1\nworkers: {iris: {family: claude, launch: [claude]}}\nworktrees_root: " + dir + "\ndelivery:\n  unexpected: true\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := hostpoller.LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "unexpected") || !strings.Contains(err.Error(), "line 5") {
		t.Fatalf("lost known-field line diagnostic: %v", err)
	}
}
