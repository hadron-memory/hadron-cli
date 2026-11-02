package decision

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

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
// than being dropped. The caller decodes into its generated input struct with
// dec.Decode(&input) and then calls finishInput. The server still judges every
// value (refusing with DECISION_INVALID and a JSON pointer); fields the file
// leaves out stay nil and are omitted on the wire.
func inputDecoder(data []byte) (*json.Decoder, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, exitcode.Newf(exitcode.Usage, "--input-file must hold one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	return dec, nil
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
