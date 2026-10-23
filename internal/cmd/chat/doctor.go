package chat

import (
	"fmt"
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

// `chat config show` and `chat doctor` are the READ-ONLY half of #477: a
// .hadron/config.json that points at the wrong chat goes stale silently,
// because read and post accept whatever it resolves to. These say what that is
// and where each value came from. Neither writes config, posts, or creates a
// Channel — the writer is a separately gated residual on #477.
//
// THE DTO IS AN ALLOWLIST. It is built from the parsed known keys only, never
// from the raw file, so an appKey or any unrelated key cannot reach the output
// by construction rather than by redaction.

// configFileDTO is where the ancestor search ended.
type configFileDTO struct {
	Path    *string            `json:"path"`
	Status  string             `json:"status"`
	Problem *string            `json:"problem"`
	Skipped []skippedConfigDTO `json:"skipped"`
}

type skippedConfigDTO struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type coordInputDTO struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Used   bool   `json:"used"`
}

// deprecatedInputDTO is a legacy handle/identity/role value. Effect is always
// "none": post ignores them, and reporting them as an identity would revive
// the hand-typed attribution the Worker model replaced.
type deprecatedInputDTO struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type resolvedDTO struct {
	Form        string `json:"form"`
	Memory      string `json:"memory"`
	MessagesLoc string `json:"messagesLoc"`
}

// configShowDTO is the stable --json shape of `chat config show`.
type configShowDTO struct {
	Config     configFileDTO        `json:"config"`
	Resolved   *resolvedDTO         `json:"resolved"`
	Problem    *string              `json:"problem"`
	Inputs     []coordInputDTO      `json:"inputs"`
	Deprecated []deprecatedInputDTO `json:"deprecated"`
}

// channelCheckDTO is doctor's Channel read. Lifecycle is always
// "not-provided": a Channel carries no archived/live/superseded fact, so none
// is reported, and lastSeq / lastMessageAt are left out rather than offered
// as a proxy for one — an old timestamp is a quiet chat, not a retired one.
type channelCheckDTO struct {
	Ref         *string `json:"ref"`
	Status      string  `json:"status"` // readable | unavailable | not-checked
	ID          *string `json:"id"`
	Name        *string `json:"name"`
	ChatRootUrn *string `json:"chatRootUrn"`
	Memory      *string `json:"memory"`
	Lifecycle   string  `json:"lifecycle"`
}

type problemDTO struct {
	Check  string `json:"check"` // config | coordinates | channel
	Detail string `json:"detail"`
}

// doctorDTO is the stable --json shape of `chat doctor`: the show report plus
// the Channel check and a verdict.
type doctorDTO struct {
	configShowDTO
	Channel  channelCheckDTO `json:"channel"`
	OK       bool            `json:"ok"`
	Problems []problemDTO    `json:"problems"`
}

const lifecycleNotProvided = "not-provided"

func newCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <command>",
		Short: "Inspect the chat coordinates from flags, env and .hadron/config.json",
	}
	cmd.AddCommand(newCmdConfigShow(f))
	return cmd
}

// coordFlags registers the coordinate flags read and post take, so show and
// doctor answer for exactly the invocation being diagnosed.
func coordFlags(cmd *cobra.Command, node, memory, messagesLoc *string) {
	cmd.Flags().StringVar(node, "node", "", "message-parent node URN (hrn:node:<root>:<slug>:<loc>); packs memory + message location")
	cmd.Flags().StringVarP(memory, "memory", "m", "", "chat memory (hrn:mem:<root>:<slug>); the two-field form with --messages-loc")
	cmd.Flags().StringVar(messagesLoc, "messages-loc", "", "message-parent loc prefix; the two-field form with -m")
	cmd.MarkFlagsMutuallyExclusive("node", "memory")
	cmd.MarkFlagsMutuallyExclusive("node", "messages-loc")
}

func newCmdConfigShow(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the chat `chat read|post` would use, and where each value came from",
		Long: `Show the chat coordinates ` + "`chat read`" + ` and ` + "`chat post`" + ` would resolve from
here, offline: which .hadron/config.json was found (nearest ancestor), every
supplied coordinate with its source (flag, env, config) and whether it won, and
the legacy handle/identity/role values, which have NO effect on a post.

Precedence is the commands' own: a flag, then HADRON_CHAT_*, then the config
file; and the node form (--node / HADRON_CHAT_NODE / chat.node, from any
source) beats the two-field form (-m + --messages-loc).

Reports, never fails, on an absent, malformed or unresolvable config (exit 0):
use ` + "`chat doctor`" + ` for a pass/fail check. Only the known chat keys are
printed — never appKey or any other key in the file. Nothing is written.`,
		Example: `  hadron chat config show
  hadron chat config show --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dto, _, _ := buildShow(node, memory, messagesLoc)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				writeShow(w, dto)
				return nil
			})
		},
	}
	coordFlags(cmd, &node, &memory, &messagesLoc)
	return cmd
}

// buildShow runs the same search and resolution read/post run, and reports it.
func buildShow(node, memory, messagesLoc string) (configShowDTO, Coords, error) {
	pc, cf := findProjectChat()
	c, tr, err := resolveCoordsTraced(pc, node, memory, messagesLoc)

	dto := configShowDTO{
		Config:     configFileDTO{Status: cf.Status, Skipped: []skippedConfigDTO{}},
		Inputs:     []coordInputDTO{},
		Deprecated: []deprecatedInputDTO{},
	}
	if cf.Path != "" {
		dto.Config.Path = &cf.Path
	}
	if cf.Problem != "" {
		dto.Config.Problem = &cf.Problem
	}
	for _, s := range cf.Skipped {
		dto.Config.Skipped = append(dto.Config.Skipped, skippedConfigDTO(s))
	}
	for _, in := range tr.Inputs {
		dto.Inputs = append(dto.Inputs, coordInputDTO(in))
	}
	if err != nil {
		msg := err.Error()
		dto.Problem = &msg
	} else {
		dto.Resolved = &resolvedDTO{Form: tr.Form, Memory: c.Memory, MessagesLoc: c.MessagesLoc}
	}
	// No flags here: show and doctor take no --handle/--identity/--role, so
	// these are only what a bare post would pick up and ignore.
	for _, d := range []deprecatedInputDTO{
		{Key: "handle", Source: "env", Name: "HADRON_CHAT_HANDLE", Value: os.Getenv("HADRON_CHAT_HANDLE")},
		{Key: "handle", Source: "config", Name: "handle", Value: pc.Handle},
		{Key: "identity", Source: "config", Name: "chat.identity", Value: pc.Identity},
		{Key: "role", Source: "config", Name: "chat.role", Value: pc.Role},
	} {
		if v := strings.TrimSpace(d.Value); v != "" {
			d.Value, d.Effect = v, "none"
			dto.Deprecated = append(dto.Deprecated, d)
		}
	}
	return dto, c, err
}

func writeShow(w io.Writer, dto configShowDTO) {
	switch {
	case dto.Config.Path == nil:
		fmt.Fprintln(w, "config: no .hadron/config.json in this directory or any parent")
	case dto.Config.Status == "malformed":
		fmt.Fprintf(w, "config: %s (MALFORMED — read and post ignore it: %s)\n", *dto.Config.Path, *dto.Config.Problem)
	default:
		fmt.Fprintf(w, "config: %s\n", *dto.Config.Path)
	}
	for _, s := range dto.Config.Skipped {
		fmt.Fprintf(w, "  skipped (unreadable): %s — %s\n", s.Path, s.Reason)
	}
	if dto.Resolved != nil {
		fmt.Fprintf(w, "chat (%s form):\n  memory:      %s\n  messagesLoc: %s\n",
			dto.Resolved.Form, dto.Resolved.Memory, dto.Resolved.MessagesLoc)
	} else {
		fmt.Fprintf(w, "chat: unresolved — %s\n", *dto.Problem)
	}
	if len(dto.Inputs) > 0 {
		fmt.Fprintln(w, "inputs (precedence order):")
		for _, in := range dto.Inputs {
			mark := "unused"
			if in.Used {
				mark = "USED"
			}
			fmt.Fprintf(w, "  %-6s %-24s %s = %s\n", mark, in.Name+" ("+in.Source+")", in.Key, in.Value)
		}
	}
	if len(dto.Deprecated) > 0 {
		fmt.Fprintln(w, "legacy identity values (no effect on posts; authorship comes from --session):")
		for _, d := range dto.Deprecated {
			fmt.Fprintf(w, "  %s (%s) = %s\n", d.Name, d.Source, d.Value)
		}
	}
}

func newCmdDoctor(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the configured chat resolves and its Channel is readable",
		Long: `Check the chat ` + "`chat read|post`" + ` would use from here: everything
` + "`chat config show`" + ` reports, plus ONE read of the Channel at the resolved chat
root. Read-only: it never posts, never creates the Channel (post does that on
first use), and never edits config.

Exit 0 only when the config file (if any) parsed, the coordinates resolved and
the Channel is readable. Otherwise the report still prints, and the exit is the
first failing check's: 2 for a malformed or unreadable config file or
unresolvable coordinates, 4 when no Channel is readable at the chat root. A
server refusal or transport failure exits with its own code (e.g. 8, 7).

"unavailable" covers a Channel that does not exist, a ref naming nothing, and
one whose host memory you may not read — the server answers all three
identically, so this does not say which. ` + "`lifecycle`" + ` is always "not-provided":
a Channel carries no archived/live/superseded fact, and this command does not
guess one from its last-message time.`,
		Example: `  hadron chat doctor
  hadron chat doctor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			show, c, resolveErr := buildShow(node, memory, messagesLoc)
			dto := doctorDTO{
				configShowDTO: show,
				Channel:       channelCheckDTO{Status: "not-checked", Lifecycle: lifecycleNotProvided},
				Problems:      []problemDTO{},
			}
			fail := exitcode.OK
			problem := func(check, detail string, code int) {
				dto.Problems = append(dto.Problems, problemDTO{Check: check, Detail: detail})
				if fail == exitcode.OK {
					fail = code
				}
			}
			if show.Config.Status == "malformed" {
				problem("config", fmt.Sprintf("%s does not parse, so read and post silently ignore it: %s", *show.Config.Path, *show.Config.Problem), exitcode.Usage)
			}
			for _, s := range show.Config.Skipped {
				problem("config", fmt.Sprintf("%s could not be read and was skipped: %s", s.Path, s.Reason), exitcode.Usage)
			}
			if resolveErr != nil {
				problem("coordinates", resolveErr.Error(), exitcode.Usage)
			} else {
				client, err := f.GraphQLClient()
				if err != nil {
					return err
				}
				ref, err := chatRootRef(cmd.Context(), client, c)
				if err != nil {
					return err
				}
				dto.Channel.Ref = &ref
				resp, err := gen.GetChannel(cmd.Context(), client, ref)
				if err != nil {
					return api.MapError(err)
				}
				if resp == nil || resp.Channel == nil {
					dto.Channel.Status = "unavailable"
					problem("channel", fmt.Sprintf("no Channel is readable at %s — it does not exist, the ref names nothing, or you may not read its host memory (the server does not say which)", ref), exitcode.NotFound)
				} else {
					ch := resp.Channel
					id, name, mem := ch.Id, ch.Name, ch.Memory.Urn
					dto.Channel.Status = "readable"
					dto.Channel.ID, dto.Channel.Name, dto.Channel.ChatRootUrn, dto.Channel.Memory = &id, &name, ch.ChatRootUrn, &mem
				}
			}
			dto.OK = fail == exitcode.OK
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				writeShow(w, dto.configShowDTO)
				writeChannelCheck(w, dto)
				return nil
			}); err != nil {
				return err
			}
			if fail != exitcode.OK {
				return exitcode.Silent(fail)
			}
			return nil
		},
	}
	coordFlags(cmd, &node, &memory, &messagesLoc)
	return cmd
}

func writeChannelCheck(w io.Writer, dto doctorDTO) {
	switch dto.Channel.Status {
	case "readable":
		fmt.Fprintf(w, "channel: readable — %s (%s)", *dto.Channel.Name, *dto.Channel.ID)
		if dto.Channel.ChatRootUrn != nil {
			fmt.Fprintf(w, " at %s", *dto.Channel.ChatRootUrn)
		}
		fmt.Fprintln(w)
	case "unavailable":
		fmt.Fprintf(w, "channel: unavailable at %s\n", *dto.Channel.Ref)
	default:
		fmt.Fprintln(w, "channel: not checked")
	}
	fmt.Fprintln(w, "lifecycle: not provided by the server (no archived/live/superseded fact exists to report)")
	if dto.OK {
		// Scoped to what was read: whether this is the chat your team actually
		// uses is not something any field here can answer.
		fmt.Fprintln(w, "ok: config parsed, chat resolved, Channel readable (not checked: whether this is the chat your team uses)")
		return
	}
	for _, p := range dto.Problems {
		fmt.Fprintf(w, "problem (%s): %s\n", p.Check, p.Detail)
	}
}
