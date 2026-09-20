package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// registerEntryJSON is one narrow entry — an attendee named explicitly.
const registerEntryJSON = `{
	"id":"reg_1","channelId":"chn_1","attendeeUrn":"hrn:worker:acme.com:eng:iris",
	"role":"WATCH","mentionOnly":false,"installDefault":false,"description":null,
	"ownerType":"APP","ownerId":"app_1","organizationId":"org_1",
	"createdAt":"2026-09-20T00:00:00Z","updatedAt":null}`

// registerEntryWideJSON is the every-attendee row: attendeeUrn is NULL, which
// is a real answer (the wide grant) rather than a missing field.
const registerEntryWideJSON = `{
	"id":"reg_2","channelId":"chn_1","attendeeUrn":null,
	"role":"BOTH","mentionOnly":true,"installDefault":false,"description":"whole team",
	"ownerType":"APP","ownerId":"app_1","organizationId":"org_1",
	"createdAt":"2026-09-20T00:00:00Z","updatedAt":null}`

// inputOf pulls the `input` variable out of a captured register mutation.
// Returns present=false when the operation never ran, so a refusal that
// short-circuits before the request is distinguishable from one that sent a
// request and got nothing back.
func inputOf(t *testing.T, captured map[string]json.RawMessage, op string) (in map[string]any, present bool) {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		return nil, false
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("unmarshal %s variables: %v", op, err)
	}
	m, _ := vars["input"].(map[string]any)
	return m, true
}

// #636 — a named attendee reaches the wire verbatim. Refs are NOT classified
// client-side anywhere in this package, so an id and a URN must both pass
// through unexamined.
func TestChannelRegisterAddNamedAttendee(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateRegisterEntry": `{"data":{"createRegisterEntry":` + registerEntryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "add",
		"--channel", "hrn:node:acme.com:team-shared:chats:standup",
		"--owner", "hrn:app:acme.com:eng-team",
		"--attendee", "hrn:worker:acme.com:eng:iris",
		"--role", "watch", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, present := inputOf(t, captured, "CreateRegisterEntry")
	if !present {
		t.Fatal("CreateRegisterEntry must be called")
	}
	if in["attendeeRef"] != "hrn:worker:acme.com:eng:iris" {
		t.Errorf("attendeeRef = %v, want the ref verbatim", in["attendeeRef"])
	}
	if in["channelRef"] != "hrn:node:acme.com:team-shared:chats:standup" {
		t.Errorf("channelRef = %v, want the address verbatim", in["channelRef"])
	}
	if in["role"] != "WATCH" {
		t.Errorf("role = %v, want WATCH", in["role"])
	}
	var dto struct {
		AttendeeURN *string `json:"attendeeUrn"`
		Role        string  `json:"role"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if dto.AttendeeURN == nil || *dto.AttendeeURN != "hrn:worker:acme.com:eng:iris" {
		t.Errorf("attendeeUrn = %v", dto.AttendeeURN)
	}
}

// THE assertion this command exists for (@Ada's ruling on #636).
//
// --all-attendees is the WIDE grant, and on the wire it is the ABSENCE of
// attendeeRef — that is what the server reads as "every attendee". Asserted by
// KEY PRESENCE: `"attendeeRef": null` and an absent key decode identically
// into a nil pointer, so a value test would pass with the omitempty dropped
// and the wire contract broken.
func TestChannelRegisterAddAllAttendeesOmitsTheRef(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateRegisterEntry": `{"data":{"createRegisterEntry":` + registerEntryWideJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "add",
		"--channel", "chn_1", "--owner", "hrn:app:acme.com:eng-team",
		"--all-attendees", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, _ := inputOf(t, captured, "CreateRegisterEntry")
	if _, sent := in["attendeeRef"]; sent {
		t.Errorf("--all-attendees must OMIT attendeeRef, not send it: %v", in)
	}
	// The wide row renders as an answer, not a blank cell.
	if !strings.Contains(out.String(), `"attendeeUrn": null`) {
		t.Errorf("the wide row's null attendeeUrn must survive into --json:\n%s", out.String())
	}
}

// The wide grant must never be the result of a forgotten flag. Each of these
// is refused (exit 2) AND must refuse before the request goes out — a widening
// that reached the server would already have happened.
func TestChannelRegisterAddRequiresAnAttendeeChoice(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"neither", []string{"--channel", "c1", "--owner", "o1"}},
		{"both", []string{"--channel", "c1", "--owner", "o1", "--attendee", "w1", "--all-attendees"}},
		{"attendee asked for nothing", []string{"--channel", "c1", "--owner", "o1", "--attendee="}},
		// THE case that separates Changed() from a value test. Keyed on the
		// value, `--attendee=` reads as "did not ask", so this falls through to
		// the wide grant and SUCCEEDS — the exact widening-by-forgetting the
		// --all-attendees flag exists to prevent. The three cases above all
		// refuse under either implementation, so without this one the guard
		// could be keyed wrongly and every assertion would still pass.
		{"attendee emptied AND all-attendees", []string{"--channel", "c1", "--owner", "o1", "--attendee=", "--all-attendees"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateRegisterEntry": `{"data":{"createRegisterEntry":` + registerEntryJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"channel", "register", "add"}, append(tc.args, "--server", gql.URL)...))
			err := root.Execute()
			if err == nil {
				t.Fatal("should fail")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			if _, ran := captured["CreateRegisterEntry"]; ran {
				t.Error("a refused registration must not have reached the server")
			}
		})
	}
}

// An unset --role is OMITTED so the server's own default applies; the client
// does not pick one. Same for the other optional fields.
func TestChannelRegisterAddOmitsUnsetOptionals(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateRegisterEntry": `{"data":{"createRegisterEntry":` + registerEntryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "add", "--channel", "c1", "--owner", "o1",
		"--attendee", "w1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, _ := inputOf(t, captured, "CreateRegisterEntry")
	for _, k := range []string{"role", "mentionOnly", "description"} {
		if _, sent := in[k]; sent {
			t.Errorf("%q must be ABSENT when not asked for, got %v", k, in)
		}
	}
}

// `set` sends ONLY the fields that changed: an omitted one preserves, where an
// explicit null would clear. Key presence again, for the same reason.
func TestChannelRegisterSetSendsOnlyChangedFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateRegisterEntry": `{"data":{"updateRegisterEntry":` + registerEntryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "set", "reg_1", "--role", "watch",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, _ := inputOf(t, captured, "UpdateRegisterEntry")
	if in["role"] != "WATCH" {
		t.Errorf("role = %v, want WATCH", in["role"])
	}
	for _, k := range []string{"mentionOnly", "description"} {
		if _, sent := in[k]; sent {
			t.Errorf("%q must be ABSENT (preserve), not sent as null (clear): %v", k, in)
		}
	}
}

// --mention-only=false must be SENT, not treated as unset. A bool flag's zero
// value is indistinguishable from its absence by value, so this is the case a
// Changed()-blind implementation gets wrong: turning the setting off would
// silently do nothing.
func TestChannelRegisterSetMentionOnlyFalseIsSent(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateRegisterEntry": `{"data":{"updateRegisterEntry":` + registerEntryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "set", "reg_1", "--mention-only=false",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	in, _ := inputOf(t, captured, "UpdateRegisterEntry")
	v, sent := in["mentionOnly"]
	if !sent {
		t.Fatalf("--mention-only=false must be sent, not omitted: %v", in)
	}
	if v != false {
		t.Errorf("mentionOnly = %v, want false", v)
	}
}

// A `set` naming no field would be accepted by the server and return the row
// unchanged, which reads as success. The likely cause is a mistyped flag, so
// refuse rather than report a no-op as a change.
func TestChannelRegisterSetRefusesANoOp(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateRegisterEntry": `{"data":{"updateRegisterEntry":` + registerEntryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "set", "reg_1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a set with no fields should fail")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Fatalf("want exit %d (usage), got %d", exitcode.Usage, got)
	}
	if _, ran := captured["UpdateRegisterEntry"]; ran {
		t.Error("a no-op set must not have reached the server")
	}
}

// The filters forward verbatim and AND together.
func TestChannelRegisterListForwardsFilters(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":1,"items":[` + registerEntryJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list",
		"--channel", "hrn:node:acme.com:team-shared:chats:team",
		"--attendee", "hrn:worker:acme.com:eng:iris",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RegisterEntries"], &vars)
	filter, _ := vars["filter"].(map[string]any)
	if filter["channelRef"] != "hrn:node:acme.com:team-shared:chats:team" {
		t.Errorf("channelRef = %v", filter["channelRef"])
	}
	if filter["attendeeRef"] != "hrn:worker:acme.com:eng:iris" {
		t.Errorf("attendeeRef = %v", filter["attendeeRef"])
	}
	// ownerRef was not asked for, so it must not narrow the query.
	if _, sent := filter["ownerRef"]; sent {
		t.Errorf("ownerRef must be absent when unset: %v", filter)
	}
	if !strings.Contains(out.String(), "reg_1") {
		t.Errorf("row missing:\n%s", out.String())
	}
}

// An unfiltered list sends no filter at all.
func TestChannelRegisterListUnfilteredSendsNoFilter(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":0,"items":[]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RegisterEntries"], &vars)
	if _, sent := vars["filter"]; sent {
		t.Errorf("an unfiltered list must send no filter, got %v", vars["filter"])
	}
}

// An empty page under a filter is ambiguous — nothing registered, or a ref
// that named nothing (the server matches nothing rather than refusing). The
// note goes to STDERR so it cannot corrupt --json.
func TestChannelRegisterListEmptyUnderFilterExplainsItself(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":0,"items":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--channel", "typo", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "matches nothing") {
		t.Errorf("an empty filtered page must explain the ambiguity on stderr")
	}
	// --json stays a clean empty array.
	var entries []any
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil {
		t.Fatalf("stdout must stay valid JSON: %v\n%s", err, out.String())
	}
	if len(entries) != 0 {
		t.Errorf("want [], got %v", entries)
	}
}

// deleteRegisterEntry returns a BOOLEAN. false means nothing was removed, and
// discarding it would report success (exit 0) on a failed removal — automation
// would then treat a live permission as withdrawn.
func TestChannelRegisterRmFalseIsNotSuccess(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetRegisterEntry":    `{"data":{"registerEntry":` + registerEntryJSON + `}}`,
		"DeleteRegisterEntry": `{"data":{"deleteRegisterEntry":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "rm", "reg_1", "--yes", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a false delete must not be reported as success")
	}
	if got := renderError(f, err); got != exitcode.NotFound {
		t.Fatalf("want exit %d (not found), got %d", exitcode.NotFound, got)
	}
}

// Non-interactive removal without --yes must refuse rather than proceed.
func TestChannelRegisterRmNeedsYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetRegisterEntry":    `{"data":{"registerEntry":` + registerEntryJSON + `}}`,
		"DeleteRegisterEntry": `{"data":{"deleteRegisterEntry":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "rm", "reg_1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("rm without --yes should refuse when not on a TTY")
	}
	if _, ran := captured["DeleteRegisterEntry"]; ran {
		t.Error("an unconfirmed removal must not have reached the server")
	}
}

// @codex, #638 (P2): `--role=` passes the Changed() no-op guard but parses to
// nil, so the field is omitted, the server PRESERVES the current role, and the
// command prints success for a change it never made. An explicitly empty value
// is a usage error, not an absent flag.
func TestChannelRegisterRefusesAnEmptyRole(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"set", []string{"channel", "register", "set", "reg_1", "--role="}},
		{"add", []string{"channel", "register", "add", "--channel", "c1", "--owner", "o1", "--attendee", "w1", "--role="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"UpdateRegisterEntry": `{"data":{"updateRegisterEntry":` + registerEntryJSON + `}}`,
				"CreateRegisterEntry": `{"data":{"createRegisterEntry":` + registerEntryJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("an explicitly empty --role should fail")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			for _, op := range []string{"UpdateRegisterEntry", "CreateRegisterEntry"} {
				if _, ran := captured[op]; ran {
					t.Errorf("%s must not have been called", op)
				}
			}
		})
	}
}

// @copilot, #638: the pre-delete lookup exists so the prompt can name what is
// going. A TRANSPORT or GraphQL failure there is not "no such entry" — and
// swallowing it let a transient error be followed by a confirmed delete of an
// entry the command could not identify, silently under --yes.
//
// The benign case is a SUCCESSFUL response carrying a null entry; only that one
// falls through to the id.
func TestChannelRegisterRmStopsOnALookupError(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetRegisterEntry":    `{"errors":[{"message":"upstream exploded"}]}`,
		"DeleteRegisterEntry": `{"data":{"deleteRegisterEntry":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "rm", "reg_1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a failed lookup must not be followed by a delete")
	}
	if _, ran := captured["DeleteRegisterEntry"]; ran {
		t.Error("the removal must not have reached the server after a failed lookup")
	}
}

// A readable-but-missing entry IS benign: the server answers successfully with
// a null, and the command falls back to the id so the delete can report the
// not-found itself. Distinguishing this from the error case is the whole point.
func TestChannelRegisterRmProceedsWhenTheEntryIsSimplyAbsent(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetRegisterEntry":    `{"data":{"registerEntry":null}}`,
		"DeleteRegisterEntry": `{"data":{"deleteRegisterEntry":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "rm", "reg_1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a null entry must not block the removal: %v", err)
	}
	if _, ran := captured["DeleteRegisterEntry"]; !ran {
		t.Error("the removal should still have been attempted")
	}
}

// The prompt and help must not promise revocation: a register row is intent,
// never permission (D-2026-09-13-008), so an operator told they revoked access
// would walk away from a door that is still open.
func TestChannelRegisterRmDoesNotPromiseRevocation(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"channel", "register", "rm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "DOES NOT REVOKE ACCESS") {
		t.Errorf("rm help must say it does not revoke access:\n%s", help)
	}
	// And it must point at what DOES revoke access, rather than leaving the
	// reader with a correction and no remedy.
	if !strings.Contains(help, "memory member") && !strings.Contains(help, "memory share") {
		t.Errorf("rm help must name the surface that actually controls access:\n%s", help)
	}
}

// @copilot, #638: the default listing is "the whole slice", so it must DRAIN.
// A single call is capped server-side, and a truncated prefix looks exactly
// like the complete answer — the reason `channel list`, `memory list` and
// `app list` all drain. Two pages here: a first that fills the page and a
// second that completes it.
func TestChannelRegisterListDrainsByDefault(t *testing.T) {
	page := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op != "RegisterEntries" {
			return ""
		}
		page++
		if page == 1 {
			return `{"data":{"registerEntries":{"total":2,"items":[` + registerEntryJSON + `]}}}`
		}
		return `{"data":{"registerEntries":{"total":2,"items":[` + registerEntryWideJSON + `]}}}`
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if page < 2 {
		t.Errorf("the default listing must drain every page, made %d call(s)", page)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(entries) != 2 {
		t.Fatalf("both pages must survive, got %d: %s", len(entries), out.String())
	}
	// Named, not counted: a length of 2 would also pass if page one were kept
	// twice, which is what a broken drain loop actually produces.
	if entries[0].ID != "reg_1" || entries[1].ID != "reg_2" {
		t.Errorf("want reg_1 then reg_2, got %+v", entries)
	}
}

// An EXPLICIT --limit means the caller is paging deliberately: honour it as one
// page rather than overriding it with a drain.
func TestChannelRegisterListExplicitLimitIsOnePage(t *testing.T) {
	calls := 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		if op != "RegisterEntries" {
			return ""
		}
		calls++
		return `{"data":{"registerEntries":{"total":99,"items":[` + registerEntryJSON + `]}}}`
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--limit", "1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 {
		t.Errorf("an explicit --limit must make exactly one call, made %d", calls)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RegisterEntries"], &vars)
	if vars["limit"] != float64(1) {
		t.Errorf("limit = %v, want the caller's 1", vars["limit"])
	}
}

// @codex, #638: --org narrows through its OWN argument rather than through
// filter, so a condition keyed on `filter != nil` let a mistyped organization
// produce a clean empty result — exactly the ambiguity the note exists for.
func TestChannelRegisterListOrgAloneStillWarnsOnEmpty(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":0,"items":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--org", "typo", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "matches nothing") {
		t.Errorf("--org alone must still arm the ambiguity note on an empty page")
	}
	var entries []any
	if err := json.Unmarshal([]byte(out.String()), &entries); err != nil {
		t.Fatalf("stdout must stay valid JSON: %v", err)
	}
}

// An UNNARROWED empty listing is not ambiguous — there is nothing to have
// mistyped — so it must NOT emit the note. Without this, a condition of
// `len(entries) == 0` alone would satisfy the test above while crying wolf on
// every empty register.
func TestChannelRegisterListUnnarrowedEmptyDoesNotWarn(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":0,"items":[]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "register", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "matches nothing") {
		t.Errorf("an unnarrowed empty listing has no ambiguity to explain")
	}
}
