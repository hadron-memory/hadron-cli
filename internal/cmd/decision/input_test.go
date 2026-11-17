package decision

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// TestScanErrorFailsClosed pins #1558: a key-scan error the strict decode
// will not see again (it reads opaque values as json.RawMessage) must refuse,
// not be skipped; only syntax errors, which the strict decode re-reports with
// its own message, are left to it.
func TestScanErrorFailsClosed(t *testing.T) {
	for name, err := range map[string]error{
		"key error":   cmdutil.KeyError(`duplicate key "title" at /title`),
		"type error":  &json.UnmarshalTypeError{Value: "number 1e999"},
		"other error": errors.New("boom"),
	} {
		got := scanError(err)
		var ec *exitcode.CodedError
		if !errors.As(got, &ec) || ec.Code != exitcode.Usage {
			t.Errorf("%s: want a usage refusal, got %v", name, got)
		}
	}
	for name, err := range map[string]error{
		"nil":            nil,
		"syntax":         &json.SyntaxError{},
		"unexpected EOF": io.ErrUnexpectedEOF,
		"EOF":            io.EOF,
	} {
		if got := scanError(err); got != nil {
			t.Errorf("%s: want it left to the strict decode, got %v", name, got)
		}
	}
}
