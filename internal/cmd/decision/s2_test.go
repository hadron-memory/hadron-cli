package decision

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#2639: the S2 operations are hand-written, so nothing generated checks
// them against the server's schema. These tests do, against a copy of the
// Questions S2 SDL pinned at hadron-server 17b1b787 (testdata/s2-questions.graphql).

func s2Schema(t *testing.T) *ast.Schema {
	t.Helper()
	sdl, err := os.ReadFile("testdata/s2-questions.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "s2-questions.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the S2 SDL fixture does not load: %v", gerr)
	}
	return schema
}

// TestS2OperationsMatchTheSDL validates every S2 operation: each selected
// field exists, each variable has the declared type and each argument matches.
func TestS2OperationsMatchTheSDL(t *testing.T) {
	schema := s2Schema(t)
	for name, q := range map[string]string{
		"DecisionS2":         s2DecisionQuery,
		"DecisionsS2":        s2DecisionsQuery,
		"AskDecisionS2":      s2AskMutation,
		"AmendDecisionS2":    s2AmendMutation,
		"WithdrawDecisionS2": s2WithdrawMutation,
	} {
		if _, errs := gqlparser.LoadQuery(schema, q); len(errs) > 0 {
			t.Errorf("%s does not validate against the S2 SDL: %v", name, errs)
		}
	}
}

// TestSharedOperationsMatchTheS2SDL covers the verbs that do not probe: the log
// reads and writes, verify and purge use the same generated operations on both
// servers, so each of them must still validate against the S2 SDL, or "the same
// on both" would be an assumption nothing checks.
func TestSharedOperationsMatchTheS2SDL(t *testing.T) {
	schema := s2Schema(t)
	for name, q := range map[string]string{
		"DecisionLog":             gen.DecisionLog_Operation,
		"CreateDecisionLog":       gen.CreateDecisionLog_Operation,
		"SetDecisionLogDeciders":  gen.SetDecisionLogDeciders_Operation,
		"VerifyDecision":          gen.VerifyDecision_Operation,
		"PurgeUnansweredDecision": gen.PurgeUnansweredDecision_Operation,
	} {
		if _, errs := gqlparser.LoadQuery(schema, q); len(errs) > 0 {
			t.Errorf("%s does not validate against the S2 SDL, so it is not shape-invariant: %v", name, errs)
		}
	}
}

// TestS2InputsMatchTheSDL checks that every JSON key of the S2 input structs
// is a field of the SDL's input type, and that every field of the SDL's input
// type has a key: a field one side lacks would be dropped, or refused by the
// server, with nothing else to notice.
func TestS2InputsMatchTheSDL(t *testing.T) {
	schema := s2Schema(t)
	for typeName, v := range map[string]any{
		"AskQuestionInput":              s2AskInput{},
		"AmendQuestionInput":            s2AmendInput{},
		"QuestionOptionInput":           s2OptionInput{},
		"QuestionRecommendationInput":   s2RecommendationInput{},
		"QuestionNoRecommendationInput": s2NoRecommendationInput{},
		"QuestionRefInput":              s2RefInput{},
	} {
		def := schema.Types[typeName]
		if def == nil {
			t.Fatalf("%s is not in the SDL fixture", typeName)
		}
		want := map[string]bool{}
		for _, f := range def.Fields {
			want[f.Name] = true
		}
		got := map[string]bool{}
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			key := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
			got[key] = true
			if !want[key] {
				t.Errorf("%s: the struct sends %q, which the SDL does not have", typeName, key)
			}
		}
		for k := range want {
			if !got[k] {
				t.Errorf("%s: the SDL has %q and the CLI cannot send it", typeName, k)
			}
		}
	}
}

// TestS2ResponseTypesCoverTheSelection checks the other direction: every field
// the S2 selection asks for has a place in the response structs, so none is
// fetched and silently dropped.
func TestS2ResponseTypesCoverTheSelection(t *testing.T) {
	keys := map[string]bool{}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			keys[strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]] = true
			walk(rt.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(s2Question{}))
	doc, errs := gqlparser.LoadQuery(s2Schema(t), s2DecisionQuery)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var check func(ss ast.SelectionSet)
	check = func(ss ast.SelectionSet) {
		for _, s := range ss {
			switch s := s.(type) {
			case *ast.Field:
				if s.Name != "question" && !keys[s.Name] {
					t.Errorf("the selection asks for %q and no response struct field carries it", s.Name)
				}
				check(s.SelectionSet)
			case *ast.FragmentSpread:
				check(s.Definition.SelectionSet)
			}
		}
	}
	check(doc.Operations[0].SelectionSet)
}

// refuseAmendNulls must not wave through input it cannot read: a future caller
// that skips precheck would otherwise send an amend with the bad file ignored.
func TestRefuseAmendNullsReturnsTheParseError(t *testing.T) {
	for _, in := range []string{`not json`, `[1,2]`, ``} {
		if err := refuseAmendNulls([]byte(in)); err == nil {
			t.Errorf("%q: want an error, got nil", in)
		}
	}
	if err := refuseAmendNulls([]byte(`{"background":null,"options":[]}`)); err != nil {
		t.Errorf("clearable null and [] are accepted: %v", err)
	}
}
