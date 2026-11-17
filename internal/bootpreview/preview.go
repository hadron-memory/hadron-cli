// Package bootpreview projects the server's next-bind decision, never content.
package bootpreview

import (
	"fmt"
	"math"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Fields are additive public JSON keys. Null means unknown/withheld/refused,
// never no pointer. Only BOOT_PROMPT_NOT_SET establishes absence.
type Fields struct {
	PointerLevel      *string `json:"pointerLevel"`
	CompiledNodeID    *string `json:"compiledNodeId"`
	ServedRevSeq      *int    `json:"servedRevSeq"`
	BootPromptRefusal *string `json:"bootPromptRefusal"`
	BootPromptURN     *string `json:"bootPromptUrn,omitempty"`
}

type Preview struct {
	Fields
	label string
}

// Project uses no pointer as evidence of approval. ownLevel/ref are already
// selected under the target's own gate; they can decorate only a matching level.
func Project(o *gen.BootPromptPreviewFields, ownLevel string, ref *string) Preview {
	p := Preview{label: "not visible to you (next bind)"}
	if o == nil {
		return p
	}
	p.label = "unknown (next bind)"
	if o.Served != nil && o.Refusal == nil {
		s := o.Served
		level := string(s.PointerLevel)
		if (ownLevel == "APP_AGENT" && level == "WORKER") || (level != "WORKER" && level != "APP_AGENT" && level != "AGENT") || strings.TrimSpace(s.CompiledNodeId) == "" || s.ServedRevSeq < 1 || int64(s.ServedRevSeq) > math.MaxInt32 {
			return p
		}
		p.PointerLevel = &level
		p.CompiledNodeID = &s.CompiledNodeId
		p.ServedRevSeq = &s.ServedRevSeq
		p.label = fmt.Sprintf("%s %s revision %d (next bind)", level, output.SafeCell(s.CompiledNodeId), s.ServedRevSeq)
		if level == ownLevel && ref != nil && *ref != "" {
			p.BootPromptURN = ref
			p.label += "; " + output.SafeCell(*ref)
		}
	} else if o.Refusal != nil && o.Served == nil {
		refusal := string(*o.Refusal)
		switch refusal {
		case "BOOT_PROMPT_NOT_SET":
			p.label = "none (next bind)"
		case "BOOT_PROMPT_NOT_APPROVED", "BOOT_PROMPT_NOT_SIGNED", "BOOT_PROMPT_TARGET_DELETED",
			"BOOT_PROMPT_INSTALL_LICENSE_INACTIVE":
			p.label = refusal + " (next bind)"
		default:
			return p
		}
		p.BootPromptRefusal = &refusal
	}
	return p
}

func (p Preview) Label() string { return p.label }
