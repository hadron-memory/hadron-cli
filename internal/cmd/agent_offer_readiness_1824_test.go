package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agent"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// hadron-cli#1824 (cor:dmo:050:19, cor:dmo:050:13, cor:api:240:02): `agent get` shows Agent.offerReadiness.

func agentWithReadiness(readiness string) string {
	return strings.Replace(agentJSON, `"usesTasks":true`, `"usesTasks":true,"offerReadiness":`+readiness, 1)
}

func runAgentGet1824(t *testing.T, readiness string, args ...string) (string, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"GetAgent": `{"data":{"agent":` + agentWithReadiness(readiness) + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "get", "acme.com::support-bot", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String(), captured
}

func TestAgentGet1824ReadyShowsReadyWithNoReasons(t *testing.T) {
	out, _ := runAgentGet1824(t, `{"ready":true,"reasons":[]}`)
	if !strings.Contains(out, "offer readiness: ready") {
		t.Fatalf("output: %s", out)
	}
	if strings.Contains(out, "not ready") || strings.Contains(out, "not available") {
		t.Fatalf("output: %s", out)
	}
}

func TestAgentGet1824NotReadyListsEveryReasonInServerOrderWithCounts(t *testing.T) {
	out, _ := runAgentGet1824(t, `{"ready":false,"reasons":[
		{"code":"CLONE_LINKED_MEMORY_UNREADABLE","count":25,"subjects":[{"name":"alpha-kb"},{"name":"beta-kb"}],"omittedByCap":20,"hiddenByAccess":3},
		{"code":"AGENT_NOT_PUBLIC","count":1,"subjects":[],"omittedByCap":0,"hiddenByAccess":1}]}`)
	for _, want := range []string{"offer readiness: not ready", "CLONE_LINKED_MEMORY_UNREADABLE", "AGENT_NOT_PUBLIC", "alpha-kb", "beta-kb"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
	if strings.Index(out, "CLONE_LINKED_MEMORY_UNREADABLE") > strings.Index(out, "AGENT_NOT_PUBLIC") {
		t.Fatalf("reasons reordered: %s", out)
	}
	// counts, never names, for what the server did not name
	for _, want := range []string{"25", "20 more", "3 hidden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing count %q in: %s", want, out)
		}
	}
}

func TestAgentGet1824NullReadinessIsNotAvailableNeverReady(t *testing.T) {
	out, _ := runAgentGet1824(t, `null`)
	if !strings.Contains(out, "offer readiness: not available to you") {
		t.Fatalf("output: %s", out)
	}
	if strings.Contains(out, "offer readiness: ready") || strings.Contains(strings.ToUpper(out), "FORBIDDEN") {
		t.Fatalf("a null answer must neither say ready nor invent an error code: %s", out)
	}
	js, _ := runAgentGet1824(t, `null`, "--json")
	var dto map[string]any
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatal(err)
	}
	if v, ok := dto["offerReadiness"]; !ok || v != nil {
		t.Fatalf("offerReadiness must be present and null: %s", js)
	}
}

func TestAgentGet1824JSONCarriesServerShapeWithEmptyArraysNeverNull(t *testing.T) {
	// a hostile or older server may send null lists inside a non-null object
	js, _ := runAgentGet1824(t, `{"ready":false,"reasons":[{"code":"AGENT_RETIRED","count":2,"subjects":null,"omittedByCap":0,"hiddenByAccess":2}]}`, "--json")
	var dto struct {
		OfferReadiness *struct {
			Ready   bool `json:"ready"`
			Reasons []struct {
				Code           string           `json:"code"`
				Count          int              `json:"count"`
				Subjects       []map[string]any `json:"subjects"`
				OmittedByCap   int              `json:"omittedByCap"`
				HiddenByAccess int              `json:"hiddenByAccess"`
			} `json:"reasons"`
		} `json:"offerReadiness"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil || dto.OfferReadiness == nil {
		t.Fatalf("err %v json %s", err, js)
	}
	if dto.OfferReadiness.Reasons[0].Subjects == nil || !strings.Contains(js, `"subjects":[]`) {
		t.Fatalf("subjects must be [] not null: %s", js)
	}
	rj, _ := runAgentGet1824(t, `{"ready":true,"reasons":null}`, "--json")
	if !strings.Contains(rj, `"reasons":[]`) {
		t.Fatalf("reasons must be [] not null: %s", rj)
	}
}

// The --json field names are the SDL's (AgentOfferReadiness, AgentOfferReason, AgentOfferSubject).
func TestAgentGet1824JSONFieldNamesMatchTheSDL(t *testing.T) {
	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema", Input: string(sdl)})
	if err != nil {
		t.Fatal(err)
	}
	fieldNames := func(typ string) []string {
		def := schema.Types[typ]
		if def == nil {
			t.Fatalf("SDL has no type %s", typ)
		}
		var names []string
		for _, f := range def.Fields {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		return names
	}
	jsonNames := func(v any) []string {
		var names []string
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			names = append(names, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		}
		sort.Strings(names)
		return names
	}
	for sdlType, dto := range map[string]any{
		"AgentOfferReadiness": agent.OfferReadinessDTO{},
		"AgentOfferReason":    agent.OfferReasonDTO{},
		"AgentOfferSubject":   agent.OfferSubjectDTO{},
	} {
		if got, want := jsonNames(dto), fieldNames(sdlType); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: json %v != SDL %v", sdlType, got, want)
		}
	}
	if agentField := schema.Types["Agent"].Fields.ForName("offerReadiness"); agentField == nil {
		t.Fatal("Agent.offerReadiness missing from the snapshot")
	}
}
