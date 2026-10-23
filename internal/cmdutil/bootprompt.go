package cmdutil

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// BootPromptNodeRef turns the `set-boot-prompt` arguments (cli#916 C2, #2045
// S8) into the nodeRef to send: the normalized node ref to set, or nil for
// --clear, which the caller sends as an explicit null. Exactly one of a node
// ref and --clear is required; anything else is a usage error before any
// request. The ref is normalized locally (BatchNodeRef: an id, a node URN, or
// -m plus a bare loc) and never looked up, since whether it names a readable,
// compiled prompt in the right memory is the server's answer to give.
func BootPromptNodeRef(memory string, args []string, clear bool) (*string, error) {
	switch {
	case clear && len(args) == 1:
		return nil, exitcode.Newf(exitcode.Usage, "pass either a node ref or --clear, not both")
	case clear:
		if strings.TrimSpace(memory) != "" {
			return nil, exitcode.Newf(exitcode.Usage, "-m/--memory names a bare loc's memory; it has no meaning with --clear")
		}
		return nil, nil
	case len(args) == 1:
		if strings.TrimSpace(args[0]) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "the node ref is empty")
		}
		ref, err := BatchNodeRef(memory, args[0])
		if err != nil {
			return nil, err
		}
		return &ref, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "pass the compiled boot prompt's node ref, or --clear to remove the pointer")
	}
}

// BootPromptLabel renders a boot-prompt pointer for human output. Both fields
// null means unset OR withheld, and the label says so rather than "none": the
// CLI cannot tell the two apart and does not try. A deleted target keeps its
// URN with the remedy, since nothing is served from it (R27).
func BootPromptLabel(ref *string, deleted *bool) string {
	if ref == nil || *ref == "" {
		return "— (unset or not visible to you)"
	}
	if deleted != nil && *deleted {
		return *ref + " (deleted: clear or repoint)"
	}
	return *ref
}
