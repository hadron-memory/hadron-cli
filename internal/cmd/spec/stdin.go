package spec

import "github.com/hadron-memory/hadron-cli/internal/cmdutil"

// refuseDocumentStdin refuses an interactive terminal for the spec group's two
// stdin readers, a spec body (--content -) and its abstract (--abstract -).
// Both are DOCUMENTS, not secrets (#648): a terminal's line discipline can
// truncate them before the CLI reads a byte, and a spec stored truncated is
// law with clauses missing. Called with the other argument checks, before the
// citation is resolved over the network, so the refusal costs no round trip.
func refuseDocumentStdin(stdinIsTerminal bool, content, abstract string) error {
	if content == "-" {
		if err := cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--content -", "--content-file"); err != nil {
			return err
		}
	}
	if abstract == "-" {
		return cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--abstract -", "--abstract-file")
	}
	return nil
}
