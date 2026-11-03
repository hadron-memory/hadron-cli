package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// #1075: writeUpdatePreview appends the advisory to the warnings slice it is
// handed. With spare capacity that append writes into the caller's backing
// array; the helper must leave caller-owned storage alone, and the advisory
// text and JSON stay exactly as they were.
func TestUpdatePreviewWarningsOwnership1075(t *testing.T) {
	const advisory = "expected revision 7 is advisory and not validated by the server in a dry run"
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
			backing := make([]string, 2, 4)
			backing[0] = "template dropped"
			backing[1] = "sentinel"
			caller := backing[:1]
			streams, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, JSON: asJSON}
			rev := 7
			stored := &gen.GetNodeNode{Id: "n1", Urn: "hrn:node:acme.com:kb:a", Loc: "a"}
			changed := func(fl string) bool { return fl == "name" }
			if err := writeUpdatePreview(f, stored, changed, "", false, caller, &rev); err != nil {
				t.Fatal(err)
			}
			if len(caller) != 1 || caller[0] != "template dropped" {
				t.Fatalf("caller slice changed: %q", caller)
			}
			if backing[1] != "sentinel" {
				t.Fatalf("helper wrote into the caller's spare capacity: %q", backing[1])
			}
			if !asJSON {
				if !strings.Contains(out.String(), "\nwarning: template dropped\n") || !strings.Contains(out.String(), "\nwarning: "+advisory+"\n") {
					t.Fatalf("text warnings: %s", out.String())
				}
				return
			}
			var d struct {
				Warnings         []string `json:"warnings"`
				ExpectedRevision *int     `json:"expectedRevision"`
			}
			if err := json.Unmarshal(out.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			if len(d.Warnings) != 2 || d.Warnings[0] != "template dropped" || d.Warnings[1] != advisory {
				t.Fatalf("json warnings: %q", d.Warnings)
			}
			if d.ExpectedRevision == nil || *d.ExpectedRevision != 7 {
				t.Fatalf("expectedRevision: %v", d.ExpectedRevision)
			}
		})
	}
}
