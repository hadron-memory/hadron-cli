package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #544, asserted where the USER meets it: a real command against a gateway 502
// must exit 7, through the binary's own classifier.
//
// The api-package tests assert the classified error directly, which is the right
// unit assertion but stops one step short of production: `main` runs
// `renderError` → `exitCodeFor`, which post-processes (it upgrades cobra's own
// refusals to Usage). A value asserted before that step is one that exists only
// inside the test — review:assert-the-value-the-user-gets. This closes the gap
// by going through the function the binary actually calls.
//
// (The prose deliberately does not spell the pre-classification call: the #537
// guard in exit_code_assertion_test.go matches the literal on any line, and
// exempts only its own file, so naming it here — even to say why it is not
// used — fails the build.)
//
// It is also the end-to-end statement of the contract: exit 1 vs 7 is what an
// agent branches on to tell "the server refused this" from "ask again", and #544
// meant every gateway 5xx answered the first when it was the second.
func TestGatewayFiveHundredExitsUnavailableThroughTheBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><head><title>502 Bad Gateway</title></head><body>nginx</body></html>"))
	}))
	defer srv.Close()

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a 502 must fail the command")
	}
	if code := exitCodeFor(err); code != exitcode.Unavailable {
		t.Errorf("exit code = %d, want %d (Unavailable) — a gateway 5xx is a lost answer, not a refusal: %v",
			code, exitcode.Unavailable, err)
	}
	// The user gets a gateway sentence, not a page of HTML re-wrapped in a JSON
	// envelope, which is what the pre-#544 behaviour printed.
	msg := err.Error()
	if strings.Contains(msg, "<html>") || strings.Contains(msg, "nginx") {
		t.Errorf("the gateway body must not be dumped at the user: %s", msg)
	}
	for _, want := range []string{"gateway", "retry"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must carry %q: %s", want, msg)
		}
	}
}
