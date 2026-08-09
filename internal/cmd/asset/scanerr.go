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
// case-insensitive substring, because the wire message carries a GraphQL path
// prefix ("input:3: completeAssetUpload …") around them.
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
func isScanRefusal(err error, code, msgFragment string) bool {
	if err == nil {
		return false
	}
	if api.HasErrorCode(err, code) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), msgFragment)
}
