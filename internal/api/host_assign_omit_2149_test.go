package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Provider pinning adds optional S1b input fields to an existing command.
// Nil must remain omitted for servers predating those fields.
func TestHostAssignmentOmitsAllUnsetOptionalFields2149(t *testing.T) {
	in := gen.AssignHostWorkerInput{}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(b, &values); err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(in)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Pointer {
			continue
		}
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if !strings.Contains(tag, ",omitempty") {
			t.Errorf("%s loses omission on schema refresh", f.Name)
		}
		if _, exists := values[name]; exists {
			t.Errorf("unset %s sent to older provider", name)
		}
	}
}
