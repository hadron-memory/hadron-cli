package cmdutil

// Strict JSON key checking shared by the commands that read a JSON file into a
// generated input type (decision --input-file, team task create --batch-file).
// encoding/json alone accepts a duplicate key (the last wins) and a mis-cased
// one ("Title" for "title"); CheckKeys refuses both against the target type's
// exact JSON names.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// KeyError is a key the input file must not carry: duplicated, or not one of
// the target type's exact JSON names.
type KeyError string

func (e KeyError) Error() string { return string(e) }

// CheckKeys walks one JSON value, refusing a duplicate key in any object and,
// where the matching Go type is a struct, a key that is not exactly one of its
// JSON field names (encoding/json would match "Title" to "title"). path is the
// JSON pointer of the value, for the message.
func CheckKeys(dec *json.Decoder, t reflect.Type, path string) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		fields := jsonFields(t)
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := kt.(string)
			at := path + "/" + key
			if seen[key] {
				return KeyError(fmt.Sprintf("duplicate key %q at %s", key, at))
			}
			seen[key] = true
			var next reflect.Type
			if fields != nil {
				ft, ok := fields[key]
				if !ok {
					return KeyError(fmt.Sprintf("unknown key %q at %s (keys are case-sensitive)", key, at))
				}
				next = ft
			} else if t != nil && t.Kind() == reflect.Map {
				next = t.Elem()
			}
			if err := CheckKeys(dec, next, at); err != nil {
				return err
			}
		}
	case '[':
		var elem reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			elem = t.Elem()
		}
		for i := 0; dec.More(); i++ {
			if err := CheckKeys(dec, elem, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // the closing delimiter
	return err
}

// jsonFields maps a struct type's exact JSON names to their field types; nil
// for a non-struct (a map, json.RawMessage or scalar is not name-checked).
func jsonFields(t reflect.Type) map[string]reflect.Type {
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}
