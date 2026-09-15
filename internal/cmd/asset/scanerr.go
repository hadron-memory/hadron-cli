package asset

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Turning the scanner's refusals into something a caller can act on (#364,
// server #896 / spec cor:dmo:060:12).
//
// The three refusals are the server's typed AssetUploadError codes —
// MALWARE_BLOCKED on completeAssetUpload, SCAN_PENDING and SCAN_BLOCKED on
// assetDownloadUrl. **They do not reach the wire as extensions.code today.**
// AssetUploadError extends Error rather than GraphQLError, and hadron-server's
// formatError maps only UrnNotQualifiedError / UrnParseError /
// AmbiguousLegacyUrnError / MemoryUnlockError — so Apollo passes the message
// through with no code of its own. Verified against prd on 2026-08-08 with the
// EICAR test file:
//
//	$ hadron asset upload eicar-test.txt -m …::experiments
//	hadron: input:3: completeAssetUpload upload rejected: file failed the malware scan
//	exit=1                       # …and --json carried no extensions.code
//
// That captured line is the PRE-#566 rendering and is kept as the dated record
// it is. The same refusal prints without the `input:3: completeAssetUpload `
// prefix now — which matters here beyond tidiness, because the prefix was
// pasted into this package's test fixtures as though the server had sent it,
// and a fixture carrying the client's own rendering could not tell a clean
// message read from a raw one. The absence of `extensions.code` is the part of
// this capture that is still live, and it is the part the code below turns on.
//
// So each refusal is matched on the typed code FIRST and on the server's
// message second. The typed branch is not speculative padding: it costs one
// line, and the day hadron-server#918 lands it takes over silently, leaving the
// message match as the compatibility path for older servers. Matching only the
// message would have to be revisited then; matching only the code would do
// nothing today.
//
// Exit codes deliberately do not change. All three are exit 1 now (no code ⇒
// codeForExtension's default), and all three stay exit 1 once the codes ship,
// because codeForExtension's default is also Error — so nothing here is a
// contract that breaks when the server catches up.

// Server-side codes, for the day they reach the wire.
const (
	codeMalwareBlocked = "MALWARE_BLOCKED"
	codeScanPending    = "SCAN_PENDING"
	codeScanBlocked    = "SCAN_BLOCKED"
)

// Message fragments the server sends today. Lowercase; matched as a
// case-insensitive substring of the SERVER's own message (api.ServerMessage,
// #566) rather than of genqlient's rendering of it.
//
// The substring match is still a substring match, and deliberately: these are
// fragments of a sentence the server composes around a filename, not whole
// messages. What changed is that the haystack no longer carries `input:3:
// completeAssetUpload ` in front of it, so a fragment can no longer match
// inside the scaffolding instead of inside the message.
const (
	msgMalwareBlocked = "failed the malware scan"
	msgScanPending    = "has not been scanned yet"
	msgScanBlocked    = "blocked by scan"
)

// uploadScanError rewrites completeAssetUpload's malware refusal. Anything else
// falls through to the ordinary mapping.
//
// Retrying is NOT suggested: the verdict is a property of the bytes, so the
// same file fails the same way every time. The blocked row is mentioned because
// it is not a leak — it is a deliberate audit tombstone whose bytes are gone —
// and a caller who doesn't know that will go looking for an upload to clean up.
func uploadScanError(err error, filename string) error {
	if !isScanRefusal(err, codeMalwareBlocked, msgMalwareBlocked) {
		return api.MapError(err)
	}
	return exitcode.Newf(exitcode.Error,
		"upload rejected: %s failed the malware scan. The bytes were discarded and a BLOCKED record was kept for audit "+
			"(`hadron asset list -m <memory> --include-deleted` shows it, with the engine signature). "+
			"Re-uploading the same file will fail the same way.", filename)
}

// downloadScanError rewrites assetDownloadUrl's two scan gates.
func downloadScanError(err error, ref string) error {
	switch {
	case isScanRefusal(err, codeScanPending, msgScanPending):
		// PENDING is never terminal: the upload succeeded and the server's
		// sweep retries on backoff, so this is "not yet", not "no".
		return exitcode.Newf(exitcode.Error,
			"%s has not finished its malware scan yet — downloads are gated until it settles. "+
				"This is not a dead end: the server retries on a backoff, so try again shortly "+
				"(`hadron asset list -m <memory>` shows the scan status).", ref)
	case isScanRefusal(err, codeScanBlocked, msgScanBlocked):
		return exitcode.Newf(exitcode.Error,
			"%s failed the malware scan and cannot be downloaded — its bytes were deleted and the record is kept for audit. "+
				"`hadron asset list -m <memory> --include-deleted` shows the engine signature.", ref)
	}
	return api.MapError(err)
}

// isScanRefusal reports whether err is the named server refusal, by typed code
// or by message.
//
// The message pass reads EVERY server message, and falls back to err.Error()
// only when there are none — a transport failure has no GraphQL message, and
// matching its raw text is no worse than before.
//
// All of them, not the first (PR #581 review, @copilot). The rendering this
// replaced was `err.Error()`, which joins the whole list, so matching only
// ServerMessage would have NARROWED the match: a scan refusal arriving behind
// another resolver's error would stop being recognised, and the caller would
// get the generic mapping instead of the actionable guidance — a regression
// introduced by a change whose entire purpose was to leave behaviour alone.
func isScanRefusal(err error, code, msgFragment string) bool {
	if err == nil {
		return false
	}
	if api.HasErrorCode(err, code) {
		return true
	}
	msgs := api.ServerMessages(err)
	if len(msgs) == 0 {
		msgs = []string{err.Error()}
	}
	for _, m := range msgs {
		if strings.Contains(strings.ToLower(m), msgFragment) {
			return true
		}
	}
	return false
}
