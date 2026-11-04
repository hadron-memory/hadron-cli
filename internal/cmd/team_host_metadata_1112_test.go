package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestTeamHostMetadata1112(t *testing.T) {
	for _, field := range []string{"label", "app", "worker", "launch", "created", "registration-id", "registration-time", "read-error"} {
		for _, variant := range []string{"clean", "controls", "long"} {
			t.Run(field+"/"+variant, func(t *testing.T) {
				value, want := "a\nb\x1b[31mc\u009b\u007f\u202e\u2028\u2029\u200b", "a b [31mc      "
				if variant == "clean" {
					value, want = "fixture", "fixture"
				}
				if variant == "long" {
					value = strings.Repeat("界", 1000)
					want = strings.Repeat("界", 255) + "…"
				}
				dir := t.TempDir()
				key := filepath.Join(dir, "host.key")
				if err := os.WriteFile(key, []byte("hk_fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				worker := map[string]any{"family": "claude", "launch": []string{"fixture-worker"}}
				workers := map[string]any{"iris": worker}
				cfg := map[string]any{"app": "hrn:app:acme.com:eng-team", "label": "fixture-host", "poll_interval_seconds": 30, "key_file": key, "worktrees_root": filepath.Join(dir, "trees"), "workers": workers}
				state := map[string]any{"created": map[string][]string{}, "keep": map[string]string{}, "hostId": "h1"}
				reg := map[string]any{"hostId": "h1", "label": "fixture-host", "pollIntervalSeconds": 30, "registeredAt": "2026-10-27T00:00:00Z", "refreshedAt": "2026-10-27T00:00:00Z", "live": true, "workers": []any{}}
				switch field {
				case "label":
					cfg["label"] = value
				case "app":
					cfg["app"] = value
				case "worker":
					delete(workers, "iris")
					workers[value] = worker
				case "launch":
					worker["launch"] = []string{value}
				case "created":
					state["created"] = map[string][]string{"iris": {value}}
				case "registration-id":
					state["hostId"] = value
					reg["hostId"] = value
				case "registration-time":
					reg["refreshedAt"] = value
				}
				write := func(name string, data any) {
					b, err := json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.NewReplacer("\u009b", `\u009b`, "\u007f", `\u007f`).Replace(string(b))), 0600); err != nil {
						t.Fatal(err)
					}
				}
				write("host.json", cfg)
				write("hadron-host-state.json", state)
				body, _ := json.Marshal(map[string]any{"data": map[string]any{"hostRegistrations": []any{reg}}})
				if field == "read-error" {
					body, _ = json.Marshal(map[string]any{"errors": []any{map[string]any{"message": value, "extensions": map[string]any{"code": "UNTYPED_SERVER_ERROR"}}}})
				}
				srv, _ := hostServerFake(t, map[string]string{"HostRegistrations": string(body)})
				run := func(asJSON bool) (string, error) {
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := []string{"team", "host", "status", "--config", filepath.Join(dir, "host.json"), "--server", srv.URL}
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					return out.String(), err
				}
				// Label's existing 128-rune config limit rejects this before rendering;
				// do not weaken validation just to manufacture a display-length test.
				if field == "label" && variant == "long" {
					out, err := run(false)
					if err == nil || out != "" || !strings.Contains(err.Error(), "label must be 1 to 128") {
						t.Fatalf("long label must remain rejected: %q %v", out, err)
					}
					return
				}
				raw, err := run(true)
				if err != nil {
					t.Fatal(err)
				}
				metadataJSON1112(t, "host-"+field+"-"+variant, raw, dir)
				out, err := run(false)
				if err != nil {
					t.Fatal(err)
				}
				if field == "read-error" && variant == "long" {
					var dto struct {
						ReadError string `json:"readError"`
					}
					if err := json.Unmarshal([]byte(raw), &dto); err != nil {
						t.Fatal(err)
					}
					r := []rune(dto.ReadError)
					if len(r) <= 256 {
						t.Fatal("fixture error was not long")
					}
					want = string(r[:255]) + "…"
				}
				if !strings.Contains(out, want) {
					t.Error("missing bounded sanitized host value")
				}
				if variant == "clean" {
					metadataText1112(t, "host-"+field, out, dir)
				}
				for _, r := range out {
					if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
						t.Errorf("raw terminal character U+%04X", r)
						break
					}
				}
				if strings.Contains(out, strings.Repeat("界", 256)) {
					t.Error("uncut long host value survived")
				}
			})
		}
	}
}
