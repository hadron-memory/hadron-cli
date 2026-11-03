package node

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		memory         string
		install        string
		loc            string
		name           string
		content        string
		contentFile    string
		allowEmpty     bool
		nodeType       string
		objectType     string
		description    string
		abstract       string
		data           string
		dataFile       string
		properties     string
		propertiesFile string
		runnable       bool
		role           string
		tags           []string
		edges          []string
		id             string
		seq            int
	)
	cmd := &cobra.Command{
		Use:     "create",
		Aliases: []string{"add"},
		Short:   "Create a node",
		Long: `Create a note (a node) in a memory. Give the memory with -m, the node's loc
(its path inside the memory, such as findings:flaky-ci) with --loc, a --name,
and the content:

  hadron node create -m hrn:mem:acme.com:kb --loc findings:flaky-ci \
    --name "Flaky CI" --content-file finding.md

On success it prints "✓ created <loc> <name>"; --json prints the new node,
including its id. It fails if a node already exists at that loc: use node
update to change one. You need write access to the memory.

Content: --content takes it inline, --content-file reads a file. Either one
takes "-" to read piped input (cat finding.md | hadron node create ...
--content -). "-" is refused when input is a terminal rather than a pipe,
because a terminal can cut long input short and the node would be stored
truncated. An empty --content-file is refused too (a failed command upstream
leaves exactly that), unless you pass --allow-empty.

Three different words for "what kind of node":
  --type          the platform's kind of node; leave it out for an ordinary
                  note (comments are made with hadron comment create)
  --object-type   the collection a record belongs to (for example competitor),
                  used with --properties; see the object commands
  --role          what the node is for in a governed workflow: "spec", "spec.*",
                  "review" and "review.*" are created through that workflow's
                  rules; leave it out otherwise

--properties / --properties-file set the record's fields (what --where and
--sort-property search on); --data / --data-file set a separate free-form JSON
object. On a memory with a schema, the server checks --object-type and
--properties against it and refuses a record that does not fit.

--runnable makes the node a task that hadron task run can run.

--edge links the new node to another one in the same write, so the node never
exists without its links. Repeat it for several. The short form is
<name>=<target> (routes-to=start-here); a JSON object also sets the edge's
loc, description and runnable: --edge '{"target":"start-here","name":"routes-to",
"description":"why"}'. The target is a node id, a node URN, or a loc in this
memory. --id creates the node with an id you choose (for importers and syncs)
instead of a minted one.

Details:

` + installDetails("The first create there makes the memory together with the node; a refused\ncreate leaves nothing behind.") + ``,
		Example: `  hadron node create -m hrn:mem:acme.com:kb --loc findings:flaky-ci --name "Flaky CI" --content "CI fails on every third run."
  cat finding.md | hadron node create -m hrn:mem:acme.com:kb --loc findings:flaky-ci --name "Flaky CI" --content -
  hadron node create -m hrn:mem:acme.com:research --loc competitors:acme --name Acme \
    --object-type competitor --properties '{"tier":"enterprise","seats":50}'
  hadron node create -m hrn:mem:acme.com:kb --loc findings:flaky-ci --name "Flaky CI" --edge routes-to=start-here`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := installTarget(cmd, install, []string{loc})
			if err != nil {
				return err
			}
			if target == nil && strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "pass --memory or --install to name the target memory")
			}
			if err := cmdutil.ValidateURNPath("--loc", loc); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			body, err := resolveContent(content, contentFile, f.IOStreams.In, f.IOStreams.IsInputTerminal(), allowEmpty)
			if err != nil {
				return err
			}

			input := gen.CreateNodeInput{
				MemoryId: memory,
				Install:  target,
				Loc:      loc,
				Name:     name,
				Tags:     tags,
			}
			if body != "" {
				input.Content = &body
			}
			if nodeType != "" {
				input.NodeType = &nodeType
			}
			if objectType != "" {
				input.ObjectType = &objectType
			}
			if description != "" {
				input.Description = &description
			}
			if abstract != "" {
				input.Abstract = &abstract
			}
			// Gate on Changed() (not the value): an explicit --data "" / --properties ""
			// is a deliberate write of an empty/invalid bag — it must reach
			// resolveJSONObject (and error as invalid JSON), matching node update, and
			// must still trip mutual exclusion against its -file companion.
			changed := cmd.Flags().Changed
			if changed("data") && changed("data-file") {
				return exitcode.Newf(exitcode.Usage, "--data and --data-file are mutually exclusive")
			}
			if changed("properties") && changed("properties-file") {
				return exitcode.Newf(exitcode.Usage, "--properties and --properties-file are mutually exclusive")
			}
			if changed("data") || changed("data-file") {
				raw, err := resolveJSONObject("--data", data, dataFile)
				if err != nil {
					return err
				}
				input.Data = raw
			}
			if changed("properties") || changed("properties-file") {
				raw, err := resolveJSONObject("--properties", properties, propertiesFile)
				if err != nil {
					return err
				}
				input.Properties = raw
			}
			if cmd.Flags().Changed("runnable") {
				input.IsRunnable = &runnable
			}
			// cli#479: sent only when given; explicit 0 is a position, absent is not.
			if changed("seq") {
				input.Seq = &seq
			}
			// #1201 — setting a governed role routes this create through that
			// kind's door (see api.CreateNodeByKind). Omitted leaves the node
			// ungoverned, which the generic surface accepts.
			if cmd.Flags().Changed("role") {
				// A node cannot be two governed kinds at once — no door can
				// write it, and the server's refusal names one that would also
				// refuse (@codex on #615). Caught here so the message says the
				// true thing.
				if kinds := api.GovernedKindConflict(&role, runnable && cmd.Flags().Changed("runnable")); kinds != nil {
					return exitcode.Newf(exitcode.Usage,
						"a node cannot be two governed kinds at once — this would be %s, and each door is exempt from its OWN kind only, so every one of them refuses it. Drop one, or write it with `hadron api` if the server's register has changed",
						strings.Join(kinds, " AND "))
				}
				// Blank roles mean no role; omission also works on older servers.
				if strings.TrimSpace(role) != "" {
					input.Role = &role
				}
			}

			// cli#1341: outgoing edges written with the node, atomically, and a
			// client-chosen id. Both are refused here when malformed or empty.
			if changed("edge") {
				in, err := parseEdgeFlags(edges)
				if err != nil {
					return err
				}
				input.Edges = in
			}
			if changed("id") {
				if strings.TrimSpace(id) == "" {
					return exitcode.Newf(exitcode.Usage, "--id is empty: give the id to create the node with, or drop the flag. Nothing was sent")
				}
				v := strings.TrimSpace(id)
				input.Id = &v
			}

			// Dispatch by KIND, not by command (#1201). `--runnable` produces a
			// task-kind node, which the generic `createNode` REFUSES — so this
			// command was broken against the server for exactly the flag it
			// advertises (@codex on #614, reproduced before it was believed).
			resp, err := api.CreateNodeByKind(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}

			dto := createDTO(resp)
			dto.LintWarnings = loadLintWarnings(cmd, client, dto.ID)
			return writeWithLint(f, dto, dto.LintWarnings, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ created", output.SafeCell(dto.Loc), output.SafeCell(dto.Name))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (or --install)")
	cmd.Flags().StringVar(&install, "install", "", installFlagHelp)
	cmd.Flags().StringVar(&loc, "loc", "", "node location within the memory (required)")
	cmd.Flags().StringVar(&name, "name", "", "node name (required)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `node content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", `read node content from a file ("-" reads piped stdin)`)
	cmd.Flags().BoolVar(&allowEmpty, "allow-empty", false, "accept a zero-byte --content-file (or empty piped stdin) as a deliberately empty body; without it that is refused, since a failed upstream command leaves exactly that file")
	cmd.Flags().StringVar(&nodeType, "type", "", "node type (defaults to the server default; comments use hadron comment create)")
	cmd.Flags().StringVar(&objectType, "object-type", "", "collection this record belongs to (e.g. competitor); not --type")
	cmd.Flags().StringVar(&description, "description", "", "one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", "paragraph-length summary")
	cmd.Flags().StringVar(&data, "data", "", "machine-readable JSON data object")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the JSON data object from a file")
	cmd.Flags().StringVar(&properties, "properties", "", "the record's fields as a JSON object (checked against the memory's schema, if it has one)")
	cmd.Flags().StringVar(&propertiesFile, "properties-file", "", "read the JSON properties object from a file")
	// See node update's --role: NOT --type, and NOT a membership role.
	cmd.Flags().StringVar(&role, "role", "",
		`what this node is for in a governed workflow ("spec", "spec.*", "review", "review.*"); blank means no role. Not --type, and not a membership role`)
	cmd.Flags().BoolVar(&runnable, "runnable", false, "mark the node runnable by 'hadron task run'")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringArrayVar(&edges, "edge", nil, edgeFlagUsage)
	cmd.Flags().IntVar(&seq, "seq", 0, "position among sibling nodes, lower first (0 is a real position; omit for the default); "+seqRefusalHelp)
	cmd.Flags().StringVar(&id, "id", "", "create the node with this id instead of a minted one (importers and syncs)")
	cmd.MarkFlagsOneRequired("memory", "install")
	cmd.MarkFlagsMutuallyExclusive("memory", "install")
	_ = cmd.MarkFlagRequired("loc")
	_ = cmd.MarkFlagRequired("name")
	enableLintWarnings(cmd)
	return cmd
}

// resolveContent reads the node body from --content, --content-file, or stdin.
// The stdin path goes through cmdutil.ReadDocumentStdin, which refuses an
// interactive terminal — see there for why a node body is a document read and
// `--data-key -` is not (#643).
//
// A zero-byte --content-file (or empty piped `--content-file -`) is refused
// unless allowEmpty (#1035): it is the signature of a failed upstream command,
// and an empty body is a real clear on the wire. `--content ""` and `--content -`
// are unchanged.
func resolveContent(content, contentFile string, stdin io.Reader, stdinIsTerminal, allowEmpty bool) (string, error) {
	if content != "" && contentFile != "" {
		return "", exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
	}
	if contentFile == "-" {
		body, err := cmdutil.ReadDocumentStdin(stdin, stdinIsTerminal, "--content-file -", "--content-file")
		if err != nil {
			return "", err
		}
		return body, cmdutil.RefuseEmptyFileInput(body, "stdin", "--content-file -", "--allow-empty", allowEmpty)
	}
	if contentFile != "" {
		data, err := os.ReadFile(contentFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --content-file: %v", err)
		}
		return string(data), cmdutil.RefuseEmptyFileInput(string(data), "file "+contentFile, "--content-file", "--allow-empty", allowEmpty)
	}
	if content == "-" {
		return cmdutil.ReadDocumentStdin(stdin, stdinIsTerminal, "--content -", "--content-file")
	}
	return content, nil
}

// resolveJSONObject reads a node JSON bag (data or properties) from an inline
// flag or its companion --*-file, validates it, and returns a raw message ready
// for a create/update input field. The value REPLACES the whole bag on write
// (the server preserves an omitted field and overwrites a supplied one); pass
// `null` to clear it. flag is the base flag name (e.g. "--data",
// "--properties") — the "-file" variant is derived for error messages.
//
// The two sources are mutually exclusive, but that's enforced at the call site
// on cmd.Flags().Changed() (an inline value of "" is a *set* empty string, which
// a value check here couldn't tell from unset) — so this only reads the source
// that's present, file winning when both values are non-empty. Callers gate the
// call on Changed() so an unset flag stays omitted from the wire, and an
// explicit "" reaches here to fail as invalid JSON.
func resolveJSONObject(flag, inline, file string) (*json.RawMessage, error) {
	raw := strings.TrimSpace(inline)
	name := flag
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading %s-file: %v", flag, err)
		}
		raw = strings.TrimSpace(string(b))
		name = flag + "-file"
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain valid JSON (use `null` to clear)", name)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}
