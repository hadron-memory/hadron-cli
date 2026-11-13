package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const tickerSettingsGetBody = `{"data":{"appTickerSettings":{"minCadenceSeconds":60,"liveCap":10,"selfSubscribeCadenceSeconds":120,"dailyWakeBudget":96,"dailyWakeBudgetDerived":true}}}`

// cli#1985: `team ticker settings get` is the read path for an App's ticker
// settings (the bare `set` no longer reads them after #1980).
func TestTeamTickerSettingsGet1985JSON(t *testing.T) {
	r := taskResponses()
	r["TeamTickerSettingsGet"] = tickerSettingsGetBody
	calls, out, err := runTeamTask(t, r, "team", "ticker", "settings", "get", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err = json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err, out)
	}
	want := map[string]any{
		"minCadenceSeconds": float64(60), "liveCap": float64(10),
		"selfSubscribeCadenceSeconds": float64(120), "dailyWakeBudget": float64(96),
		"dailyWakeBudgetDerived": true,
	}
	if !reflect.DeepEqual(dto, want) {
		t.Fatalf("dto %#v want %#v", dto, want)
	}
	n := 0
	for _, c := range *calls {
		if c.Op == "TeamTickerSettingsGet" {
			n++
			if c.Vars["appRef"] == nil || c.Vars["appRef"] == "" {
				t.Fatalf("no appRef: %#v", c.Vars)
			}
		}
		if c.Op == "TeamTickerSettingsSet" {
			t.Fatal("get must not write")
		}
	}
	if n != 1 {
		t.Fatalf("calls %d", n)
	}
}

func TestTeamTickerSettingsGet1985Text(t *testing.T) {
	for derived, want := range map[bool]string{true: "(derived)", false: "(set)"} {
		r := taskResponses()
		body := tickerSettingsGetBody
		if !derived {
			body = strings.Replace(body, `"dailyWakeBudgetDerived":true`, `"dailyWakeBudgetDerived":false`, 1)
		}
		r["TeamTickerSettingsGet"] = body
		_, out, err := runTeamTask(t, r, "team", "ticker", "settings", "get")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"60", "10", "120", "96", want} {
			if !strings.Contains(out, s) {
				t.Fatalf("missing %q in %q", s, out)
			}
		}
	}
}

func TestTeamTickerSettingsGet1985Refusals(t *testing.T) {
	for code, exit := range map[string]int{"FORBIDDEN": 8, "APP_NOT_FOUND": 4} {
		r := taskResponses()
		r["TeamTickerSettingsGet"] = `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
		_, out, e := runTeamTask(t, r, "team", "ticker", "settings", "get")
		if exitCodeFor(e) != exit {
			t.Fatalf("%s: %v", code, e)
		}
		if strings.Contains(out, "ticker settings") {
			t.Fatalf("out %q", out)
		}
	}
}

func TestTeamTickerSettingsSetHelpPointsToGet1985(t *testing.T) {
	_, out, _ := runTeamTask(t, taskResponses(), "team", "ticker", "settings", "set", "--help")
	if !strings.Contains(out, "settings get") {
		t.Fatalf("set help does not point to get: %q", out)
	}
}
