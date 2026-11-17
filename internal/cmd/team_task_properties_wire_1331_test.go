package cmd

// hadron-cli#1331 — once the schema snapshot carries `properties` (server#2420),
// the generated inputs must keep the strict-patch wire: an unnamed field is
// ABSENT (the server reads it as "unchanged"), and only an explicit null clears.
// An always-null key would clear a task's properties on every unrelated update
// against a new server and fail validation against one without the field.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestTaskPropertiesOmittedUnlessNamed1331(t *testing.T) {
	// The existing commands never name properties, so no key may appear.
	calls, _, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "update", "AT1.2", "--title", "x")
	if err != nil {
		t.Fatal(err)
	}
	var upd []attnCall
	for _, c := range *calls {
		if c.Op == "AgentTaskUpdate" {
			upd = append(upd, c)
		}
	}
	if len(upd) != 1 || strings.Contains(string(upd[0].RawVars), "properties") {
		t.Fatalf("task update sent a properties key: %v", upd)
	}
	calls, _, err = runTeamTask(t, taskResponses(), "team", "task", "create", "T")
	if err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			creates++
			if strings.Contains(string(c.RawVars), "properties") {
				t.Fatalf("task create sent a properties key: %s", c.RawVars)
			}
		}
	}
	if creates != 1 {
		t.Fatalf("AgentTaskCreate requests = %d, want exactly 1", creates)
	}
}

// wireOf runs one generated operation against a capturing server and returns
// the request's variables, as the server would read them.
func wireOf(t *testing.T, call func(ctx context.Context, c graphql.Client) error) string {
	t.Helper()
	var vars string
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vars = string(body.Variables)
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()
	// The server answers data:null, so the generated call may report a missing
	// result: that is not the failure under test. A transport or request-build
	// error is, and so is anything but exactly one request on the wire.
	if err := call(context.Background(), graphql.NewClient(srv.URL, srv.Client())); err != nil && requests == 0 {
		t.Fatalf("the operation never reached the server: %v", err)
	}
	if requests != 1 {
		t.Fatalf("%d requests captured, want exactly 1", requests)
	}
	return vars
}

func TestGeneratedPropertiesInputWire1331(t *testing.T) {
	clear := json.RawMessage("null")
	value := json.RawMessage(`{"a":1}`)
	upd := func(p json.RawMessage) string {
		return wireOf(t, func(ctx context.Context, c graphql.Client) error {
			_, err := gen.AgentTaskUpdate(ctx, c, "t1", &gen.UpdateAgentTaskInput{Properties: p})
			return err
		})
	}
	if got := upd(nil); strings.Contains(got, "properties") {
		t.Fatalf("nil update properties must be omitted: %s", got)
	}
	if got := upd(clear); !strings.Contains(got, `"properties":null`) {
		t.Fatalf("RawMessage(null) must clear: %s", got)
	}
	if got := upd(value); !strings.Contains(got, `"properties":{"a":1}`) {
		t.Fatalf("a value must be sent as is: %s", got)
	}
	crt := func(p *json.RawMessage) string {
		return wireOf(t, func(ctx context.Context, c graphql.Client) error {
			_, err := gen.AgentTaskCreate(ctx, c, &gen.CreateAgentTaskInput{Title: "T", Properties: p})
			return err
		})
	}
	if got := crt(nil); strings.Contains(got, "properties") {
		t.Fatalf("nil create properties must be omitted: %s", got)
	}
	if got := crt(&value); !strings.Contains(got, `"properties":{"a":1}`) {
		t.Fatalf("create value: %s", got)
	}
}
