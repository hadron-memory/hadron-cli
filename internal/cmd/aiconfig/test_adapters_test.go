package aiconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Value copying: the adapters drop nil knob entries and keep the rest, and a
// missing style stays nil. Key coverage (that every generated field has a DTO
// key) is a different question and has its own tests below; one passing does
// not stand for the other.
func TestResultAdaptersKeepNilAndNonNilKnobs(t *testing.T) {
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

// A new generated top-level field must at least have a stable DTO key. Value
// preservation is tested separately by the command fixtures; typed adapters
// only make renamed/retyped fields fail compilation.
func TestResultDocumentFieldsHaveStableJSONKeys(t *testing.T) {
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

// The nested documents (error, used, knobs) get the same freshness check
// (cli#2364 item 5). The oracle marshals a DTO whose nested values are PRESENT
// but zero, so every nested DTO key shows up as a JSON key whatever its value.
// That only works while the DTO fields carry no omitempty: a future omitempty
// would drop a zero-valued key from the oracle, so a real key would read as
// missing (or the check would be loosened and hide a real gap). The tripwire
// below fails the day one appears and points back here, so the oracle is
// extended with non-zero values instead of being silently weakened.
func TestResultNestedDocumentFieldsHaveStableJSONKeys(t *testing.T) {
	raw, err := json.Marshal(testConfigDTO{Error: &testErrorDTO{}, Knobs: []testKnobDTO{{}}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Error json.RawMessage   `json:"error"`
		Used  json.RawMessage   `json:"used"`
		Knobs []json.RawMessage `json:"knobs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Knobs) != 1 {
		t.Fatalf("oracle shape changed: want one knob, got %d in %s", len(doc.Knobs), raw)
	}
	keysOf := func(label string, doc json.RawMessage) map[string]bool {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(doc, &m); err != nil {
			t.Fatalf("%s is not an object in the oracle: %v (%s)", label, err, doc)
		}
		keys := map[string]bool{}
		for k := range m {
			keys[k] = true
		}
		return keys
	}
	nested := map[string]map[string]bool{
		"Error": keysOf("error", doc.Error),
		"Used":  keysOf("used", doc.Used),
		"Knobs": keysOf("knobs[0]", doc.Knobs[0]),
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[defaultTestResult](), reflect.TypeFor[probeTestResult]()} {
		for fieldName, want := range nested {
			field, ok := typ.FieldByName(fieldName)
			if !ok {
				t.Errorf("%s has no %s field: the nested check no longer covers it", typ.Name(), fieldName)
				continue
			}
			elem := field.Type
			for elem.Kind() == reflect.Pointer || elem.Kind() == reflect.Slice {
				elem = elem.Elem()
			}
			if elem.Kind() != reflect.Struct {
				t.Errorf("%s.%s is %s, not a struct document", typ.Name(), fieldName, field.Type)
				continue
			}
			for i := 0; i < elem.NumField(); i++ {
				key := strings.Split(elem.Field(i).Tag.Get("json"), ",")[0]
				if key == "" || key == "-" {
					continue
				}
				if !want[key] {
					t.Errorf("%s.%s.%s (%s) has no stable JSON field in the DTO", typ.Name(), fieldName, elem.Field(i).Name, key)
				}
			}
		}
	}
}

// The tripwire for the oracle above: no DTO field may carry omitempty.
func TestResultDTOFieldsHaveNoOmitEmptyForTheKeyOracle(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[testConfigDTO](), reflect.TypeFor[testErrorDTO](), reflect.TypeFor[testUsedDTO](), reflect.TypeFor[testKnobDTO]()} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.Contains(typ.Field(i).Tag.Get("json"), "omitempty") {
				t.Errorf("%s.%s has omitempty: a zero value would vanish from the key oracle in TestResultNestedDocumentFieldsHaveStableJSONKeys; extend that oracle with non-zero values before adding it", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

// cli#2364 item 4: the knob copy is safe against a nil entry. Both typed
// adapters filter nils today, so this guards a future adapter that does not;
// the rest keep their order and an empty list renders as [] (never null).
func TestKnobDTOsSkipsNilEntriesAndKeepsOrder(t *testing.T) {
	detail := "d"
	got := knobDTOs([]*testKnobDTO{nil, {Param: "temperature", State: "ACCEPTED"}, nil, {Param: "top_p", State: "IGNORED", Detail: &detail}, nil})
	if len(got) != 2 || got[0].Param != "temperature" || got[1].Param != "top_p" || got[1].Detail == nil || *got[1].Detail != "d" {
		t.Fatalf("nil entries not skipped, or order/values changed: %+v", got)
	}
	for _, in := range [][]*testKnobDTO{nil, {}, {nil, nil}} {
		out := knobDTOs(in)
		raw, err := json.Marshal(out)
		if err != nil || string(raw) != "[]" {
			t.Fatalf("%v must render as [], got %s (%v)", in, raw, err)
		}
	}
}
