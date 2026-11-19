package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Shared pieces of the help-example walks (#1051, #1053, #2565, #2579). Each
// walk runs every example of a help page against a fixture server that answers
// every GraphQL operation with an empty data object; the pieces below are what
// the four walks decide the same way (cli#2601).

// walkFixture is that fixture server. It counts an operation only when the
// request names one, so a stray HTTP hit is not read as "the command reached
// the server".
type walkFixture struct {
	mu      sync.Mutex
	ops     []string
	unnamed int
	srv     *httptest.Server
}

func newWalkFixture(t *testing.T) *walkFixture {
	t.Helper()
	w := &walkFixture{}
	w.srv = httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *walkFixture) serve(rw http.ResponseWriter, r *http.Request) {
	var body struct {
		OperationName string `json:"operationName"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)
	w.mu.Lock()
	if body.OperationName != "" {
		w.ops = append(w.ops, body.OperationName)
	} else {
		w.unnamed++
	}
	w.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	_, _ = rw.Write([]byte(`{"data":{}}`))
}

// mark is the number of operations seen so far; since returns those after it.
func (w *walkFixture) mark() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.ops)
}

func (w *walkFixture) since(n int) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.ops[n:]...)
}

// isConfirmRefusal reports whether err is the confirmation prompt's refusal
// without a terminal (cmdutil.Confirm and ConfirmDeletion), the one local
// refusal before a request that the help pages document.
func isConfirmRefusal(err error) bool {
	return err != nil && exitCodeFor(err) == exitcode.Usage &&
		strings.Contains(strings.SplitN(err.Error(), "\n", 2)[0], "without --yes in non-interactive mode")
}

// knownPanic is a command that panics on the fixture's empty reply, tracked by
// an issue. It matches only the resolved command and the tracked signature, so
// a different panic of the same command, or of another command, still fails.
type knownPanic struct{ Issue, Signature string }

func matchKnownPanic(known map[string]knownPanic, resolvedPath string, recovered any) (string, bool) {
	k, ok := known[resolvedPath]
	if !ok || k.Signature == "" || !strings.Contains(fmt.Sprint(recovered), k.Signature) {
		return "", false
	}
	return k.Issue, true
}

// resolvedPath is the command path the args resolve to ("usage events"), so a
// known panic is matched on the command that ran, not on the example's text.
func resolvedPath(root *cobra.Command, args []string) string {
	c, _, err := root.Find(args)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
}
