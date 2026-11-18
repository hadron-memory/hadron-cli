package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#1468: pins for the response guards of #1338/#1464 whose mutants
// survived because the fixtures only nulled whole payloads. Each case uses
// valid arguments, so pre-validation cannot mask the response guard, and each
// guard has a positive normal-row control beside it. Test-only.

const revokedGrant1468 = `"revokedAt":"2026-10-31T01:00:00Z"`

func grant1468(field, value string) string {
	g := strings.Replace(grant1338, `"revokedAt":null`, revokedGrant1468, 1)
	if field == "" {
		return g
	}
	old := map[string]string{"id": `"id":"g1"`, "appId": `"appId":"a1"`, "prefix": `"prefix":"reports"`, "revokedAt": revokedGrant1468}[field]
	return strings.Replace(g, old, `"`+field+`":`+value, 1)
}

// refused1468 asserts exit 1, empty stdout and exactly one request of op.
func refused1468(t *testing.T, op, response string, args ...string) {
	t.Helper()
	calls, out, err := runTeamTask(t, map[string]string{op: response}, append(args, "--json")...)
	if got := exitCodeFor(err); got != 1 {
		t.Fatalf("exit = %d, want 1 (%v); stdout %q", got, err, out)
	}
	if out != "" {
		t.Fatalf("a refused response must print nothing on stdout, got %q", out)
	}
	if len(*calls) != 1 || (*calls)[0].Op != op {
		t.Fatalf("want exactly one %s request, got %v", op, *calls)
	}
}

// accepted1468 is the positive control: the same command and op with a normal
// row exits 0 and prints the document.
func accepted1468(t *testing.T, op, response string, args ...string) {
	t.Helper()
	_, out, err := runTeamTask(t, map[string]string{op: response}, append(args, "--json")...)
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("normal row must succeed: %v %q", err, out)
	}
}

var rm1468 = []string{"app", "memory-grant", "rm", "g1", "--yes"}

// Item 1: `memory-grant rm` refuses a grant that comes back with revokedAt
// null; it must not read as revoked (memory_grants.go, the RevokedAt guard).
func TestMemoryGrantRm1468UnconfirmedRevocationRefused(t *testing.T) {
	accepted1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+grant1468("", "")+`}}`, rm1468...)
	refused1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+grant1468("revokedAt", "null")+`}}`, rm1468...)
}

// Item 2: a grant row with an empty id, appId or prefix is refused
// (memoryGrantFrom), one field at a time so each disjunct is pinned, on
// rm and list alike.
func TestMemoryGrant1468IncompleteMetadataRefused(t *testing.T) {
	list := []string{"app", "memory-grant", "list", "acme.com:team"}
	accepted1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+grant1468("", "")+`]}}`, list...)
	for _, field := range []string{"id", "appId", "prefix"} {
		t.Run(field, func(t *testing.T) {
			row := grant1468(field, `""`)
			refused1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+row+`}}`, rm1468...)
			refused1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+row+`]}}`, list...)
		})
	}
}

// Item 2: `app whoami` refuses an identity with an empty id or urn
// (identity_asset.go), one field at a time.
func TestAppWhoami1468IncompleteIdentityRefused(t *testing.T) {
	whoami := []string{"app", "whoami"}
	accepted1468(t, "MyApp", `{"data":{"myApp":`+app1338+`}}`, whoami...)
	for field, old := range map[string]string{"id": `"id":"a1"`, "urn": `"urn":"hrn:app:acme.com:team"`} {
		t.Run(field, func(t *testing.T) {
			app := strings.Replace(app1338, old, `"`+field+`":""`, 1)
			refused1468(t, "MyApp", `{"data":{"myApp":`+app+`}}`, whoami...)
		})
	}
}
