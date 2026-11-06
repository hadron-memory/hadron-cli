package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const findingFileBytes = 4 * 1024 * 1024

var findingID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// readTaskFindings reads a bounded, strict JSON array once. Errors name only
// the location/field category, never user text or parser excerpts.
func readTaskFindings(f *cmdutil.Factory, path string) ([]*gen.AgentTaskFindingInput, error) {
	fail := func(s string) ([]*gen.AgentTaskFindingInput, error) {
		return nil, exitcode.Newf(exitcode.Usage, "--findings-file: %s", s)
	}
	if path == "" {
		return fail("path must not be empty")
	}
	var r io.Reader
	if path == "-" {
		if f.IOStreams.IsInputTerminal() {
			return fail("stdin must be piped, not interactive")
		}
		r = f.IOStreams.In
		if r == nil {
			return fail("stdin is not available")
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return fail("cannot open input file")
		}
		defer func() { _ = file.Close() }()
		r = file
	}
	data, err := io.ReadAll(io.LimitReader(r, findingFileBytes+1))
	if err != nil {
		return fail("cannot read input")
	}
	if len(data) > findingFileBytes {
		return fail("input exceeds 4 MiB")
	}
	rows, err := parseTaskFindings(data)
	if err != nil {
		return fail(err.Error())
	}
	return rows, nil
}

// validFindingEscapes rejects lone surrogate escapes before encoding/json
// replaces them with U+FFFD. A valid literal replacement rune remains legal.
func validFindingEscapes(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		v, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil || v == 0 {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func parseTaskFindings(data []byte) ([]*gen.AgentTaskFindingInput, error) {
	bad := func(index int, field string) error { return fmt.Errorf("finding %d: invalid %s", index, field) }
	if !validFindingEscapes(data) {
		return nil, fmt.Errorf("invalid UTF-8, NUL or surrogate escape")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("expected one JSON array")
	}
	rows := make([]*gen.AgentTaskFindingInput, 0)
	ids := map[string]bool{}
	withID := 0
	for dec.More() {
		index := len(rows)
		if index >= 100 {
			return nil, fmt.Errorf("at most 100 findings")
		}
		token, err = dec.Token()
		if err != nil || token != json.Delim('{') {
			return nil, bad(index, "object")
		}
		row := &gen.AgentTaskFindingInput{}
		seen := map[string]bool{}
		for dec.More() {
			key, e := dec.Token()
			if e != nil {
				return nil, bad(index, "object")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return nil, bad(index, "duplicate field")
			}
			seen[name] = true
			if name != "id" && name != "severity" && name != "text" {
				return nil, bad(index, "unknown field")
			}
			value, e := dec.Token()
			if e != nil {
				return nil, bad(index, name)
			}
			str, isString := value.(string)
			switch name {
			case "id":
				if value == nil {
					continue
				}
				if !isString || !findingID.MatchString(str) || ids[str] {
					return nil, bad(index, "id")
				}
				ids[str] = true
				withID++
				row.Id = &str
			case "severity":
				if !isString || (str != "P1" && str != "P2" && str != "P3" && str != "note") {
					return nil, bad(index, "severity")
				}
				row.Severity = gen.AgentTaskFindingSeverity(str)
			case "text":
				if !isString || utf8.RuneCountInString(str) > 2000 || strings.ContainsRune(str, 0) {
					return nil, bad(index, "text")
				}
				row.Text = str
			}
		}
		token, err = dec.Token()
		if err != nil || token != json.Delim('}') || !seen["severity"] || !seen["text"] {
			return nil, bad(index, "required fields/object")
		}
		rows = append(rows, row)
	}
	token, err = dec.Token()
	if err != nil || token != json.Delim(']') {
		return nil, fmt.Errorf("invalid array")
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected end of input")
	}
	if withID != 0 && withID != len(rows) {
		return nil, fmt.Errorf("give every finding id, or none")
	}
	return rows, nil
}
