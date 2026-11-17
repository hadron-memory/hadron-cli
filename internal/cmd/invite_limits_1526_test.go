package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// V49 is server policy: help states it, but limits are sent only when set.
func TestInviteLimitsHelp1526(t *testing.T) {
	for _, args := range [][]string{{"user", "invite", "--help"}, {"org", "invite", "create", "--help"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			for _, want := range []string{"1-10", "2-1000", "server default 10", "server default 2"} {
				if !strings.Contains(text, want) {
					t.Errorf("help missing %q: %s", want, text)
				}
			}
			for _, old := range []string{"unlimited", "never expires", "no expiry", "default 1;", "--max-activations 1 --json"} {
				if strings.Contains(text, old) {
					t.Errorf("stale %q in help", old)
				}
			}
		})
	}
}

func TestInviteLimitsAgentic1526(t *testing.T) {
	text := string(readContract(t))
	for _, want := range []string{"1-10", "2-1000", "server default 10", "server default 2", "BAD_USER_INPUT"} {
		if !strings.Contains(text, want) {
			t.Errorf("agentic missing %q", want)
		}
	}
	for _, old := range []string{"Defaults: one activation, no expiry", "--max-activations 0` means unlimited", "--expires-in-days 0` means never"} {
		if strings.Contains(text, old) {
			t.Errorf("stale %q in agentic", old)
		}
	}
}

func TestInviteLimitsWire1526(t *testing.T) {
	for _, tc := range []struct {
		name, op, field, expiry string
		args                    []string
	}{
		{"platform", "InviteUser", "inviteUser", "--expires-in-days", []string{"user", "invite"}},
		{"org", "CreateUserInvitation", "createUserInvitation", "--expires-days", []string{"org", "invite", "create", "alice@example.test", "--org", "org1", "--role", "READER"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"omitted", "zero-expiry", "zero-cap"} {
				t.Run(mode, func(t *testing.T) {
					body := `{"data":{"` + tc.field + `":` + orgInviteJSON + `}}`
					message := "expiresInDays must be 1-10"
					flag, key := tc.expiry, "expiresInDays"
					if mode == "zero-cap" {
						flag, key, message = "--max-activations", "maxActivations", "maxActivations must be 2-1000"
					}
					if mode != "omitted" {
						b, err := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "BAD_USER_INPUT"}}}})
						if err != nil {
							t.Fatal(err)
						}
						body = string(b)
					}
					srv, captured := captureGraphQL(t, map[string]string{tc.op: body})
					f, out := testFactory(t)
					f.IOStreams.ErrOut = out
					root := NewRootCmd(f)
					args := append([]string{}, tc.args...)
					if mode != "omitted" {
						args = append(args, flag, "0")
					}
					root.SetArgs(append(args, "--server", srv.URL))
					err := root.Execute()
					if mode == "omitted" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						if code := renderError(f, err); code != 2 {
							t.Fatalf("exit=%d err=%v", code, err)
						}
						if !strings.Contains(out.String(), message) {
							t.Fatalf("server message lost: %s", out)
						}
					}
					raw, ok := captured[tc.op]
					if !ok || len(captured) != 1 {
						t.Fatalf("expected exactly operation %s: %v", tc.op, captured)
					}
					var vars map[string]json.RawMessage
					if err := json.Unmarshal(raw, &vars); err != nil {
						t.Fatal(err)
					}
					for _, k := range []string{"expiresInDays", "maxActivations"} {
						v, present := vars[k]
						if mode != "omitted" && k == key {
							if !present || string(v) != "0" {
								t.Errorf("explicit zero lost %s=%s", k, v)
							}
						} else if present {
							t.Errorf("unset %s sent: %s", k, v)
						}
					}
				})
			}
		})
	}
}
