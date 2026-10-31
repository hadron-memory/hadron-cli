package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const slackRow = `{"id":"c1","organizationId":"o1","defaultAppId":"a1","provider":"slack","status":"ACTIVE","teamId":"T1","teamName":"Acme\u001b[31m","botId":"B1","botUserId":"U1","createdAt":"c","updatedAt":"u","lastError":"PRIVATE_ERR","botToken":"PRIVATE_TOK"}`

func runSlack(t *testing.T, wire string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	srv, ops := captureGraphQL(t, map[string]string{"SlackConnections": wire, "SlackConnection": wire})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"slack"}, args...), "--server", srv.URL))
	e := root.Execute()
	return out.String(), ops, e
}

func TestSlackStatusRequestAndShape(t *testing.T) {
	out, ops, e := runSlack(t, `{"data":{"slackConnections":{"total":3,"items":[`+slackRow+`]}}}`, "status", "--org", "o1", "--limit", "1", "--offset", "1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["SlackConnections"], &vars)
	if vars["orgId"] != "o1" || vars["limit"] != float64(1) || vars["offset"] != float64(1) {
		t.Fatal(vars)
	}
	var got struct {
		Items   []map[string]any
		Total   int
		Offset  int
		HasMore bool
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatal(e)
	}
	if got.Total != 3 || !got.HasMore || got.Offset != 1 || len(got.Items) != 1 || len(got.Items[0]) != 11 {
		t.Fatal(out)
	}
	if strings.Contains(out, "PRIVATE") || strings.Contains(out, "lastError") {
		t.Fatal(out)
	}
}

func TestSlackStatusDefaultsOmitScopeAndPaging(t *testing.T) {
	_, ops, e := runSlack(t, `{"data":{"slackConnections":{"total":0,"items":[]}}}`, "status", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["SlackConnections"], &vars)
	if len(vars) != 0 {
		t.Fatal("expected omitted variables, got", vars)
	}
}

func TestSlackStatusCountOnlyAndEmpty(t *testing.T) {
	out, ops, e := runSlack(t, `{"data":{"slackConnections":{"total":4,"items":[]}}}`, "status", "--limit", "0", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["SlackConnections"], &vars)
	if v, ok := vars["limit"]; !ok || v != float64(0) {
		t.Fatal("limit 0 must be sent", vars)
	}
	if !strings.Contains(out, `"items": []`) && !strings.Contains(out, `"items":[]`) {
		t.Fatal(out)
	}
}

func TestSlackStatusHumanHintAndEscapes(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"SlackConnections": `{"data":{"slackConnections":{"total":3,"items":[` + slackRow + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"slack", "status", "--server", srv.URL})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if strings.ContainsRune(out.String(), '\x1b') || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal(out.String())
	}
}

func TestSlackStatusRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		args       []string
		want       int
	}{
		{"null-page", `{"data":{"slackConnections":null}}`, []string{"status"}, 7},
		{"null-row", `{"data":{"slackConnections":{"total":1,"items":[null]}}}`, []string{"status"}, 7},
		{"unknown-status", `{"data":{"slackConnections":{"total":1,"items":[` + strings.Replace(slackRow, `"ACTIVE"`, `"PAUSED"`, 1) + `]}}}`, []string{"status"}, 7},
		{"denied", `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`, []string{"status"}, 8},
		{"limit-over-cap", ``, []string{"status", "--limit", "201"}, 2},
		{"negative-offset", ``, []string{"status", "--offset", "-1"}, 2},
		{"blank-org", ``, []string{"status", "--org", "  "}, 2},
		{"extra-arg", ``, []string{"status", "x"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, e := runSlack(t, tc.wire, tc.args...)
			if exitCodeFor(e) != tc.want {
				t.Fatal(e, out)
			}
			if strings.Contains(out, "PRIVATE") {
				t.Fatal(out)
			}
		})
	}
}

func TestSlackGet(t *testing.T) {
	out, ops, e := runSlack(t, `{"data":{"slackConnection":`+slackRow+`}}`, "get", "c1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var vars map[string]any
	_ = json.Unmarshal(ops["SlackConnection"], &vars)
	var m map[string]any
	if vars["ref"] != "c1" || json.Unmarshal([]byte(out), &m) != nil || len(m) != 11 || m["status"] != "ACTIVE" || strings.Contains(out, "PRIVATE") {
		t.Fatal(vars, out)
	}
	if _, _, e := runSlack(t, `{"data":{"slackConnection":null}}`, "get", "nope"); exitCodeFor(e) != 4 {
		t.Fatal("null must be not-found", e)
	}
	if _, _, e := runSlack(t, ``, "get"); exitCodeFor(e) != 2 {
		t.Fatal(e)
	}
}
