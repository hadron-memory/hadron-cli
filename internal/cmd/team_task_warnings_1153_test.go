package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1153 (CHECKED T1 + scoped §10): the text warning sink renders each
// server-supplied value (code, text, where) through output.SafeCell, so a
// warning can neither emit live terminal controls nor forge a second
// diagnostic line. --json carries the original values.

const (
	c1153Hostile = "a\x1b[31mb‮c\nwarning: [forged] line"
	// c1153Safe is c1153Hostile rendered by SafeCell: ESC, bidi override and
	// newline each become one space.
	c1153Safe = "a [31mb c warning: [forged] line"
)

// c1153Escape is s as a JSON string literal body (control characters escaped).
func c1153Escape(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestC1153HostileWarningValuesAreEscapedInText(t *testing.T) {
	hostile := c1153Escape(t, c1153Hostile)
	for _, field := range []string{"code", "text", "where"} {
		t.Run(field, func(t *testing.T) {
			code, text, reg := `"task.future.unknown"`, `"plain"`, `{"state":"unmapped"}`
			wantLine := "warning: [task.future.unknown] plain (no task mapped)\n"
			switch field {
			case "code":
				code = hostile
				wantLine = "warning: [" + c1153Safe + "] plain (no task mapped)\n"
			case "text":
				text = hostile
				wantLine = "warning: [task.future.unknown] " + c1153Safe + " (no task mapped)\n"
			case "where":
				reg = `{"state":"mapped","taskUrn":` + hostile + `}`
				wantLine = "warning: [task.future.unknown] plain → " + c1153Safe + "\n"
			}
			w := `[{"path":"createAgentTask","code":` + code + `,"text":` + text + `,"register":` + reg + `}]`
			_, errOut, err := runTeamTaskStreams(t, c962With("AgentTaskCreate", w), "team", "task", "create", "T")
			if err != nil {
				t.Fatal(err)
			}
			if errOut != wantLine {
				t.Fatalf("stderr %q\nwant   %q", errOut, wantLine)
			}
			if strings.ContainsAny(errOut, "\x1b‮") || strings.Count(errOut, "\n") != 1 {
				t.Fatalf("live control or extra line reached stderr: %q", errOut)
			}
		})
	}
}

func TestC1153JSONWarningsKeepTheOriginalValues(t *testing.T) {
	hostile := c1153Escape(t, c1153Hostile)
	w := `[{"path":"createAgentTask","code":` + hostile + `,"text":` + hostile + `,"register":{"state":"mapped","taskUrn":` + hostile + `}}]`
	out, errOut, err := runTeamTaskStreams(t, c962With("AgentTaskCreate", w), "team", "task", "create", "T", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if errOut != "" {
		t.Errorf("--json wrote stderr: %q", errOut)
	}
	var dto struct {
		Warnings []struct {
			Code     string `json:"code"`
			Text     string `json:"text"`
			Register struct {
				TaskURN string `json:"taskUrn"`
			} `json:"register"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || len(dto.Warnings) != 1 {
		t.Fatalf("json: %v: %q", err, out)
	}
	got := dto.Warnings[0]
	if got.Code != c1153Hostile || got.Text != c1153Hostile || got.Register.TaskURN != c1153Hostile {
		t.Errorf("--json warning values changed: %+v", got)
	}
}
