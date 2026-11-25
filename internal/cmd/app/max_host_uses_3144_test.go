package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// cli#3144: maxHostUses repeats the server's cap, which the schema states only
// in AppKey.hostUsesPage's description. A refresh that changes the cap there
// must fail here, or the CLI's stderr note would give the wrong number.
func TestMaxHostUsesMatchesTheSchema(t *testing.T) {
	raw, err := os.ReadFile("../../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	sdl := string(raw)
	end := strings.Index(sdl, "hostUsesPage(limit: Int, offset: Int): HostKeyUsePage!")
	if end < 0 {
		t.Fatal("schema has no AppKey.hostUsesPage field")
	}
	// The description is the block string that closes just above the field.
	parts := strings.Split(sdl[:end], `"""`)
	if len(parts) < 3 {
		t.Fatal("AppKey.hostUsesPage has no description")
	}
	doc := parts[len(parts)-2]
	if want := fmt.Sprintf("a limit over %d is served as %d (never a refusal)", maxHostUses, maxHostUses); !strings.Contains(doc, want) {
		t.Errorf("AppKey.hostUsesPage's description lacks %q: update maxHostUses (and the --host-uses help) to the server's cap\n%s", want, doc)
	}
}
