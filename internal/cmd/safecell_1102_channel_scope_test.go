package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#1102: the channel and scope text tables. Each fixture puts an
// evil label (newline + ESC) in one server-supplied cell and a 1000-rune value
// in another; the assertions are in safecell_1102_helpers_test.go.

func channelWith1102(name, description, memoryURN string) string {
	c := strings.Replace(channelJSON, `"name":"team"`, `"name":"`+name+`"`, 1)
	c = strings.Replace(c, `"description":null`, `"description":"`+description+`"`, 1)
	return strings.Replace(c, `"urn":"hrn:mem:acme.com:team-shared"`, `"urn":"`+memoryURN+`"`, 1)
}

func TestSafeCell1102ChannelGet(t *testing.T) {
	text, js := runBoth1102(t, map[string]string{
		"NodePortal": `{"data":{"node":null}}`,
		"GetChannel": `{"data":{"channel":` + channelWith1102(evilJSON1102, longJSON1102, "hrn:mem:acme.com:team-shared") + `}}`,
	}, "channel", "get", "hrn:node:acme.com:team-shared:chats:team")
	assertSafeText1102(t, text, 8, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102ChannelList(t *testing.T) {
	text, js := runBoth1102(t, map[string]string{
		"Channels": `{"data":{"channels":{"total":1,"items":[` + channelWith1102(evilJSON1102, "d", longJSON1102) + `]}}}`,
	}, "channel", "list")
	assertSafeText1102(t, text, 2, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102ChannelRegisterList(t *testing.T) {
	entry := strings.Replace(registerEntryJSON, `"attendeeUrn":"hrn:worker:acme.com:eng:iris"`, `"attendeeUrn":"`+evilJSON1102+`"`, 1)
	entry = strings.Replace(entry, `"channelId":"chn_1"`, `"channelId":"`+longJSON1102+`"`, 1)
	text, js := runBoth1102(t, map[string]string{
		"RegisterEntries": `{"data":{"registerEntries":{"total":1,"items":[` + entry + `]}}}`,
	}, "channel", "register", "list")
	assertSafeText1102(t, text, 2, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func scopeWith1102(name, description string) string {
	s := strings.Replace(scopeJSON, `"name":"research"`, `"name":"`+name+`"`, 1)
	s = strings.Replace(s, `"description":"papers"`, `"description":"`+description+`"`, 1)
	return strings.Replace(s, `"name":"Papers"`, `"name":"`+name+`"`, 1)
}

func TestSafeCell1102ScopeGet(t *testing.T) {
	sc := scopeWith1102(evilJSON1102, longJSON1102)
	text, js := runBoth1102(t, map[string]string{
		"GetScope": `{"data":{"scope":` + sc + `}}`,
	}, "scope", "get", "0123456789abcdef0123456789abcdef")
	assertSafeText1102(t, text, 10, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102ScopeList(t *testing.T) {
	text, js := runBoth1102(t, map[string]string{
		"Scopes": `{"data":{"scopes":{"total":1,"items":[` + scopeWith1102(evilJSON1102, "d") + `]}}}`,
	}, "scope", "list")
	// header + the record + the static "not readable" note line.
	assertSafeText1102(t, text, 3, true, false)
	assertJSONUnchanged1102(t, js, true, false)
}

func TestSafeCell1102ScopeExplain(t *testing.T) {
	sc := scopeWith1102(evilJSON1102, "d")
	mem := `{"id":"m1","urn":"hrn:mem:acme.com:papers","name":"` + longJSON1102 + `"}`
	text, js := runBoth1102(t, map[string]string{
		"ScopeExplain": `{"data":{"scopeExplain":{"resolvedVia":"APP","droppedCount":0,
			"scope":` + sc + `,"memories":[` + mem + `],"winner":` + mem + `,"shadowed":[` + sc + `]}}}`,
	}, "scope", "explain", "0123456789abcdef0123456789abcdef", "--loc", "findings:x")
	// summary table, memories table, blank, note, shadowed table.
	assertSafeText1102(t, text, 11, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}
