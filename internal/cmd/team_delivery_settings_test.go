package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const deliverySettingsJSON = `{"batchWindowSeconds":120,"busyDelaySeconds":600,"stuckWindowSeconds":900,"standbyEnabled":true,"standbyEffective":false}`

func deliveryResponses() map[string]string {
	r := taskResponses()
	r["TeamDeliverySettings"] = `{"data":{"appDeliverySettings":` + deliverySettingsJSON + `}}`
	r["TeamDeliverySettingsSet"] = `{"data":{"setAppDeliverySettings":` + deliverySettingsJSON + `}}`
	return r
}

func TestTeamDeliverySettingsRoundTripDTO(t *testing.T) {
	for _, args := range [][]string{{"get"}, {"set", "--standby=false"}} {
		t.Run(args[0], func(t *testing.T) {
			calls, out, err := runTeamTask(t, deliveryResponses(), append([]string{"team", "delivery-settings"}, append(args, "--json")...)...)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err = json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(deliverySettingsJSON), &want)
			if len(got) != len(want) {
				t.Fatalf("DTO %v", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s = %v, want %v", k, got[k], v)
				}
			}
			op := "TeamDeliverySettings"
			if args[0] == "set" {
				op = "TeamDeliverySettingsSet"
			}
			seen := 0
			for _, c := range *calls {
				if c.Op == op {
					seen++
					if c.Vars["appRef"] != "capp100000000000000000000" {
						t.Fatalf("scope %v", c.Vars)
					}
					if c.Session != "" {
						t.Fatalf("settings must use ordinary authenticated caller, got worker session %q", c.Session)
					}
				}
			}
			if seen != 1 {
				t.Fatalf("%s calls %d", op, seen)
			}
		})
	}
}

func TestTeamDeliverySettingsPatchPresence(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--standby=false"}, `{"standbyEnabled":false}`},
		{[]string{"--busy-delay-seconds", "700"}, `{"busyDelaySeconds":700}`},
		{[]string{"--batch-window-seconds", "60", "--busy-delay-seconds", "1", "--stuck-window-seconds", "62", "--standby=true"}, `{"batchWindowSeconds":60,"busyDelaySeconds":1,"stuckWindowSeconds":62,"standbyEnabled":true}`},
		{nil, `{}`},
	} {
		calls, out, err := runTeamTask(t, deliveryResponses(), append([]string{"team", "delivery-settings", "set"}, tc.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, c := range *calls {
			if c.Op == "TeamDeliverySettingsSet" {
				seen++
				var vars map[string]json.RawMessage
				_ = json.Unmarshal(c.RawVars, &vars)
				var actual, expected map[string]any
				_ = json.Unmarshal(vars["input"], &actual)
				_ = json.Unmarshal([]byte(tc.want), &expected)
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("wire %s want %s", vars["input"], tc.want)
				}
			}
		}
		if seen != 1 {
			t.Fatalf("mutations %d", seen)
		}
		if tc.args == nil && (!strings.Contains(out, "unchanged") || strings.Contains(out, "✓")) {
			t.Fatalf("empty patch claimed a change: %q", out)
		}
	}
}

func TestTeamDeliverySettingsLocalIntegerRefusals(t *testing.T) {
	for _, v := range []string{"1.5", "null", "default", "2147483648", "-2147483649"} {
		calls, _, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "set", "--busy-delay-seconds", v)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%s: %v", v, err)
		}
		for _, c := range *calls {
			if c.Op == "TeamDeliverySettingsSet" {
				t.Fatalf("invalid %s mutated", v)
			}
		}
	}
}

func TestTeamDeliverySettingsRefusalNoRetry(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"FORBIDDEN", exitcode.Forbidden}, {"APP_NOT_FOUND", exitcode.NotFound},
		{"SESSION_NOT_LIVE", exitcode.Error}, {"DELIVERY_SETTINGS_OUT_OF_RANGE", exitcode.Usage},
		{"DELIVERY_SETTINGS_CONFLICT", exitcode.Usage}, {"TICKER_SETTINGS_OUT_OF_RANGE", exitcode.Usage},
		{"TICKER_SETTINGS_CONFLICT", exitcode.Conflict}, {"DELIVERY_STANDBY_UNAVAILABLE", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := deliveryResponses()
			msg := "The exact server refusal sentence."
			r["TeamDeliverySettingsSet"] = `{"errors":[{"message":"` + msg + `","extensions":{"code":"` + tc.code + `"}}]}`
			calls, out, err := runTeamTask(t, r, "team", "delivery-settings", "set", "--standby=true")
			if err == nil || exitCodeFor(err) != tc.exit || !strings.Contains(err.Error(), msg) {
				t.Fatalf("exit/message: %v (%d)", err, exitCodeFor(err))
			}
			if strings.Contains(out, "✓") || strings.Contains(out, "standbyEnabled") {
				t.Fatalf("false success %q", out)
			}
			count := 0
			for _, c := range *calls {
				if c.Op == "TeamDeliverySettingsSet" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("mutations %d", count)
			}
		})
	}
}

func TestTeamDeliverySettingsHelpAndText(t *testing.T) {
	for _, sub := range []string{"get", "set"} {
		_, help, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", sub, "--help")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"cadence floor", "current limits", "120", "600", "900"} {
			if !strings.Contains(help, s) {
				t.Fatalf("%s help lacks %q: %s", sub, s, help)
			}
		}
		if strings.Contains(help, "(default true)") || strings.Contains(help, "standby active") {
			t.Fatalf("misleading help %s", help)
		}
	}
	_, out, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"worktree binding", "batch window / ticker cadence floor", "120 s", "600 s", "900 s", "standby (stored)", "standby (effective)"} {
		if !strings.Contains(out, s) {
			t.Fatalf("text lacks %q: %s", s, out)
		}
	}
}

// Review regression: floor edits never silently rewrite existing tickers.
func TestTeamDeliverySettingsFloorHelpDoesNotPromiseRetiming(t *testing.T) {
	for _, sub := range []string{"get", "set"} {
		_, out, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", sub, "--help")
		if err != nil {
			t.Fatal(err)
		}
		normalized := strings.Join(strings.Fields(out), " ")
		if strings.Contains(normalized, "changing it changes ticker cadence") {
			t.Fatal("help promises silent retiming")
		}
		for _, want := range []string{"Existing tickers keep their cadence", "Raising it above a live ticker's cadence is refused"} {
			if !strings.Contains(normalized, want) {
				t.Fatalf("missing consequence %q: %s", want, out)
			}
		}
	}
}

// A stored-on projection must not be rendered as effective-on (Joran P2).
func TestTeamDeliverySettingsDistinctStandbyText(t *testing.T) {
	_, out, err := runTeamTask(t, deliveryResponses(), "team", "delivery-settings", "get")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ label, value string }{{"standby (stored)", "true"}, {"standby (effective)", "false"}} {
		matched := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, row.label) {
				matched = true
				fields := strings.Fields(line)
				if fields[len(fields)-1] != row.value {
					t.Fatalf("wrong server value: %s", line)
				}
			}
		}
		if !matched {
			t.Fatalf("missing %s", row.label)
		}
	}
}
