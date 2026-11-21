package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// hadron-cli#1468: pins for the response guards of #1338/#1464 whose mutants
// survived because the fixtures only nulled whole payloads. Each case uses
// valid arguments, so pre-validation cannot mask the response guard, and each
// guard has a positive normal-row control beside it. Test-only.
//
// Traceability: the helpers and the *1468 tests are hadron-cli#1468; the *2414
// tests are hadron-cli#2414; the helper polish (checked stderr, shared setup,
// subtest names, direct fixture-helper coverage) is hadron-cli#2784.

func fixtureObject1468(base, field, value string) (string, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(base), &row); err != nil {
		return "", err
	}
	if field != "" {
		if _, ok := row[field]; !ok {
			return "", fmt.Errorf("unknown fixture field %q", field)
		}
		row[field] = json.RawMessage(value)
	}
	encoded, err := json.Marshal(row)
	return string(encoded), err
}

func grantFixture1468(field, value string) (string, error) {
	row, err := fixtureObject1468(grant1338, "revokedAt", `"2026-10-31T01:00:00Z"`)
	if err != nil {
		return "", err
	}
	return fixtureObject1468(row, field, value)
}

func grant1468(t *testing.T, field, value string) string {
	t.Helper()
	row, err := grantFixture1468(field, value)
	if err != nil {
		t.Fatalf("build grant fixture: %v", err)
	}
	return row
}

func appIdentity1468(t *testing.T, field, value string) string {
	t.Helper()
	row, err := fixtureObject1468(app1338, field, value)
	if err != nil {
		t.Fatalf("build App fixture: %v", err)
	}
	return row
}

// capturedText1468 reads what a test writer captured. A writer that does not
// capture (a wrong fixture type) is an error naming its type, so a test fails
// with a diagnosis instead of panicking on a type assertion (cli#2784 F1).
func capturedText1468(w io.Writer) (string, error) {
	c, ok := w.(interface{ String() string })
	if !ok {
		return "", fmt.Errorf("writer is %T, which does not capture output (want a *strings.Builder from testFactory)", w)
	}
	return c.String(), nil
}

// refused1468 checks no success payload and one request in each mode, as a
// json=true / json=false subtest. Text mode also goes through the real error
// renderer and pins its stderr message; JSON errors normally render on stdout,
// so empty stdout is checked BEFORE error rendering, as in the original guard
// pin. The scaffold is runTeamTask's (runTeamTaskFactory); only the
// renderer-specific observations live here (cli#2784 F2).
func refused1468(t *testing.T, op, response, message string, args ...string) {
	t.Helper()
	for _, jsonMode := range []bool{true, false} {
		t.Run(fmt.Sprintf("json=%t", jsonMode), func(t *testing.T) {
			t.Helper()
			argv := append([]string{}, args...)
			if jsonMode {
				argv = append(argv, "--json")
			}
			f, calls, out, err := runTeamTaskFactory(t, map[string]string{op: response}, argv...)
			if got := exitCodeFor(err); got != 1 {
				t.Fatalf("exit=%d, want 1 (%v); stdout %q", got, err, out.String())
			}
			if out.String() != "" {
				t.Fatalf("a refused response printed a success payload: %q", out.String())
			}
			if len(*calls) != 1 || (*calls)[0].Op != op {
				t.Fatalf("want exactly one %s request, got %v", op, *calls)
			}
			if !strings.Contains(err.Error(), message) {
				t.Fatalf("refusal %q lacks %q", err, message)
			}
			if !jsonMode {
				renderError(f, err)
				stderr, cerr := capturedText1468(f.IOStreams.ErrOut)
				if cerr != nil {
					t.Fatal(cerr)
				}
				if !strings.Contains(stderr, message) {
					t.Fatalf("rendered stderr %q lacks refusal %q", stderr, message)
				}
				if out.String() != "" {
					t.Fatalf("text refusal wrote stdout: %q", out.String())
				}
			}
		})
	}
}

// accepted1468 proves that the same operation ran once and returned the row's
// actual id, not merely some non-empty output from a different command.
func accepted1468(t *testing.T, op, response, expectedID string, args ...string) {
	t.Helper()
	argv := append(append([]string{}, args...), "--json")
	calls, out, err := runTeamTask(t, map[string]string{op: response}, argv...)
	if err != nil {
		t.Fatalf("normal row must succeed: %v %q", err, out)
	}
	if len(*calls) != 1 || (*calls)[0].Op != op {
		t.Fatalf("want exactly one %s request, got %v", op, *calls)
	}
	var row struct {
		ID string `json:"id"`
	}
	switch op {
	case "AppMemoryWriteGrants":
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("want one grant, got %s", out)
		}
		row = rows[0]
	case "MyApp":
		var identity struct {
			App *struct {
				ID string `json:"id"`
			} `json:"app"`
		}
		if err := json.Unmarshal([]byte(out), &identity); err != nil {
			t.Fatal(err)
		}
		if identity.App == nil {
			t.Fatalf("normal identity is absent: %s", out)
		}
		row = *identity.App
	case "RevokeAppMemoryWriteGrant":
		if err := json.Unmarshal([]byte(out), &row); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("positive control has no output shape for %s", op)
	}
	if row.ID != expectedID {
		t.Fatalf("returned id=%q, want %q; output=%s", row.ID, expectedID, out)
	}
}

var rm1468 = []string{"app", "memory-grant", "rm", "g1", "--yes"}

const incompleteGrant1468 = "the server returned an incomplete memory write grant"
const incompleteIdentity1468 = "the server returned an incomplete App identity"

func TestMemoryGrantRm1468UnconfirmedRevocationRefused(t *testing.T) {
	accepted1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+grant1468(t, "", "")+`}}`, "g1", rm1468...)
	refused1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+grant1468(t, "revokedAt", "null")+`}}`, "the server did not confirm revocation", rm1468...)
}

func TestMemoryGrant1468IncompleteMetadataRefused(t *testing.T) {
	list := []string{"app", "memory-grant", "list", "acme.com:team"}
	accepted1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+grant1468(t, "", "")+`]}}`, "g1", list...)
	for _, field := range []string{"id", "appId", "prefix"} {
		t.Run(field, func(t *testing.T) {
			row := grant1468(t, field, `""`)
			refused1468(t, "RevokeAppMemoryWriteGrant", `{"data":{"revokeAppMemoryWriteGrant":`+row+`}}`, incompleteGrant1468, rm1468...)
			refused1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+row+`]}}`, incompleteGrant1468, list...)
		})
	}
}

func TestAppWhoami1468IncompleteIdentityRefused(t *testing.T) {
	whoami := []string{"app", "whoami"}
	accepted1468(t, "MyApp", `{"data":{"myApp":`+app1338+`}}`, "a1", whoami...)
	for _, field := range []string{"id", "urn"} {
		t.Run(field, func(t *testing.T) {
			refused1468(t, "MyApp", `{"data":{"myApp":`+appIdentity1468(t, field, `""`)+`}}`, incompleteIdentity1468, whoami...)
		})
	}
}

// cli#2414: the current list guard refuses the WHOLE list, in either order.
func TestMemoryGrant2414MixedListRefusesTheWholeAnswer(t *testing.T) {
	for _, field := range []string{"id", "appId", "prefix"} {
		for _, badFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/badFirst=%t", field, badFirst), func(t *testing.T) {
				good, bad := grant1468(t, "", ""), grant1468(t, field, `""`)
				rows := good + "," + bad
				if badFirst {
					rows = bad + "," + good
				}
				refused1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+rows+`]}}`, incompleteGrant1468, "app", "memory-grant", "list", "acme.com:team")
			})
		}
	}
}

// These are empty-string guards, not trimming rules. Preserve that behavior.
func TestAppGuards2414WhitespaceMetadataIsAccepted(t *testing.T) {
	for _, field := range []string{"id", "appId", "prefix"} {
		t.Run("grant/"+field, func(t *testing.T) {
			id := "g1"
			if field == "id" {
				id = "   "
			}
			accepted1468(t, "AppMemoryWriteGrants", `{"data":{"appMemoryWriteGrants":[`+grant1468(t, field, `"   "`)+`]}}`, id, "app", "memory-grant", "list", "acme.com:team")
		})
	}
	for _, field := range []string{"id", "urn"} {
		t.Run("whoami/"+field, func(t *testing.T) {
			id := "a1"
			if field == "id" {
				id = "   "
			}
			accepted1468(t, "MyApp", `{"data":{"myApp":`+appIdentity1468(t, field, `"   "`)+`}}`, id, "app", "whoami")
		})
	}
}

// Name/type may be empty and description may be null: only id/urn are guarded.
func TestAppWhoami2414OtherMetadataIsNotAnIdentityGuard(t *testing.T) {
	for _, tc := range []struct{ field, value string }{{"name", `""`}, {"appType", `""`}, {"description", `null`}, {"description", `""`}} {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			accepted1468(t, "MyApp", `{"data":{"myApp":`+appIdentity1468(t, tc.field, tc.value)+`}}`, "a1", "app", "whoami")
		})
	}
}

// A fixture edit that is not a known field, or whose value is not valid JSON,
// is an error rather than a silently malformed fixture. An invalid value is
// stored as a json.RawMessage, so it is rejected when the row is re-encoded
// (json.Marshal validates RawMessage), not when it is assigned (cli#2784 F7).
func TestGrantFixture2414RejectsInvalidEdits(t *testing.T) {
	for _, tc := range []struct{ name, field, value string }{{"unknown key", "not-a-grant-field", `"x"`}, {"invalid JSON value, rejected at re-encode", "id", `not-json`}} {
		t.Run(tc.name, func(t *testing.T) {
			if row, err := grantFixture1468(tc.field, tc.value); err == nil {
				t.Fatalf("invalid fixture edit produced %q without an error", row)
			}
		})
	}
}

// cli#2784 F7: the common helper and the App identity fixture reject the same
// bad edits directly (the App wrapper reports through t.Fatalf, so its base
// helper is tested here), and a malformed base row is an error too. Each case
// asserts the error its own check raises, so one check cannot stand in for
// another: an unknown key is the helper's own refusal, an invalid value is the
// re-encode's *json.MarshalerError, a malformed base is the decode's
// *json.SyntaxError (with an otherwise valid edit, so only the base can fail).
func TestFixtureObject2784RejectsBadEditsDirectly(t *testing.T) {
	unknownKey := func(err error) bool { return err != nil && strings.Contains(err.Error(), "unknown fixture field") }
	badValue := func(err error) bool { var m *json.MarshalerError; return errors.As(err, &m) }
	badBase := func(err error) bool { var syn *json.SyntaxError; return errors.As(err, &syn) }
	for _, tc := range []struct {
		name, base, field, value string
		want                     func(error) bool
	}{
		{"App unknown key", app1338, "not-an-app-field", `"x"`, unknownKey},
		{"App invalid value", app1338, "id", `not-json`, badValue},
		{"grant invalid value", grant1338, "prefix", `{`, badValue},
		{"malformed base row", `{"id":`, "id", `"a9"`, badBase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := fixtureObject1468(tc.base, tc.field, tc.value)
			if !tc.want(err) {
				t.Fatalf("bad fixture: got %q, %v (%T); want this case's own error", row, err, err)
			}
		})
	}
	// Positive control: a valid edit of a known field changes that field.
	var before, after struct {
		ID string `json:"id"`
	}
	row, err := fixtureObject1468(app1338, "id", `"a9"`)
	if err != nil || json.Unmarshal([]byte(app1338), &before) != nil || json.Unmarshal([]byte(row), &after) != nil {
		t.Fatalf("valid edit: %q %v", row, err)
	}
	if before.ID == "a9" || after.ID != "a9" {
		t.Fatalf("valid edit did not change id: before %q after %q", before.ID, after.ID)
	}
}

// cli#2784 F1: the checked stderr read fails with the writer's type instead of
// panicking, and reads a capturing writer unchanged.
type noCapture2784 struct{ io.Writer }

func TestCapturedText2784NamesAWrongWriterType(t *testing.T) {
	if _, err := capturedText1468(noCapture2784{io.Discard}); err == nil || !strings.Contains(err.Error(), "noCapture2784") {
		t.Fatalf("a non-capturing writer must be an error naming its type, got %v", err)
	}
	var b strings.Builder
	b.WriteString("refused")
	if got, err := capturedText1468(&b); err != nil || got != "refused" {
		t.Fatalf("capturing writer: %q %v", got, err)
	}
}

func TestAppGuardHelpers2414PreserveCallerArguments(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "refused", true: "accepted"}[accepted], func(t *testing.T) {
			backing := []string{"app", "whoami", "caller-owned-tail"}
			args := backing[:2]
			response := `{"data":{"myApp":` + app1338 + `}}`
			if accepted {
				accepted1468(t, "MyApp", response, "a1", args...)
			} else {
				response = `{"data":{"myApp":` + appIdentity1468(t, "id", `""`) + `}}`
				refused1468(t, "MyApp", response, incompleteIdentity1468, args...)
			}
			if backing[2] != "caller-owned-tail" {
				t.Fatalf("helper overwrote caller backing array: %q", backing)
			}
		})
	}
}
