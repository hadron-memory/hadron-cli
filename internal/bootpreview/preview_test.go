package bootpreview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestProjectionGolden(t *testing.T) {
	for _, tc := range []struct{ name, wire, own, want, label string }{
		{"empty-outcome", `{"served":null,"refusal":null}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":null}`, "unknown (next bind)"},
		{"unknown-refusal", `{"refusal":"FUTURE_REFUSAL"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":null}`, "unknown (next bind)"},
		{"unsigned", `{"refusal":"BOOT_PROMPT_NOT_SIGNED"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":"BOOT_PROMPT_NOT_SIGNED"}`, "BOOT_PROMPT_NOT_SIGNED (next bind)"},
		{"agent", `{"served":{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":9}}`, "WORKER", `{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":9,"bootPromptRefusal":null}`, "AGENT n1 revision 9 (next bind)"},
		{"own", `{"served":{"pointerLevel":"WORKER","compiledNodeId":"n1","servedRevSeq":9}}`, "WORKER", `{"pointerLevel":"WORKER","compiledNodeId":"n1","servedRevSeq":9,"bootPromptRefusal":null,"bootPromptUrn":"hrn:node:example.com:kb:boot"}`, "WORKER n1 revision 9 (next bind); hrn:node:example.com:kb:boot"},
		{"none", `{"refusal":"BOOT_PROMPT_NOT_SET"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":"BOOT_PROMPT_NOT_SET"}`, "none (next bind)"},
		{"refused", `{"refusal":"BOOT_PROMPT_TARGET_DELETED"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":"BOOT_PROMPT_TARGET_DELETED"}`, "BOOT_PROMPT_TARGET_DELETED (next bind)"},
		{"license-inactive", `{"refusal":"BOOT_PROMPT_INSTALL_LICENSE_INACTIVE"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":"BOOT_PROMPT_INSTALL_LICENSE_INACTIVE"}`, "BOOT_PROMPT_INSTALL_LICENSE_INACTIVE (next bind)"},
		{"malformed", `{"served":{"pointerLevel":"PRIVATE","compiledNodeId":"secret","servedRevSeq":9}}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":null}`, "unknown (next bind)"},
		{"unapproved-pointer", `{"refusal":"BOOT_PROMPT_NOT_APPROVED"}`, "WORKER", `{"pointerLevel":null,"compiledNodeId":null,"servedRevSeq":null,"bootPromptRefusal":"BOOT_PROMPT_NOT_APPROVED"}`, "BOOT_PROMPT_NOT_APPROVED (next bind)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var o gen.BootPromptPreviewFields
			if err := json.Unmarshal([]byte(tc.wire), &o); err != nil {
				t.Fatal(err)
			}
			ref := "hrn:node:example.com:kb:boot"
			p := Project(&o, tc.own, &ref)
			wire, err := json.Marshal(p.Fields)
			if err != nil {
				t.Fatal(err)
			}
			if string(wire) != tc.want || p.Label() != tc.label {
				t.Fatalf("JSON=%s label=%q", wire, p.Label())
			}
		})
	}
	p := Project(nil, "WORKER", nil)
	if p.Label() != "not visible to you (next bind)" {
		t.Fatal(p.Label())
	}
}

func TestProjectionDegenerate1655(t *testing.T) {
	ref := "hrn:node:example.com:private:boot"
	p := Project(nil, "WORKER", &ref)
	if p.CompiledNodeID != nil || p.PointerLevel != nil || p.BootPromptURN != nil || p.BootPromptRefusal != nil {
		t.Fatal("pointer promoted to served evidence")
	}
	for _, wire := range []string{
		`{"served":{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":0}}`,
		`{"served":{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":2147483648}}`,
		`{"served":{"pointerLevel":"AGENT","compiledNodeId":" ","servedRevSeq":1}}`,
		`{"served":{"pointerLevel":"AGENT","compiledNodeId":"n1","servedRevSeq":1},"refusal":"BOOT_PROMPT_NOT_SET"}`,
	} {
		var o gen.BootPromptPreviewFields
		if err := json.Unmarshal([]byte(wire), &o); err != nil {
			t.Fatal(err)
		}
		p = Project(&o, "WORKER", &ref)
		if p.PointerLevel != nil || p.CompiledNodeID != nil || p.ServedRevSeq != nil || p.BootPromptURN != nil || p.BootPromptRefusal != nil || p.Label() != "unknown (next bind)" {
			t.Fatalf("malformed decided: %#v %s", p, p.Label())
		}
	}
}

func TestProjectionTerminalSafety1655(t *testing.T) {
	var o gen.BootPromptPreviewFields
	if err := json.Unmarshal([]byte(`{"served":{"pointerLevel":"WORKER","compiledNodeId":"n\u001b[31m\nrow","servedRevSeq":3}}`), &o); err != nil {
		t.Fatal(err)
	}
	ref := "hrn:node:example.com:kb:x\n\x1b[31m"
	p := Project(&o, "WORKER", &ref)
	if strings.ContainsAny(p.Label(), "\n\r\x1b") {
		t.Fatalf("unsafe text: %q", p.Label())
	}
	if p.CompiledNodeID == nil || *p.CompiledNodeID != "n\x1b[31m\nrow" || p.BootPromptURN == nil || *p.BootPromptURN != ref {
		t.Fatal("JSON raw identity changed")
	}
}

func TestProjectionInstallCannotServeWorker1655(t *testing.T) {
	for _, tc := range []struct {
		level    string
		accepted bool
	}{{"WORKER", false}, {"APP_AGENT", true}} {
		t.Run(tc.level, func(t *testing.T) {
			var o gen.BootPromptPreviewFields
			wire := `{"served":{"pointerLevel":"` + tc.level + `","compiledNodeId":"fixture-node","servedRevSeq":3}}`
			if err := json.Unmarshal([]byte(wire), &o); err != nil {
				t.Fatal(err)
			}
			if o.Served == nil || string(o.Served.PointerLevel) != tc.level {
				t.Fatal("fixture lost discriminator")
			}
			p := Project(&o, "APP_AGENT", nil)
			if !tc.accepted {
				if p.PointerLevel != nil || p.CompiledNodeID != nil || p.Label() != "unknown (next bind)" {
					t.Fatalf("inconsistent surface accepted: %#v %s", p, p.Label())
				}
			} else if p.PointerLevel == nil || *p.PointerLevel != "APP_AGENT" || p.CompiledNodeID == nil || *p.CompiledNodeID != "fixture-node" || p.ServedRevSeq == nil || *p.ServedRevSeq != 3 || p.Label() != "APP_AGENT fixture-node revision 3 (next bind)" {
				t.Fatalf("valid install rejected: %#v %s", p, p.Label())
			}
		})
	}
}
