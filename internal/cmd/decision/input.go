package decision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// readInputFile reads --input-file from a path, or "-" for piped stdin
// (refused from a terminal, where it would hang waiting).
func readInputFile(ios *output.IOStreams, path string) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		if ios.IsInputTerminal() {
			return nil, exitcode.Newf(exitcode.Usage, `--input-file - reads piped stdin; stdin is a terminal`)
		}
		data, err = io.ReadAll(ios.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading --input-file: %v", err)
	}
	return data, nil
}

// inputDecoder opens data as exactly one JSON object for a strict decode:
// an unknown key is refused, so a typo ("titel") fails before any call rather
// than being dropped. encoding/json alone would still accept a duplicate key
// (the last one wins) and a mis-cased one ("Title"); checkKeys refuses both,
// against the target type's exact JSON names. The caller decodes into its generated input struct with
// dec.Decode(&input) and then calls finishInput. The server still judges every
// value (refusing with DECISION_INVALID and a JSON pointer); fields the file
// leaves out stay nil and are omitted on the wire.
func inputDecoder(data []byte, target any) (*json.Decoder, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, exitcode.Newf(exitcode.Usage, "--input-file must hold one JSON object")
	}
	scan := json.NewDecoder(bytes.NewReader(trimmed))
	// The scan only reads keys, so numbers stay text: as float64 an
	// out-of-range one (1e999) ends the scan early, and a duplicate after it
	// in an opaque value would slip through (#1558).
	scan.UseNumber()
	if err := scanError(checkKeys(scan, reflect.TypeOf(target), "")); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	return dec, nil
}

// scanError maps a checkKeys error. A syntax error (or truncated input) is
// left for the strict decode, which reports it with its own message; every
// other error refuses. Failing closed matters because the strict decode reads
// opaque values as json.RawMessage, so it would not see the error again and
// the rest of the file would go unscanned (#1558).
func scanError(err error) error {
	var syn *json.SyntaxError
	switch {
	case err == nil, errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return nil
	case errors.As(err, new(keyError)):
		return exitcode.Newf(exitcode.Usage, "parsing --input-file: %v", err)
	default:
		return exitcode.Newf(exitcode.Usage, "parsing --input-file: checking keys: %v", err)
	}
}

// finishInput maps the decode error and refuses trailing data.
func finishInput(dec *json.Decoder, decodeErr error) error {
	if decodeErr != nil {
		return exitcode.Newf(exitcode.Usage, "parsing --input-file: %v", decodeErr)
	}
	if _, err := dec.Token(); err != io.EOF {
		return exitcode.Newf(exitcode.Usage, "parsing --input-file: unexpected data after the JSON object")
	}
	return nil
}

// keyError is a key the input file must not carry: duplicated, or not one of
// the target type's exact JSON names.
type keyError string

func (e keyError) Error() string { return string(e) }

// checkKeys walks one JSON value, refusing a duplicate key in any object and,
// where the matching Go type is a struct, a key that is not exactly one of its
// JSON field names (encoding/json would match "Title" to "title"). path is the
// JSON pointer of the value, for the message.
func checkKeys(dec *json.Decoder, t reflect.Type, path string) error {
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
				return keyError(fmt.Sprintf("duplicate key %q at %s", key, at))
			}
			seen[key] = true
			var next reflect.Type
			if fields != nil {
				ft, ok := fields[key]
				if !ok {
					return keyError(fmt.Sprintf("unknown key %q at %s (keys are case-sensitive)", key, at))
				}
				next = ft
			} else if t != nil && t.Kind() == reflect.Map {
				next = t.Elem()
			}
			if err := checkKeys(dec, next, at); err != nil {
				return err
			}
		}
	case '[':
		var elem reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			elem = t.Elem()
		}
		for i := 0; dec.More(); i++ {
			if err := checkKeys(dec, elem, fmt.Sprintf("%s/%d", path, i)); err != nil {
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
