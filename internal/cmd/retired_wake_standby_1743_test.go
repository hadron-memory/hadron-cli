package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetiredSelections1743ContinuingPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		op   string
	}{
		{"wake-get", []string{"team", "wake-settings", "get", "--worker", c1292Worker, "--json"}, "WorkerWakeSettings"},
		{"pause", []string{"team", "wake-settings", "set", "--worker", c1292Worker, "--paused-until", "2027-01-01T00:00:00Z", "--json"}, "SetWorkerWakeSettings"},
		{"clear-pause", []string{"team", "wake-settings", "set", "--worker", c1292Worker, "--clear-pause", "--json"}, "SetWorkerWakeSettings"},
		{"delivery-get", []string{"team", "delivery-settings", "get", "--json"}, "TeamDeliverySettings"},
		{"delivery-set", []string{"team", "delivery-settings", "set", "--busy-delay-seconds", "700", "--json"}, "TeamDeliverySettingsSet"},
		{"delivery-empty-patch", []string{"team", "delivery-settings", "set", "--json"}, "TeamDeliverySettingsSet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := deliveryResponses()
			r["WorkerWakeSettings"] = `{"data":{"workerWakeSettings":` + c1292Stored + `}}`
			r["SetWorkerWakeSettings"] = `{"data":{"setWorkerWakeSettings":` + c1292Stored + `}}`
			calls, out, err := runTeamTask(t, r, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, c := range *calls {
				if c.Op == tc.op {
					n++
					for _, retired := range []string{"isolated", "isolatedAt", "isolatedByHandle", "isolatedUntil", "canManageIsolation", "standbyEnabled", "standbyEffective"} {
						if strings.Contains(c.Query, retired) {
							t.Errorf("continuing %s selects retired %s", tc.op, retired)
						}
					}
				}
			}
			if n != 1 {
				t.Fatalf("%s calls %d", tc.op, n)
			}
			var dto map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"isolated", "isolatedAt", "isolatedByHandle", "isolatedUntil", "canManageIsolation", "standbyEnabled", "standbyEffective"} {
				if _, ok := dto[key]; ok {
					t.Errorf("retired output key %s: %s", key, out)
				}
			}
			want := 6
			if len(dto) != want {
				t.Errorf("continuing shape has %d keys want%d: %s", len(dto), want, out)
			}
		})
	}
}

func TestRetiredCommands1743RefuseLocally(t *testing.T) {
	for _, args := range [][]string{
		{"team", "isolation", "set", "--worker", c1292Worker},
		{"team", "isolation", "clear", "--worker", c1292Worker},
		{"team", "delivery-settings", "set", "--standby=false"},
		{"team", "delivery-settings", "set", "--standby=true"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := deliveryResponses()
			r["IsolateWorker"] = `{"data":{"isolateWorker":` + c1292Defaults + `}}`
			r["ClearWorkerIsolation"] = `{"data":{"clearWorkerIsolation":` + c1292Defaults + `}}`
			calls, _, err := runTeamTask(t, r, args...)
			if err == nil {
				t.Error("retired command accepted")
			}
			for _, c := range *calls {
				if c.Op == "IsolateWorker" || c.Op == "ClearWorkerIsolation" || c.Op == "TeamDeliverySettingsSet" {
					t.Errorf("retired command made request %s", c.Op)
				}
			}
		})
	}
}
