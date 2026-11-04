package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#1102: the memory ls / member / share / subscription tables and the
// attach / clone / extract / set confirmation lines render server-supplied
// values through output.SafeCell. Each test puts the evil label in one record
// and the 1000-rune value in another, then checks the text table (one logical
// row per record, no control byte, sanitized label, cut long value) and that
// --json still carries both originals.

const memRaw1102 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// memoryItem1102 is one memory as the server projects MemoryFields.
func memoryItem1102(id, urn, name, extra string) string {
	return `{"id":"` + id + `","urn":"` + urn + `","name":"` + name + `","shortDescription":null,"class":"personal",
		"visibility":null,"organizationId":null,"isEncrypted":false,"maxRevCount":10,
		"updatedAt":"2026-09-20T00:00:00Z"` + extra + `}`
}

func TestSafeCell1102MemoryLsOwned(t *testing.T) {
	page := `{"data":{"memories":{"total":2,"items":[` +
		memoryItem1102("m1", "hrn:mem:acme.com:one", evilJSON1102, "") + `,` +
		memoryItem1102("m2", "hrn:mem:acme.com:two", longJSON1102, "") + `]}}}`
	text, js := runBoth1102(t, map[string]string{"Memories": page}, "memory", "ls", "--owned-by-me")
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemoryLsShared(t *testing.T) {
	grantor := func(name string) string {
		return `,"myShare":{"role":"reader","grantor":{"id":"usr_9","name":"` + name + `","email":null,"handle":"g"}}`
	}
	page := `{"data":{"memories":{"total":2,"items":[` +
		memoryItem1102("m1", "hrn:mem:acme.com:one", evilJSON1102, grantor(evilJSON1102)) + `,` +
		memoryItem1102("m2", "hrn:mem:acme.com:two", longJSON1102, grantor(longJSON1102)) + `]}}}`
	text, js := runBoth1102(t, map[string]string{"MemoriesSharedWithMe": page}, "memory", "ls", "--shared-with-me")
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemoryMemberLs(t *testing.T) {
	user := func(id, name, email string) string {
		return `{"id":"` + id + `","name":"` + name + `","email":"` + email + `","handle":"h"}`
	}
	resp := `{"data":{"memory":{"id":"` + memRaw1102 + `","members":[
		{"role":"owner","createdAt":"2026-06-19T00:00:00Z","user":` + user("usr1", evilJSON1102, "e@acme.com") + `},
		{"role":"writer","createdAt":"2026-06-19T00:00:00Z","user":` + user("usr2", "plain", longJSON1102) + `}]}}}`
	text, js := runBoth1102(t, map[string]string{"MemoryMembers": resp}, "memory", "member", "ls", memRaw1102)
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemoryShareLs(t *testing.T) {
	user := func(id, name, email string) string {
		return `{"id":"` + id + `","name":"` + name + `","email":"` + email + `","handle":"h"}`
	}
	resp := `{"data":{"memory":{"id":"` + memRaw1102 + `","shares":[
		{"role":"reader","createdAt":"2026-06-19T00:00:00Z","grantee":` + user("usr1", evilJSON1102, "e@acme.com") + `},
		{"role":"writer","createdAt":"2026-06-19T00:00:00Z","grantee":` + user("usr2", "plain", longJSON1102) + `}]}}}`
	text, js := runBoth1102(t, map[string]string{"MemoryShares": resp}, "memory", "share", "ls", memRaw1102)
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemorySubscriptionLs(t *testing.T) {
	org := func(id, name, urn string) string {
		return `{"id":"` + id + `","name":"` + name + `","urn":"` + urn + `"}`
	}
	resp := `{"data":{"memory":{"id":"` + memRaw1102 + `","subscriptions":[
		{"role":"CONTRIBUTOR","activated":true,"organization":` + org("org1", evilJSON1102, "partnerco.com") + `},
		{"role":"CONTRIBUTOR","activated":false,"organization":` + org("org2", "plain", longJSON1102) + `}]}}}`
	text, js := runBoth1102(t, map[string]string{"MemorySubscriptions": resp}, "memory", "subscription", "ls", memRaw1102)
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

// oneLine1102 runs a header-less confirmation command twice, once with the
// evil URN and once with the long one, and checks the single "✓ ..." line.
func oneLine1102(t *testing.T, op, field string, args ...string) {
	t.Helper()
	for _, urnTail := range []string{evilJSON1102, longJSON1102} {
		isEvil := urnTail == evilJSON1102
		// The long URN carries no prefix: SafeCell cuts the whole cell at 256
		// runes, so a prefix would shorten the run of x's the helper counts.
		urn := urnTail
		if isEvil {
			urn = "hrn:mem:acme.com:" + urnTail
		}
		resp := `{"data":{"` + field + `":` + memoryItem1102("m1", urn, "Name", "") + `}}`
		text, js := runBoth1102(t, map[string]string{op: resp}, args...)
		assertSafeText1102(t, text, 1, isEvil, !isEvil)
		if !strings.Contains(text, "✓") {
			t.Fatalf("no confirmation mark:\n%s", text)
		}
		assertJSONUnchanged1102(t, js, isEvil, !isEvil)
	}
}

func TestSafeCell1102MemoryAttach(t *testing.T) {
	oneLine1102(t, "AttachMemoryToApp", "attachMemoryToApp",
		"memory", "attach", "hrn:mem:acme.com:kb", "--app", "hrn:app:acme.com:support", "--agent", "hrn:agent:acme.com:x")
}

func TestSafeCell1102MemoryClone(t *testing.T) {
	oneLine1102(t, "CloneMemory", "cloneMemory",
		"memory", "clone", "hrn:mem:acme.com:kb", "--target-urn", "hrn:mem:acme.com:copy")
}

func TestSafeCell1102MemoryExtract(t *testing.T) {
	oneLine1102(t, "ExtractParentNodeToMemory", "extractParentNodeToMemory",
		"memory", "extract", "node-id-1", "hrn:mem:acme.com:out")
}

func TestSafeCell1102MemorySet(t *testing.T) {
	// set prints URN and name: evil in the URN, long in the name, one line.
	resp := `{"data":{"updateMemory":` + strings.Replace(
		memoryItem1102("m1", "hrn:mem:acme.com:"+evilJSON1102, longJSON1102, ""),
		`"isEncrypted":false`, `"listedOnMarketplace":false,"isEncrypted":false`, 1) + `}}`
	text, js := runBoth1102(t, map[string]string{"UpdateMemory": resp},
		"memory", "set", memRaw1102, "--name", "x")
	assertSafeText1102(t, text, 1, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}
