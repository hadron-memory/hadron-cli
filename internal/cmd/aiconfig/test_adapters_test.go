package aiconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTestResultAdaptersKeepNilAndNonNilKnobs(t *testing.T) {
	if apiStyleString(nil) != nil || fromDefaultTestResult(nil) != nil || fromProbeTestResult(nil) != nil {
		t.Fatal("nil input invented an answer")
	}
	const raw = `{"apiStyle":null,"knobs":[null,{"param":"temperature","state":"ACCEPTED","detail":null}]}`
	var d defaultTestResult
	var p probeTestResult
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*testResult{"default": fromDefaultTestResult(&d), "probe": fromProbeTestResult(&p)} {
		if got.APIStyle != nil || got.Knobs == nil || len(got.Knobs) != 1 || got.Knobs[0] == nil || got.Knobs[0].Param != "temperature" || got.Knobs[0].Detail != nil {
			t.Fatalf("%s: nil filter/style changed: %+v", name, got)
		}
	}
}

// A new generated top-level field must be consciously mapped into the stable
// DTO; typed adapters only make renamed/retyped fields fail compilation.
func TestTestResultAdapterDocumentFieldsReachJSON(t *testing.T) {
	raw, err := json.Marshal(testConfigDTO{})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[defaultTestResult](), reflect.TypeFor[probeTestResult]()} {
		for i := 0; i < typ.NumField(); i++ {
			key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			if _, ok := keys[key]; !ok {
				t.Errorf("%s.%s (%s) has no stable JSON field", typ.Name(), typ.Field(i).Name, key)
			}
		}
	}
}
