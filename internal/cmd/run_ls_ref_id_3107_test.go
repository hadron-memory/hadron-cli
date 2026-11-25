package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestRunLsAgainstTheCutoverSchema3107 runs `run ls` with --app, --org and neither (the
// configured App context)
// against a server that validates the document before answering, as the real one does:
// a document that does not fit the snapshot is refused and nothing is listed.
func TestRunLsAgainstTheCutoverSchema3107(t *testing.T) {
	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, perr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if perr != nil {
		t.Fatal(perr)
	}
	for _, tt := range []struct {
		name string
		args []string
		want map[string]string
	}{
		{"app", []string{"--app", "hrn:app:acme.com:crm"}, map[string]string{"appRef": "hrn:app:acme.com:crm"}},
		{"org", []string{"--org", "acme.com"}, map[string]string{"orgRef": "acme.com"}},
		{"neither", nil, map[string]string{"appRef": "hrn:app:acme.com:active"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var vars map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					OperationName string         `json:"operationName"`
					Query         string         `json:"query"`
					Variables     map[string]any `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "application/json")
				if _, errs := gqlparser.LoadQuery(schema, body.Query); errs != nil {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{
						"message": errs.Error(), "extensions": map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}})
					return
				}
				if body.OperationName == "AppRuns" {
					vars = body.Variables
				}
				_, _ = w.Write([]byte(`{"data":{"appRuns":{"total":1,"items":[` + appRunJSON + `]}}}`))
			}))
			defer srv.Close()
			f, out := testFactory(t)
			if tt.args == nil {
				cfg, err := f.Config()
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Set("app", "hrn:app:acme.com:active"); err != nil {
					t.Fatal(err)
				}
			}
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"run", "ls", "--json", "--server", srv.URL}, tt.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("run ls %v: %v", tt.args, err)
			}
			if !strings.Contains(out.String(), "run1") {
				t.Errorf("the run is not listed:\n%s", out.String())
			}
			for _, k := range []string{"appRef", "orgRef"} {
				got, present := vars[k]
				want, wanted := tt.want[k]
				if present != wanted || (wanted && got != want) {
					t.Errorf("%s = %v (present %v), want %q (present %v)", k, got, present, want, wanted)
				}
			}
		})
	}
}
