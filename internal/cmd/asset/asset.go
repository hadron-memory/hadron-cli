// Package asset implements `hadron asset …` — the CLI surface for uploaded
// files (spec 006 / cor:dmo:060:10, server #359).
//
// An asset is bytes held against a memory, distinct from a node: nodes carry
// text the graph reasons over, assets carry files the graph points at. See
// docs/plans/asset-command-group.md.
package asset

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdAsset wires the `asset` command group.
func NewCmdAsset(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "asset <command>",
		Aliases: []string{"assets"},
		Short:   "Upload, list, download, link, and delete files",
		Long: `Work with assets — files uploaded against a memory.

An asset is bytes the graph points at, as opposed to a node, which is text the
graph reasons over. Assets are held by a memory and inherit its read access:
anyone who can read the memory can list and download its assets.

Assets are addressed by id or by URN. The URN carries its memory
(hrn:asset:<root>:<memory>:assets:<id>), so a command that needs the holding
memory can take a URN alone; with a bare id, pass -m.

Downloads are gated on virus scanning: an asset is fetchable only once its
scan status is CLEAN. Deletion is soft — "asset restore" brings an asset back
within its retention window.`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdURL(f))
	cmd.AddCommand(newCmdUpload(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdRestore(f))
	cmd.AddCommand(newCmdLink(f))
	return cmd
}

// assetNode aliases genqlient's deeply-nested name for one listed asset.
type assetNode = gen.MemoryAssetsMemoryAssetsAssetListResultAssetsAsset

// assetRef is a parsed <asset-ref>: always an id, plus the holding memory when
// the reference carried one.
type assetRef struct {
	ID string
	// MemoryRef is the memory the URN named, empty for a bare id. Callers that
	// need a memory fall back to -m and error when neither is present.
	MemoryRef string
}

// parseAssetRef accepts an asset id or an asset URN.
//
// The URN is grammar-v2 flat: hrn:asset:<root>:<memory>:assets:<id>. Parsing is
// Postel-liberal in the same way the rest of the CLI is — the `hrn:` prefix is
// optional and the legacy `::` separators are accepted — but the `assets`
// segment is required, because that is the only thing distinguishing an asset
// URN from a node URN whose loc happens to look similar.
func parseAssetRef(ref string) (assetRef, error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return assetRef{}, exitcode.Newf(exitcode.Usage, "an asset id or URN is required")
	}
	// Normalize the legacy double-colon form before splitting so both spellings
	// take the same path (#239 — input stays liberal forever).
	norm := strings.ReplaceAll(s, "::", ":")
	norm = strings.TrimPrefix(strings.TrimPrefix(norm, "hrn:"), "urn:")
	// The server emits the `asset` type word (emitAssetUrnV2 →
	// hrn:asset:<root>:<mem>:assets:<id>). `mem:` is accepted too because the
	// schema documents the shape as "<memory.urn>:assets:<asset.id>", which
	// reads as though the memory's own hrn:mem: prefix is carried through —
	// so somebody will paste that form sooner or later.
	norm = strings.TrimPrefix(strings.TrimPrefix(norm, "asset:"), "mem:")

	parts := strings.Split(norm, ":")
	if len(parts) == 1 {
		return assetRef{ID: parts[0]}, nil // a bare id
	}
	// Expect <root>:<memory>:assets:<id>; find the marker rather than indexing
	// blindly, so a root or memory slug containing a colon can't shift it.
	for i := len(parts) - 2; i >= 1; i-- {
		if parts[i] == "assets" && i+1 < len(parts) {
			id := strings.Join(parts[i+1:], ":")
			mem := strings.Join(parts[:i], ":")
			if id == "" || mem == "" {
				break
			}
			return assetRef{ID: id, MemoryRef: mem}, nil
		}
	}
	return assetRef{}, exitcode.Newf(exitcode.Usage,
		"%q is not an asset id or URN — expected a bare id or hrn:asset:<root>:<memory>:assets:<id>", ref)
}

// memoryScope resolves the memory an asset command should work in: the one the
// URN named, else -m. Returns a usage error naming both routes when neither is
// available, since "which memory?" is the single most common way these
// commands are mis-invoked.
func memoryScope(ref assetRef, memFlag string) (string, error) {
	switch {
	case memFlag != "":
		return memFlag, nil
	case ref.MemoryRef != "":
		return ref.MemoryRef, nil
	default:
		return "", exitcode.Newf(exitcode.Usage,
			"cannot tell which memory holds asset %q — pass -m <memory>, or give the asset's URN (which carries it)", ref.ID)
	}
}

// resolveMemoryID turns a memory ref into an id. A colon-free value is already
// an id and needs no round-trip; anything else is a URN or short form the
// server resolves.
func resolveMemoryID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	canon := cmdutil.CanonicalMemoryRef(ref)
	if !strings.Contains(canon, ":") {
		return canon, nil
	}
	resp, err := gen.GetMemory(cmd.Context(), client, canon)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.Memory == nil {
		return "", exitcode.Newf(exitcode.NotFound,
			"no memory found for %q — expected a memory id or a URN: hrn:mem:<root>:<slug> (canonical), the <root>::<slug> / <root>:<slug> short forms, or the legacy hrn:memory: prefix", ref)
	}
	return resp.Memory.Id, nil
}
