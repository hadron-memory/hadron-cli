package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestClearRoleGeneratedDocuments(t *testing.T) {
	literal := regexp.MustCompile(`\brole\s*:\s*null\b`)
	for name, document := range map[string]string{
		"generic": gen.UpdateNodeClearingRole_Operation,
		"spec":    gen.UpdateSpecNodeClearingRole_Operation,
		"review":  gen.UpdateReviewNodeClearingRole_Operation,
		"task":    gen.UpdateTaskNodeClearingRole_Operation,
	} {
		if !literal.MatchString(document) || strings.Count(document, "role:") != 1 {
			t.Errorf("%s clear must send exactly one literal role:null: %s", name, document)
		}
	}
	for name, document := range map[string]string{
		"generic": gen.UpdateNode_Operation, "spec": gen.UpdateSpecNode_Operation,
		"review": gen.UpdateReviewNode_Operation, "task": gen.UpdateTaskNode_Operation,
	} {
		if literal.MatchString(document) {
			t.Errorf("ordinary %s update must not clear: %s", name, document)
		}
	}
}

// A clear must carry every other explicit input in its one mutation, including
// false/zero and JSON null, while omitted fields stay absent. Reflection here
// enumerates the transport's input so a newly added field cannot be dropped
// silently by the dedicated clear operations.
func TestClearRoleOtherFieldsAndOmission(t *testing.T) {
	for _, allFields := range []bool{false, true} {
		for _, door := range []string{"Node", "SpecNode", "ReviewNode", "TaskNode"} {
			t.Run(door+map[bool]string{false: "/omit", true: "/all"}[allFields], func(t *testing.T) {
				id, text, zero, no := "n1", "verbatim", 0, false
				input := &gen.UpdateNodeInput{Id: &id}
				if allFields {
					v := reflect.ValueOf(input).Elem()
					for i := 0; i < v.NumField(); i++ {
						f := v.Field(i)
						switch f.Type() {
						case reflect.TypeOf(&text):
							f.Set(reflect.ValueOf(&text))
						case reflect.TypeOf(&zero):
							f.Set(reflect.ValueOf(&zero))
						case reflect.TypeOf(&no):
							f.Set(reflect.ValueOf(&no))
						case reflect.TypeOf((*json.RawMessage)(nil)):
							raw := json.RawMessage(`null`)
							f.Set(reflect.ValueOf(&raw))
						case reflect.TypeOf([]string{}):
							f.Set(reflect.ValueOf([]string{"tag"}))
						case reflect.TypeOf([]*gen.NodeEdgeInput{}):
							f.Set(reflect.ValueOf([]*gen.NodeEdgeInput{{}}))
						default:
							t.Fatalf("add a deliberate input fixture for %s", v.Type().Field(i).Name)
						}
					}
					revision := 7
					input.ExpectedRevision = &revision
				}
				role := ""
				cur := NodeKindState{}
				switch door {
				case "SpecNode":
					role = "spec.rule"
					cur.Role = &role
				case "ReviewNode":
					role = "review.check"
					cur.Role = &role
				case "TaskNode":
					cur.IsRunnable = true
				}
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var req struct {
						OperationName string
						Query         string
						Variables     map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.OperationName != "Update"+door+"ClearingRole" {
						t.Errorf("wrong door %s", req.OperationName)
					}
					encoded, _ := json.Marshal(input)
					var want map[string]any
					_ = json.Unmarshal(encoded, &want)
					delete(want, "role")
					if !reflect.DeepEqual(req.Variables, want) {
						t.Errorf("fields lost or unset sent: got %v want %v", req.Variables, want)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"update` + door + `":{"id":"n1","role":null}}}`))
				}))
				defer srv.Close()
				if _, err := UpdateNodeClearingRoleByKind(context.Background(), graphql.NewClient(srv.URL, srv.Client()), input, cur); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("%d mutations", calls)
				}
			})
		}
	}
}

func TestClearRoleCurrentAndResultingKindsMustShareDoor(t *testing.T) {
	role, runnable := "spec.rule", true
	// Even though role is cleared, the write touches the old spec AND the
	// resulting task. There is no single exempt door and no request may leave.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("two-kind clear sent a mutation")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := UpdateNodeClearingRoleByKind(context.Background(), graphql.NewClient(srv.URL, srv.Client()),
		&gen.UpdateNodeInput{IsRunnable: &runnable}, NodeKindState{Role: &role})
	if err == nil || !strings.Contains(err.Error(), "two governed kinds") {
		t.Fatalf("expected union refusal: %v", err)
	}
}
