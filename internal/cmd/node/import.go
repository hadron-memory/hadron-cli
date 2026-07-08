package node

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/nodedoc"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// importNodeSummaryDTO is the stable --json shape for a restore-mode import (a
// node-export file reconstituted via createNode/updateNode). `mode` is the
// discriminator against the content-mode shape below.
type importNodeSummaryDTO struct {
	Mode         string           `json:"mode"` // always "restore"
	Memory       string           `json:"memory"`
	Loc          string           `json:"loc"`
	Action       string           `json:"action"`
	NodeID       string           `json:"nodeId"`
	EdgesWired   int              `json:"edgesWired"`
	UnwiredEdges []unwiredEdgeDTO `json:"unwiredEdges"`
}

// importContentSummaryDTO is the stable --json shape for a content-mode import
// (raw source — URL/HTML/Markdown/PDF — converted to a node body by the server's
// importNode). `mode` discriminates it from the restore shape above.
type importContentSummaryDTO struct {
	Mode     string `json:"mode"` // always "content"
	Status   string `json:"status"`
	Memory   string `json:"memory"`
	Loc      string `json:"loc"`
	NodeID   string `json:"nodeId"`
	Name     string `json:"name"`
	NodeType string `json:"nodeType"`
	// JobID is the run id when --task minted a post-import task run (status
	// FETCH_PENDING); empty otherwise (#528).
	JobID string `json:"jobId,omitempty"`
}

// unwiredEdgeDTO is one outgoing edge --with-edges could not wire, with why —
// so a caller can tell a not-yet-resolvable target (transient; retry) from an
// invalid condition or a server rejection (fix the file) without guessing.
type unwiredEdgeDTO struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// contentExtensions are source extensions that can only ever be raw content (a
// node-export file is frontmatter-markdown or canonical JSON, never these), so
// they route to content mode without an explicit --as-content.
var contentExtensions = map[string]bool{".pdf": true, ".html": true, ".htm": true}

func newCmdImport(f *cmdutil.Factory) *cobra.Command {
	var (
		memory string
		loc    string
		// Restore-mode (node-export file) flags.
		format     string
		withEdges  bool
		createOnly bool
		dryRun     bool
		// Content-mode (importNode) flags.
		asContent      bool
		url            string
		nodeURN        string
		contentType    string
		name           string
		nodeType       string
		properties     string
		propertiesFile string
		// Content-mode post-import task run (#528).
		taskRef  string
		taskArgs string
		app      string
		// Shared.
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "import [<file>|-]",
		Short: "Import a node — reconstitute an export file, or ingest external content (URL/HTML/Markdown/PDF)",
		Long: `Import into a node. Two modes:

RESTORE (default) — reconstitute a node-export file produced by ` + "`hadron node export`" + `
(frontmatter-markdown, or ` + "`--format json`" + `). A node already at the target loc is
updated, else created. Read "-" to import from stdin, so an export pipes straight
into an import. The target memory/loc come from the file's own keys; -m/--memory
and --loc override them (re-homing a node into another memory). Outgoing edges
are imported only with --with-edges (off by default).

CONTENT — ingest RAW external source (a web page, a captured HTML DOM, a Markdown
file, or a PDF) and let the server convert it to the node's Markdown body. This
mode is selected by --url, by --as-content (force it for an otherwise-ambiguous
.md/.json / stdin source), or automatically for a .pdf/.html file. A PDF's text
layer is extracted to Markdown — the CLI base64-encodes the file for you
(scanned/image-only PDFs error server-side). The content type is inferred from
the file extension unless --content-type overrides it (required for a PDF over
stdin). Target the node with -m/--memory + --loc or --node <urn>; a node already
there is updated in place, else created (nodeType defaults to webpage, or info
for a PDF).

An import that lands on an EXISTING node overwrites it (a prior version is
kept). Like the destructive commands, that prompts on a terminal and requires
--yes non-interactively; a create is never gated.

--task <ref> (content mode) runs a task node against the freshly-stored node:
the server mints a MANUAL run (the imported node's URN is passed to the task)
and this prints the run id — follow it with 'hadron run get <id>'. --task-args
adds template args and --app names the App to run under (default: your active
App).`,
		Example: `  hadron node import flaky.md                        # restore a node-export file
  hadron node export acme.com::kb::x | hadron node import -m acme.com::kb2 -
  hadron node import paper.pdf -m acme.com::kb --loc papers:attention
  hadron node import --url https://example.com/post -m acme.com::kb --loc clips:post
  hadron node import notes.md --as-content -m acme.com::kb --loc notes:today
  hadron node import --url https://ex.com/p -m acme.com::kb --loc clips:p --task acme.com::kb::tasks:distill --app acme.com:ops`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var srcPath string
			if len(args) == 1 {
				srcPath = args[0]
			}

			// Mode dispatch. Content mode is selected explicitly (--url,
			// --as-content, --content-type) or by an unambiguously-content file
			// extension; everything else (a .md/.json export doc, or a stdin
			// pipe) defaults to restore so `export | import` still round-trips.
			contentMode := url != "" ||
				asContent ||
				cmd.Flags().Changed("content-type") ||
				(srcPath != "" && srcPath != "-" && contentExtensions[strings.ToLower(filepath.Ext(srcPath))])

			// Reject flags belonging to the other mode so a wrong combination
			// fails loudly instead of being silently ignored.
			restoreOnly := []string{"format", "with-edges", "create-only", "dry-run"}
			contentOnly := []string{"url", "as-content", "content-type", "name", "type", "properties", "properties-file", "node", "task", "task-args", "app"}
			if contentMode {
				if err := rejectFlags(cmd, restoreOnly, "does not apply to content import (--url/--as-content/PDF)"); err != nil {
					return err
				}
				return runImportContent(cmd, f, contentImportOpts{
					srcPath: srcPath, url: url, memory: memory, loc: loc, nodeURN: nodeURN,
					contentType: contentType, name: name, nodeType: nodeType,
					properties: properties, propertiesFile: propertiesFile,
					taskRef: taskRef, taskArgs: taskArgs, app: app, yes: yes,
				})
			}
			if err := rejectFlags(cmd, contentOnly, "applies only to content import — pass --as-content to ingest this file as raw content"); err != nil {
				return err
			}
			return runImportRestore(cmd, f, srcPath, memory, loc, format, cmd.Flags().Changed("format"), withEdges, createOnly, dryRun, yes)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "target memory ID or URN (overrides an export file's memory key)")
	cmd.Flags().StringVar(&loc, "loc", "", "target loc (overrides an export file's loc key)")
	cmd.Flags().StringVar(&format, "format", "md", "restore: input format, md or json (inferred from the file extension when unset)")
	cmd.Flags().BoolVar(&withEdges, "with-edges", false, "restore: also wire the file's outgoing edges (best-effort)")
	cmd.Flags().BoolVar(&createOnly, "create-only", false, "restore: fail if the loc already exists (no update)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "restore: parse and classify without mutating")
	cmd.Flags().BoolVar(&asContent, "as-content", false, "content: ingest the file/stdin as raw content (convert server-side) instead of restoring an export file")
	cmd.Flags().StringVar(&url, "url", "", "content: fetch this URL server-side as the source (instead of a file/stdin)")
	cmd.Flags().StringVar(&nodeURN, "node", "", "content: target node URN (instead of -m/--memory + --loc)")
	cmd.Flags().StringVar(&contentType, "content-type", "", "content: MIME type — text/html, text/markdown, or application/pdf (inferred from the file extension when unset)")
	cmd.Flags().StringVar(&name, "name", "", "content: node display name (defaults to the extracted title, or preserves an existing node's name)")
	cmd.Flags().StringVar(&nodeType, "type", "", "content: node type (defaults to webpage, or info for a PDF)")
	cmd.Flags().StringVar(&properties, "properties", "", "content: provenance JSON object merged into the node's properties")
	cmd.Flags().StringVar(&propertiesFile, "properties-file", "", "content: read the properties JSON object from a file")
	cmd.Flags().StringVar(&taskRef, "task", "", "content: after storing, run this task node (ID or URN) against the imported node; prints the run id")
	cmd.Flags().StringVar(&taskArgs, "task-args", "", "content: JSON object of extra template args merged into the task run (requires --task)")
	cmd.Flags().StringVar(&app, "app", "", "content: App (ID or URN) to run --task under (defaults to your active App)")
	cmd.Flags().BoolVar(&yes, "yes", false, "overwrite an existing node without prompting (required non-interactively)")
	return cmd
}

// rejectFlags returns a usage error if any of the named flags was set, so a
// flag belonging to the other import mode fails loudly.
func rejectFlags(cmd *cobra.Command, names []string, why string) error {
	for _, n := range names {
		if cmd.Flags().Changed(n) {
			return exitcode.Newf(exitcode.Usage, "--%s %s", n, why)
		}
	}
	return nil
}

// runImportRestore reconstitutes a node-export file (frontmatter-markdown or
// canonical JSON): a node already at the target loc is updated, else created.
func runImportRestore(cmd *cobra.Command, f *cmdutil.Factory, path, memory, loc, format string, formatChanged, withEdges, createOnly, dryRun, yes bool) error {
	if path == "" {
		return exitcode.Newf(exitcode.Usage, "no input file — pass a <file>/`-`, or --url/--as-content to ingest external content")
	}
	fmtName, err := resolveDocFormat(format, path, formatChanged)
	if err != nil {
		return err
	}

	data, err := readImportSource(path, f.IOStreams.In)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return exitcode.Newf(exitcode.Usage, "empty input — nothing to import")
	}

	var doc *nodedoc.Document
	if fmtName == "json" {
		doc, err = nodedoc.ParseJSON(data)
	} else {
		doc, err = nodedoc.ParseMarkdown(data)
	}
	if err != nil {
		return exitcode.Newf(exitcode.Usage, "%v", err)
	}

	// Target resolution: flag > frontmatter > error.
	memoryRef := firstNonEmpty(memory, doc.MemoryURN)
	if memoryRef == "" {
		return exitcode.Newf(exitcode.Usage, "no target memory — pass -m <memory> or include a `memory:` key in the file")
	}
	targetLoc := firstNonEmpty(loc, doc.Loc)
	if targetLoc == "" {
		return exitcode.Newf(exitcode.Usage, "no target loc — pass --loc <loc> or include a `loc:` key in the file")
	}
	if strings.TrimSpace(doc.Name) == "" {
		return exitcode.Newf(exitcode.Usage, "file has no `name` — a node name is required")
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}

	if dryRun {
		// Classify create vs update by a best-effort existence probe
		// (the executed path derives it from which mutation succeeds).
		action := "created"
		if nodeExists(cmd, client, memoryRef, targetLoc) {
			action = "updated"
		}
		return emitImportSummary(f, importNodeSummaryDTO{
			Mode: "restore", Memory: memoryRef, Loc: targetLoc, Action: action,
			EdgesWired: 0, UnwiredEdges: []unwiredEdgeDTO{},
		}, true, withEdges, len(doc.Edges))
	}

	// An import that lands on an existing node overwrites it — gate that behind
	// the destructive-op regime (#129). --create-only never overwrites (it fails
	// on a live loc), so it skips the probe.
	if !createOnly && nodeExists(cmd, client, memoryRef, targetLoc) {
		if err := confirmOverwrite(f, yes, overwriteTarget(memoryRef, targetLoc)); err != nil {
			return err
		}
	}

	input, err := buildCreateNodeInput(doc, memoryRef, targetLoc)
	if err != nil {
		return err
	}

	// The old upsert is now emulated (spec 039 Phase 0 split the write):
	// without --create-only, try updateNode keyed on (memoryId, loc) and
	// fall back to createNode when the server says NODE_NOT_FOUND; with
	// --create-only, go straight to createNode (a live node at the loc
	// rejects with NodeLocConflictError).
	var nodeID, nodeLoc, action string
	if createOnly {
		resp, err := gen.CreateNode(cmd.Context(), client, input)
		if err != nil {
			return api.MapError(err)
		}
		nodeID, nodeLoc, action = resp.CreateNode.Id, resp.CreateNode.Loc, "created"
	} else {
		uResp, uErr := gen.UpdateNode(cmd.Context(), client, updateNodeInputFrom(input))
		switch {
		case uErr == nil:
			nodeID, nodeLoc, action = uResp.UpdateNode.Id, uResp.UpdateNode.Loc, "updated"
		case api.HasErrorCode(uErr, "NODE_NOT_FOUND"):
			cResp, cErr := gen.CreateNode(cmd.Context(), client, input)
			if cErr != nil {
				return api.MapError(cErr)
			}
			nodeID, nodeLoc, action = cResp.CreateNode.Id, cResp.CreateNode.Loc, "created"
		default:
			return api.MapError(uErr)
		}
	}

	edgesWired, unwired := 0, []unwiredEdgeDTO{}
	if withEdges && len(doc.Edges) > 0 {
		edgesWired, unwired, err = wireEdges(cmd, client, memoryRef, nodeID, doc.Edges)
		if err != nil {
			return err
		}
	}

	summary := importNodeSummaryDTO{
		Mode: "restore", Memory: memoryRef, Loc: nodeLoc, Action: action, NodeID: nodeID,
		EdgesWired: edgesWired, UnwiredEdges: unwired,
	}
	if err := emitImportSummary(f, summary, false, withEdges, len(doc.Edges)); err != nil {
		return err
	}
	// The node was written, but one or more edges were left unwired — a partial
	// success. Exit non-zero (after the report/`unwiredEdges` array is emitted) so
	// a caller branching on the exit code doesn't read partial as complete (#127).
	if len(unwired) > 0 {
		// Count only what was unwired — not a ratio against len(doc.Edges), which
		// is misleading when wireEdges idempotently skipped already-present edges.
		return exitcode.Newf(exitcode.Error,
			"imported %s:%s but %d edge(s) could not be wired (see unwiredEdges above); fix the target(s) and re-run with --with-edges, or wire them with `hadron edge add`",
			summary.Memory, summary.Loc, len(unwired))
	}
	return nil
}

// contentImportOpts carries the content-mode inputs — a struct rather than a
// long positional list now that a post-import task run (--task/--task-args/--app)
// joins the source/target/metadata flags.
type contentImportOpts struct {
	srcPath, url, memory, loc, nodeURN string
	contentType, name, nodeType        string
	properties, propertiesFile         string
	taskRef, taskArgs, app             string
	yes                                bool
}

// runImportContent ingests raw external source (a URL, or a file/stdin holding
// HTML/Markdown/PDF) through the server's importNode, which converts it to the
// node's Markdown body. With --task it also mints a post-import run of that task
// against the stored node (#528).
func runImportContent(cmd *cobra.Command, f *cmdutil.Factory, o contentImportOpts) error {
	srcPath, url, memory, loc, nodeURN := o.srcPath, o.url, o.memory, o.loc, o.nodeURN
	// --task-args/--app only shape a post-import run, so they need --task.
	if o.taskArgs != "" && o.taskRef == "" {
		return exitcode.Newf(exitcode.Usage, "--task-args requires --task")
	}
	if o.app != "" && o.taskRef == "" {
		return exitcode.Newf(exitcode.Usage, "--app requires --task (it names the App the task runs under)")
	}

	// Source dispatch: exactly one of --url | inline (<file>|-).
	hasURL := url != ""
	hasInline := srcPath != ""
	if hasURL == hasInline {
		return exitcode.Newf(exitcode.Usage, "provide exactly one source: a <file>/`-` argument OR --url")
	}

	// Target dispatch: --node XOR (-m/--memory + --loc).
	input := gen.ImportNodeInput{}
	if nodeURN != "" {
		if memory != "" || loc != "" {
			return exitcode.Newf(exitcode.Usage, "identify the target by --node OR by -m/--memory + --loc, not both")
		}
		input.NodeUrn = &nodeURN
	} else {
		if memory == "" || loc == "" {
			return exitcode.Newf(exitcode.Usage, "no target — pass --node <urn>, or both -m/--memory and --loc")
		}
		input.MemoryId = &memory
		input.Loc = &loc
	}

	if o.name != "" {
		input.Name = &o.name
	}
	if o.nodeType != "" {
		input.NodeType = &o.nodeType
	}
	if o.properties != "" || o.propertiesFile != "" {
		props, err := resolveProperties(o.properties, o.propertiesFile)
		if err != nil {
			return err
		}
		input.Properties = props
	}
	if o.taskRef != "" {
		input.TaskRef = &o.taskRef
		// --app is passed verbatim (ID or URN); omitted, the server runs the
		// task under the caller's active App.
		if o.app != "" {
			input.AppRef = &o.app
		}
		if o.taskArgs != "" {
			ta, err := cmdutil.ParseJSONArg(o.taskArgs, "task-args")
			if err != nil {
				return err
			}
			input.TaskArgs = ta
		}
	}

	if hasURL {
		input.Url = &url
		// contentType is ignored on the url path (server always fetches HTML).
	} else {
		data, err := readImportSource(srcPath, f.IOStreams.In)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return exitcode.Newf(exitcode.Usage, "empty input — nothing to import")
		}
		// Explicit --content-type wins; otherwise infer from the file
		// extension (nothing is inferable from stdin).
		ct := o.contentType
		if ct == "" && srcPath != "-" {
			ct = inferContentType(srcPath)
		}
		var content string
		if strings.EqualFold(strings.TrimSpace(ct), "application/pdf") {
			// A PDF is binary; the inline content String carries it base64-encoded.
			content = base64.StdEncoding.EncodeToString(data)
		} else {
			content = string(data)
		}
		input.Content = &content
		// Omit an unset contentType so the server applies its default (text/html).
		if ct != "" {
			input.ContentType = &ct
		}
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}

	// Ingesting onto an existing node overwrites its body — gate that (#129).
	overwrites, target := false, ""
	if nodeURN != "" {
		overwrites, target = nodeExistsByURN(cmd, client, nodeURN), nodeURN
	} else {
		overwrites, target = nodeExists(cmd, client, memory, loc), overwriteTarget(memory, loc)
	}
	if overwrites {
		if err := confirmOverwrite(f, o.yes, target); err != nil {
			return err
		}
	}

	resp, err := gen.ImportNode(cmd.Context(), client, &input)
	if err != nil {
		return api.MapError(err)
	}
	result := resp.ImportNode
	if result == nil {
		// importNode is ImportNodeResult! — a null here is a non-spec-compliant
		// server (a real failure would arrive as a GraphQL error above); guard
		// so it surfaces as an error, not a nil-deref panic.
		return exitcode.Newf(exitcode.Error, "import returned no result")
	}
	if result.Node == nil {
		// Sync v1 always returns the stored node; a nil here means the server
		// changed shape (e.g. an async path landed) — surface it, don't panic.
		return exitcode.Newf(exitcode.Error, "import returned status %s with no node", result.Status)
	}
	n := result.Node

	// Prefer the user-supplied memory ref for the human line (friendlier than
	// the returned raw id); the DTO carries the authoritative id.
	memDisplay := memory
	if memDisplay == "" {
		memDisplay = n.MemoryId
	}
	jobID := ""
	if result.JobId != nil {
		jobID = *result.JobId
	}
	dto := importContentSummaryDTO{
		Mode:     "content",
		Status:   string(result.Status),
		Memory:   memDisplay,
		Loc:      n.Loc,
		NodeID:   n.Id,
		Name:     n.Name,
		NodeType: n.NodeType,
		JobID:    jobID,
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		fmt.Fprintf(w, "✓ imported %s:%s (%s)\n", dto.Memory, dto.Loc, dto.NodeType)
		// A --task import mints a run against the stored node (jobId is its id).
		if dto.JobID != "" {
			fmt.Fprintf(w, "  task run started: %s\n  follow it with: hadron run get %s\n", dto.JobID, dto.JobID)
		}
		return nil
	})
}

// readImportSource reads the node file, or stdin when path is "-".
func readImportSource(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: %v", path, err)
	}
	return data, nil
}

// inferContentType maps a source file's extension to importNode's contentType,
// or "" when nothing is inferable (an unknown extension defers to the server
// default, text/html).
func inferContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "application/pdf"
	case ".html", ".htm":
		return "text/html"
	case ".md", ".markdown":
		return "text/markdown"
	default:
		return ""
	}
}

// resolveProperties reads the provenance JSON from --properties (inline) or
// --properties-file, validates it, and returns it ready for the input. Callers
// gate the call so an unset flag stays omitted from the wire.
func resolveProperties(properties, propertiesFile string) (*json.RawMessage, error) {
	if properties != "" && propertiesFile != "" {
		return nil, exitcode.Newf(exitcode.Usage, "--properties and --properties-file are mutually exclusive")
	}
	raw := strings.TrimSpace(properties)
	flag := "--properties"
	if propertiesFile != "" {
		b, err := os.ReadFile(propertiesFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading --properties-file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
		flag = "--properties-file"
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain valid JSON", flag)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}

// buildCreateNodeInput maps a Document onto the create input (the canonical
// field set; updateNodeInputFrom derives the update shape from it). Empty
// optional fields are omitted (preserve-on-update) rather than sent as a
// clear; the id and the recompute-only hashes (contentHash,
// abstractOriginHash) are intentionally not sent — the write keys on
// (memory, loc) and the server owns the hashes.
func buildCreateNodeInput(doc *nodedoc.Document, memoryRef, targetLoc string) (*gen.CreateNodeInput, error) {
	input := &gen.CreateNodeInput{
		MemoryId: memoryRef,
		Loc:      targetLoc,
		Name:     doc.Name,
	}
	if doc.Content != "" {
		input.Content = &doc.Content
	}
	if doc.Type != "" {
		input.NodeType = &doc.Type
	}
	if doc.Alias != "" {
		input.Alias = &doc.Alias
	}
	if doc.Description != "" {
		input.Description = &doc.Description
	}
	if doc.Abstract != "" {
		input.Abstract = &doc.Abstract
	}
	if len(doc.Tags) > 0 {
		input.Tags = doc.Tags
	}
	if doc.Seq != nil {
		input.Seq = doc.Seq
	}
	if doc.Data != nil {
		data, err := nodedoc.EncodeJSON(doc.Data)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "encoding data: %v", err)
		}
		input.Data = data
	}
	if doc.Properties != nil {
		props, err := nodedoc.EncodeJSON(doc.Properties)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "encoding properties: %v", err)
		}
		input.Properties = props
	}
	return input, nil
}

// updateNodeInputFrom derives the updateNode input from the assembled create
// shape: the target is selected by (memoryId, loc), and every doc-supplied
// field — the file's name included, since an import means "make the node
// match the file" — is carried over verbatim. Fields the file omits stay
// omitted (nil), which updateNode reads as "preserve".
//
// Every field the two input structs share is mapped, whether or not
// buildCreateNodeInput populates it today, so a future doc field can't be
// silently dropped on the update path (TestUpdateNodeInputFromMapsAllFields
// enforces this). The one exclusion is Id: on CreateNodeInput it is a forced
// create-PK, on UpdateNodeInput it is the target selector — carrying it over
// would collide with the (memoryId, loc) selector pair (id XOR memoryId+loc).
func updateNodeInputFrom(in *gen.CreateNodeInput) *gen.UpdateNodeInput {
	name := in.Name
	return &gen.UpdateNodeInput{
		MemoryId:    &in.MemoryId,
		Loc:         &in.Loc,
		Name:        &name,
		Content:     in.Content,
		ContentType: in.ContentType,
		NodeType:    in.NodeType,
		Alias:       in.Alias,
		Description: in.Description,
		Abstract:    in.Abstract,
		Tags:        in.Tags,
		Seq:         in.Seq,
		Data:        in.Data,
		Properties:  in.Properties,
		Edges:       in.Edges,
		IsRunnable:  in.IsRunnable,
		AiAgent:     in.AiAgent,
		LlmModel:    in.LlmModel,
		OwnerRepo:   in.OwnerRepo,
		Reason:      in.Reason,
	}
}

// nodeExists best-effort probes whether a node already lives at (memory, loc),
// to label the import created vs updated. Any lookup error yields false: the
// upsert that follows is authoritative for real failures (auth/transport), and
// a dry run degrades to "would create".
func nodeExists(cmd *cobra.Command, client graphql.Client, memoryRef, loc string) bool {
	// An org::memory-shaped ref composes an exact node URN in one round-trip.
	// (cmdutil.NodeURN normalizes the separators — building the URN by hand as
	// memoryRef+":"+loc produced a single-colon `org::memory:loc` that never
	// resolved, so the overwrite probe silently missed every existing node.)
	if urn := cmdutil.NodeURN(memoryRef, loc); urn != "" {
		resp, err := gen.ResolveUrn(cmd.Context(), client, urn)
		return err == nil && resp.ResolveUrn != nil && resp.ResolveUrn.Kind == "node"
	}
	// A raw memory id: list by loc prefix and match the exact loc.
	limit := 200
	filter := &gen.NodeFilter{MemoryIds: []string{memoryRef}, LocPrefix: &loc}
	sort := gen.NodeSortLoc
	for offset := 0; ; offset += limit {
		off := offset
		page, err := api.FindNodes(cmd.Context(), client, nil, nil, filter, &sort, &limit, &off)
		if err != nil {
			return false
		}
		for _, nd := range page.Nodes {
			if nd != nil && nd.Loc == loc {
				return true
			}
		}
		if len(page.Nodes) < limit {
			return false
		}
	}
}

// nodeExistsByURN best-effort probes whether a fully-qualified node URN already
// resolves to a live node — the content-mode (`--node`) counterpart to
// nodeExists, used to gate an importNode overwrite (#129).
func nodeExistsByURN(cmd *cobra.Command, client graphql.Client, ref string) bool {
	urn := ref
	if !strings.HasPrefix(urn, "hrn:") && !strings.HasPrefix(urn, "urn:") {
		urn = "hrn:node:" + urn
	}
	resp, err := gen.ResolveUrn(cmd.Context(), client, urn)
	return err == nil && resp.ResolveUrn != nil && resp.ResolveUrn.Kind == "node"
}

// confirmOverwrite gates an import that would OVERWRITE an existing node behind
// the destructive-op regime (#129): a live node at the target prompts on a TTY
// and requires --yes non-interactively, matching `replace text`. A create (or
// an unresolvable best-effort probe) proceeds without a prompt.
func confirmOverwrite(f *cmdutil.Factory, yes bool, target string) error {
	return cmdutil.Confirm(f.IOStreams, yes,
		fmt.Sprintf("node %s already exists and will be overwritten (a prior version is kept) — continue?", target))
}

// overwriteTarget is an unambiguous label for the node an import would
// overwrite: the canonical node URN when it can be composed, else the raw-id
// memoryRef:loc (a memory id can't form a URN). Avoids the ambiguous
// single-colon memoryRef:loc for the common org::memory case.
func overwriteTarget(memoryRef, loc string) string {
	if urn := cmdutil.NodeURN(memoryRef, loc); urn != "" {
		return urn
	}
	return memoryRef + ":" + loc
}

// emitImportSummary writes the --json DTO or the human line. dryRun and
// withEdges/fileEdges tune the human hint (would-do vs did; the unwired-edges
// note vs the "re-run with --with-edges" nudge).
func emitImportSummary(f *cmdutil.Factory, s importNodeSummaryDTO, dryRun, withEdges bool, fileEdges int) error {
	return output.Write(f.IOStreams, f.JSON, s, func(w io.Writer) error {
		if dryRun {
			verb := "create"
			if s.Action == "updated" {
				verb = "update"
			}
			fmt.Fprintf(w, "[dry-run] would %s %s:%s\n", verb, s.Memory, s.Loc)
		} else {
			fmt.Fprintf(w, "✓ %s %s:%s\n", s.Action, s.Memory, s.Loc)
		}
		switch {
		case withEdges:
			fmt.Fprintf(w, "  wired %d edge(s)\n", s.EdgesWired)
			if len(s.UnwiredEdges) > 0 {
				fmt.Fprintf(w, "  %d edge(s) not wired:\n", len(s.UnwiredEdges))
				for _, u := range s.UnwiredEdges {
					fmt.Fprintf(w, "    - %s: %s\n", u.Target, u.Reason)
				}
			}
		case fileEdges > 0:
			fmt.Fprintf(w, "  %d edge(s) in file — re-run with --with-edges to wire them\n", fileEdges)
		}
		return nil
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
