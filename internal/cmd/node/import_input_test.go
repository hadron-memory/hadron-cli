package node

import (
	"reflect"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// TestUpdateNodeInputFromMapsAllFields locks the completeness of the
// create→update input mapping (`node import`'s upsert emulation, PR #131
// review): every field CreateNodeInput and UpdateNodeInput share must be
// carried over, so a doc field added later can't be silently dropped on the
// update path. It populates every CreateNodeInput field with a non-zero value
// via reflection and asserts the same-named UpdateNodeInput field comes back
// non-zero. Id is the one deliberate exclusion — a forced create-PK on the
// create shape, but the target *selector* on the update shape, where it is
// XOR with the (memoryId, loc) pair this mapping selects by.
func TestUpdateNodeInputFromMapsAllFields(t *testing.T) {
	in := &gen.CreateNodeInput{}
	iv := reflect.ValueOf(in).Elem()
	for i := 0; i < iv.NumField(); i++ {
		setNonZero(t, iv.Field(i))
	}

	out := updateNodeInputFrom(in)
	ov := reflect.ValueOf(out).Elem()

	for i := 0; i < iv.NumField(); i++ {
		name := iv.Type().Field(i).Name
		if name == "Id" {
			continue // create-PK, never a selector — must NOT map (id XOR memoryId+loc)
		}
		outField := ov.FieldByName(name)
		if !outField.IsValid() {
			t.Errorf("UpdateNodeInput has no field %q (CreateNodeInput has it) — the structs drifted; update updateNodeInputFrom and this test", name)
			continue
		}
		if outField.IsZero() {
			t.Errorf("updateNodeInputFrom drops field %q — every shared field must be mapped (import contract: make the node match the file)", name)
		}
	}

	// The selector-collision guard: Id must stay unset on the update input.
	if out.Id != nil {
		t.Errorf("updateNodeInputFrom must not map Id (selector XOR violation), got %q", *out.Id)
	}
}

// setNonZero fills v with an arbitrary non-zero value of its type, recursing
// through pointers, slices, and structs, so the coverage check above can tell
// "mapped" from "dropped" for any field shape genqlient generates.
func setNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		setNonZero(t, v.Elem())
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		setNonZero(t, elem)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), elem))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		setNonZero(t, key)
		val := reflect.New(v.Type().Elem()).Elem()
		setNonZero(t, val)
		v.SetMapIndex(key, val)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				setNonZero(t, v.Field(i))
			}
		}
	default:
		t.Fatalf("setNonZero: unhandled kind %s — extend the helper", v.Kind())
	}
}
