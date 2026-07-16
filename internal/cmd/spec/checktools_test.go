package spec

import (
	"strings"
	"testing"
)

func TestParseToolList(t *testing.T) {
	raw := "# a header comment\n\nhadron_get_node\nhadron_token   # trailing comment\n  hadron_run_task  \n# whole-line\n"
	got := parseToolList(raw)
	for _, want := range []string{"hadron_get_node", "hadron_token", "hadron_run_task"} {
		if !got[want] {
			t.Errorf("parseToolList missing %q", want)
		}
	}
	if got["# a header comment"] || got[""] || len(got) != 3 {
		t.Errorf("parseToolList picked up junk: %v", got)
	}
}

func TestCheckToolField(t *testing.T) {
	registered := map[string]bool{"hadron_get_node": true, "hadron_chatbot_send": true}
	ignored := map[string]bool{"hadron_token": true, "hadron_chatbot": true}

	body := strings.Join([]string{
		"Use hadron_get_node to read.",                           // 1: registered — ok
		"Call hadron_bogus_tool then hadron_gone.",               // 2: two unknowns
		"The hadron_token cookie is not a tool.",                 // 3: ignored
		"The hadron_chatbot_* family, e.g. hadron_chatbot_send.", // 4: prefix ignored + registered
		"hadron_bogus_tool again and hadron_bogus_tool twice.",   // 5: dedup per line
	}, "\n")

	got := checkToolField("cor:api:010:01", "content", body, registered, ignored)
	// Expected: line2 → hadron_bogus_tool, hadron_gone; line5 → hadron_bogus_tool (once).
	if len(got) != 3 {
		t.Fatalf("want 3 findings, got %d: %+v", len(got), got)
	}
	if got[0].Line != 2 || got[0].Token != "hadron_bogus_tool" {
		t.Errorf("finding 0 = %+v", got[0])
	}
	if got[1].Line != 2 || got[1].Token != "hadron_gone" {
		t.Errorf("finding 1 = %+v", got[1])
	}
	if got[2].Line != 5 || got[2].Token != "hadron_bogus_tool" {
		t.Errorf("finding 2 (dedup) = %+v", got[2])
	}
	if got[0].Citation != "cor:api:010:01" || got[0].Field != "content" {
		t.Errorf("finding meta = %+v", got[0])
	}
}

// The token regex must not capture a trailing underscore from a glob like
// "hadron_chatbot_*", so the token is comparable to a registered/ignored name.
func TestToolTokenRegexNoTrailingUnderscore(t *testing.T) {
	got := reToolToken.FindAllString("see hadron_chatbot_* and hadron_get_node.", -1)
	want := []string{"hadron_chatbot", "hadron_get_node"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("token regex = %v, want %v", got, want)
	}
}

// The embedded manifest and ignore-list must be present and sane — a broken
// embed directive or an emptied file would silently disable the check.
func TestEmbeddedToolManifest(t *testing.T) {
	reg := parseToolList(toolManifestRaw)
	if len(reg) < 30 {
		t.Fatalf("embedded tool manifest looks empty/broken (%d entries)", len(reg))
	}
	for _, want := range []string{"hadron_get_node", "hadron_run_task", "hadron_read_secret"} {
		if !reg[want] {
			t.Errorf("manifest missing expected tool %q", want)
		}
	}
	ign := parseToolList(toolIgnoreRaw)
	for _, want := range []string{"hadron_token", "hadron_chatbot"} {
		if !ign[want] {
			t.Errorf("ignore-list missing expected non-tool %q", want)
		}
	}
}
