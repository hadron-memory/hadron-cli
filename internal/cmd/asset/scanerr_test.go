package asset

import (
	"errors"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// wireErr is what genqlient hands back for a GraphQL error response: the
// message with its document-path prefix, and — today — no extensions.code,
// which is the whole reason these mappers match on text as well.
func wireErr(msg string) error {
	return gqlerror.List{&gqlerror.Error{Message: msg}}
}

// codedErr is the same refusal once hadron-server types it (hadron-server#918). The mappers
// must already recognise it, so that change needs no CLI release.
func codedErr(code, msg string) error {
	return gqlerror.List{&gqlerror.Error{
		Message:    msg,
		Extensions: map[string]any{"code": code},
	}}
}

// The exact string prd returns, captured from an EICAR upload on 2026-08-08.
const liveMalwareMsg = "input:3: completeAssetUpload upload rejected: file failed the malware scan\n"

func TestUploadScanErrorRewritesMalwareRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"as the wire carries it today", wireErr(liveMalwareMsg)},
		{"as a typed code, once the server sends one", codedErr(codeMalwareBlocked, "upload rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := uploadScanError(tc.err, "eicar-test.txt")
			if got == nil {
				t.Fatal("a refusal must stay an error")
			}
			msg := got.Error()
			if !strings.Contains(msg, "eicar-test.txt") {
				t.Errorf("name the file that was rejected: %q", msg)
			}
			if !strings.Contains(msg, "malware scan") {
				t.Errorf("say why: %q", msg)
			}
			// The verdict is a property of the bytes, so the same file fails
			// the same way — telling someone to retry wastes their time.
			if strings.Contains(strings.ToLower(msg), "try again") || strings.Contains(strings.ToLower(msg), "retry") {
				t.Errorf("must not suggest retrying an identical file: %q", msg)
			}
			// A BLOCKED row is left behind on purpose; a caller who doesn't
			// know that goes looking for an upload to clean up.
			if !strings.Contains(msg, "audit") {
				t.Errorf("mention the retained audit record: %q", msg)
			}
			if code := exitcode.FromError(got); code != exitcode.Error {
				t.Errorf("exit code should stay 1 across the typed-code change, got %d", code)
			}
		})
	}
}

func TestDownloadScanErrorRewritesBothGates(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantSubstr  []string
		wantNotable string // a phrase that must NOT appear
	}{
		{
			name:        "pending, as the wire carries it",
			err:         wireErr("input:2: assetDownloadUrl asset has not been scanned yet"),
			wantSubstr:  []string{"has not finished its malware scan", "try again"},
			wantNotable: "deleted",
		},
		{
			name:       "pending, typed",
			err:        codedErr(codeScanPending, "nope"),
			wantSubstr: []string{"try again"},
		},
		{
			name:       "blocked, as the wire carries it",
			err:        wireErr("input:2: assetDownloadUrl asset blocked by scan"),
			wantSubstr: []string{"failed the malware scan", "audit"},
		},
		{
			name:       "blocked, typed",
			err:        codedErr(codeScanBlocked, "nope"),
			wantSubstr: []string{"failed the malware scan"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := downloadScanError(tc.err, "a1")
			if got == nil {
				t.Fatal("a refusal must stay an error")
			}
			msg := got.Error()
			if !strings.Contains(msg, "a1") {
				t.Errorf("name the asset: %q", msg)
			}
			for _, want := range tc.wantSubstr {
				if !strings.Contains(msg, want) {
					t.Errorf("want %q in %q", want, msg)
				}
			}
			if tc.wantNotable != "" && strings.Contains(msg, tc.wantNotable) {
				t.Errorf("PENDING is not terminal — %q must not say %q", msg, tc.wantNotable)
			}
			if code := exitcode.FromError(got); code != exitcode.Error {
				t.Errorf("exit code should stay 1, got %d", code)
			}
		})
	}
}

// Everything that is not a scan refusal keeps its ordinary mapping — a
// not-found must not come back as a scan message.
func TestScanErrorsPassOtherErrorsThrough(t *testing.T) {
	notFound := codedErr("NOT_FOUND", "asset not found")
	if code := exitcode.FromError(downloadScanError(notFound, "a1")); code != exitcode.NotFound {
		t.Errorf("a NOT_FOUND must still map to exit 4, got %d", code)
	}
	if msg := downloadScanError(notFound, "a1").Error(); strings.Contains(msg, "malware") {
		t.Errorf("unrelated error rewritten as a scan refusal: %q", msg)
	}
	if msg := uploadScanError(wireErr("MIME_NOT_ALLOWED: text/plain is not accepted"), "x.txt").Error(); strings.Contains(msg, "malware") {
		t.Errorf("a MIME rejection is not a scan refusal: %q", msg)
	}
	// A plain transport error carries no GraphQL envelope at all.
	if msg := uploadScanError(errors.New("connection reset"), "x.txt").Error(); strings.Contains(msg, "malware") {
		t.Errorf("a transport error is not a scan refusal: %q", msg)
	}
}
