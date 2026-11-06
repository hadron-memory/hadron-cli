package team

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type terminalTitleWorkerDTO struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	URN  *string `json:"urn"`
}

type terminalTitlePartsDTO struct {
	Name      string                  `json:"name"`
	Role      *string                 `json:"role"`
	Model     *string                 `json:"model"`
	Family    *string                 `json:"family"`
	ReportsTo *terminalTitleWorkerDTO `json:"reports_to"`
}

type terminalTitleDTO struct {
	Title            string                 `json:"title"`
	Worker           terminalTitleWorkerDTO `json:"worker"`
	Parts            terminalTitlePartsDTO  `json:"parts"`
	ModelSource      string                 `json:"model_source"`
	TeamPlacement    *string                `json:"teamPlacement"`
	ReportsToRetired bool                   `json:"reportsToRetired"`
}

var terminalTitleDelimiters = strings.NewReplacer("[", " ", "]", " ", ",", " ")

// Scrub before testing emptiness; raw nonempty control-only values are not labels.
func terminalTitleElement(raw string) string {
	safe := output.SafeLine(raw, utf8.RuneCountInString(raw)+1)
	safe = terminalTitleDelimiters.Replace(safe)
	return strings.Join(strings.Fields(safe), " ")
}

func terminalTitleFromWorker(w gen.WorkerFields) terminalTitleDTO {
	dto := terminalTitleDTO{
		Worker:      terminalTitleWorkerDTO{ID: w.Id, Name: w.Name, URN: w.Urn},
		Parts:       terminalTitlePartsDTO{Name: w.Name, Role: w.Role, Model: w.Model, Family: w.Family},
		ModelSource: "none", TeamPlacement: placementString(w.TeamPlacement), ReportsToRetired: w.ReportsToRetired,
	}
	role := terminalTitleElement(titleRaw(w.Role))
	if role == "" {
		role = "role unavailable"
	}
	model := terminalTitleElement(titleRaw(w.Model))
	if model != "" {
		dto.ModelSource = "reported-by-session"
	} else {
		model = terminalTitleElement(titleRaw(w.Family))
		if model != "" {
			dto.ModelSource = "family-fallback"
		} else {
			model = "model unavailable"
		}
	}
	elements := []string{role, model}
	if w.ReportsTo != nil {
		dto.Parts.ReportsTo = &terminalTitleWorkerDTO{ID: w.ReportsTo.Id, Name: w.ReportsTo.Name, URN: w.ReportsTo.Urn}
		if leader := terminalTitleElement(w.ReportsTo.Name); leader != "" {
			elements = append(elements, leader)
		}
	}
	name := terminalTitleElement(w.Name)
	if name == "" {
		name = "worker unavailable"
	}
	title := name + " [" + strings.Join(elements, ", ") + "]"
	runes := []rune(title)
	if len(runes) > 256 {
		title = string(runes[:254]) + "…]"
	}
	dto.Title = title
	return dto
}

func newCmdWorkerTerminalTitle(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "terminal-title <name-or-id>",
		Short: "Print a worker's current terminal title",
		Long: `Print one display label: <Name> [<role>, <model>, <leader>].
The leader element is omitted when reportsTo is null or its name scrubs to empty.
A worker name that scrubs to empty becomes "worker unavailable". A retired leader's name
is kept. References and retired workers resolve as in worker get; a name uses
--app, App context or the worktree binding, while an id or worker URN needs no App.

The model is the raw session-reported value, then its family, then "model
unavailable"; it is self-reported and advisory, never verified identity or
authorization. A missing or blank role becomes "role unavailable". A masked
model or leader does not prove an unbound worker or withheld reporting.

Each element replaces controls, format/bidi characters, line/paragraph
separators and literal brackets/commas with spaces, then collapses whitespace.
The title is capped at 256 Unicode runes, with an ellipsis inside the closing
bracket when truncated. Text stdout is only the title and a newline.
--json retains original strings/nulls in worker and parts, with model_source
(reported-by-session, family-fallback or none), raw nullable teamPlacement and
reportsToRetired, alongside the sanitized title.

One worker query, no mutation. This command does not set or refresh terminal
titles: host callers apply its output separately. The title is a display label,
not a parseable identity. Requires the existing Worker.model server field;
an older provider's schema refusal is returned, with no fallback query.`,
		Example: "  hadron team worker terminal-title Jane --app hadronmemory.com:hadron-dev-team",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var w gen.WorkerFields
			var err error
			if directWorkerRef(args[0]) {
				client, cerr := f.GraphQLClient()
				if cerr != nil {
					return cerr
				}
				w, err = resolveWorker(cmd.Context(), client, "", args[0])
			} else {
				w, err = workerForArg(cmd, f, args[0])
			}
			if err != nil {
				return err
			}
			dto := terminalTitleFromWorker(w)
			return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error { _, err := fmt.Fprintln(out, dto.Title); return err })
		},
	}
}

func titleRaw(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
