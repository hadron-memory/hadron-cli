package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type bulkApplyErrorDTO struct {
	Code            string  `json:"code"`
	Message         string  `json:"message"`
	CurrentRevision *int    `json:"currentRevision,omitempty"`
	RequestID       *string `json:"requestId,omitempty"`
}
type bulkApplyEntryDTO struct {
	Index     int                `json:"index"`
	MemoryRef string             `json:"memoryRef"`
	OK        bool               `json:"ok"`
	Result    *applyResultDTO    `json:"result,omitempty"`
	Error     *bulkApplyErrorDTO `json:"error,omitempty"`
}
type bulkApplyDTO struct {
	DryRun    bool                `json:"dryRun"`
	Succeeded int                 `json:"succeeded"`
	Failed    int                 `json:"failed"`
	Results   []bulkApplyEntryDTO `json:"results"`
}

type bulkApplyOptions struct {
	templateRef string
	inputRefs   []string
	expected    *int
	dryRun      bool
	yes         bool
	stdinUsed   bool
}

func readBulkMemoryRefs(f *cmdutil.Factory, args []string, file string, hasFile bool) ([]string, error) {
	inputRefs := append([]string{}, args...)
	if hasFile {
		in := f.IOStreams.In
		if file != "-" {
			handle, err := os.Open(file)
			if err != nil {
				return nil, exitcode.Newf(exitcode.Usage, "read --memories-file: %v", err)
			}
			defer func() { _ = handle.Close() }()
			in = handle
		}
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		firstLine := true
		for scanner.Scan() {
			line := scanner.Text()
			if firstLine {
				line = strings.TrimPrefix(line, "\ufeff")
				firstLine = false
			}
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				inputRefs = append(inputRefs, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "read --memories-file: %v", err)
		}
	}
	if len(inputRefs) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "at least one memory ref is required (arguments or --memories-file)")
	}
	return inputRefs, nil
}

func runBulkTemplateApply(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, opts bulkApplyOptions) error {
	inputRefs, template, pinned, dryRun, yes := opts.inputRefs, opts.templateRef, opts.expected, opts.dryRun, opts.yes
	refs := make([]string, len(inputRefs))
	for i, r := range inputRefs {
		refs[i] = cmdutil.CanonicalMemoryRef(r)
	}
	preview, err := applyBulkTemplate(cmd, client, template, refs, pinned, true)
	if err != nil {
		return err
	}
	if dryRun {
		return writeBulkApplyResult(f, preview, inputRefs)
	}
	var rev *int
	replacements := []string{}
	failedRefs := []string{}
	for _, entry := range preview.Results {
		if !entry.OK {
			failedRefs = append(failedRefs, fmt.Sprintf("[%d] %s", entry.Index, safeBulkLine(inputRefs[entry.Index])))
			continue
		}
		r := entry.Result
		if rev != nil && *rev != r.TemplateRevision {
			return exitcode.Newf(exitcode.Conflict, "template revision changed during preview; nothing applied")
		}
		n := r.TemplateRevision
		rev = &n
		if roles := rolesWith(*r, "REPLACED"); len(roles) > 0 {
			replacements = append(replacements, fmt.Sprintf("%s: %s", safeBulkLine(inputRefs[entry.Index]), safeBulkLine(strings.Join(roles, ", "))))
		}
	}
	if rev == nil {
		return writeBulkApplyResult(f, preview, inputRefs)
	}
	if len(replacements) > 0 || len(failedRefs) > 0 {
		if opts.stdinUsed && !yes {
			return exitcode.Newf(exitcode.Usage, "stdin was consumed by --memories-file -; use --yes to explicitly confirm this apply")
		}
		parts := []string{}
		if len(replacements) > 0 {
			parts = append(parts, "It REPLACES and locks rules in "+strings.Join(replacements, "; ")+".")
		}
		if len(failedRefs) > 0 {
			parts = append(parts, "Preview failed for "+strings.Join(failedRefs, "; ")+". These targets may now succeed and replace rules that were not previewed.")
		}
		if err := cmdutil.Confirm(f.IOStreams, yes, "Apply template to the whole list? "+strings.Join(parts, " ")); err != nil {
			return err
		}
	}

	result, err := applyBulkTemplate(cmd, client, template, refs, rev, false)
	if err != nil {
		return err
	}
	return writeBulkApplyResult(f, result, inputRefs)
}
func applyBulkTemplate(cmd *cobra.Command, client graphql.Client, template string, refs []string, expected *int, dry bool) (bulkApplyDTO, error) {
	resp, err := gen.ApplyMemoryConfigTemplateToMemories(cmd.Context(), client, template, refs, expected, dry)
	if err != nil {
		mapped := api.MapError(err)
		if !dry && !api.BulkTemplateApplyRefusedBeforeWrite(err) {
			mapped = fmt.Errorf("%w; some memories may already have changed; verify memory configs before retrying", mapped)
		}
		return bulkApplyDTO{}, mapped
	}
	r := resp.ApplyMemoryConfigTemplateToMemories
	if r == nil {
		return bulkApplyDTO{}, bulkReportError(dry, "the server returned no bulk apply result")
	}
	dto := bulkApplyDTO{DryRun: r.DryRun, Succeeded: r.Succeeded, Failed: r.Failed, Results: []bulkApplyEntryDTO{}}
	if r.DryRun != dry || len(r.Results) != len(refs) {
		return bulkApplyDTO{}, bulkReportError(dry, "the server returned an incomplete bulk apply report")
	}
	succeeded := 0
	for i, e := range r.Results {
		if e == nil || e.Index != i || e.MemoryRef != refs[i] || (e.Ok && (e.Result == nil || e.Error != nil)) || (!e.Ok && (e.Error == nil || e.Result != nil)) {
			return bulkApplyDTO{}, bulkReportError(dry, "the server returned an invalid bulk apply entry")
		}
		entry := bulkApplyEntryDTO{Index: e.Index, MemoryRef: e.MemoryRef, OK: e.Ok}
		if e.Ok {
			if e.Result.DryRun != dry {
				return bulkApplyDTO{}, bulkReportError(dry, "the server returned an apply entry with the wrong dry-run mode")
			}
			v := applyDTO(&e.Result.MemoryConfigApplyFields)
			entry.Result = &v
			succeeded++
		} else {
			entry.Error = &bulkApplyErrorDTO{Code: e.Error.Code, Message: e.Error.Message, CurrentRevision: e.Error.CurrentRevision, RequestID: e.Error.RequestId}
		}
		dto.Results = append(dto.Results, entry)
	}
	if dto.Succeeded != succeeded || dto.Failed != len(refs)-succeeded {
		return bulkApplyDTO{}, bulkReportError(dry, "the server returned inconsistent bulk apply counts")
	}
	return dto, nil
}
func writeBulkApplyResult(f *cmdutil.Factory, r bulkApplyDTO, inputRefs []string) error {
	err := output.Write(f.IOStreams, f.JSON, r, func(w io.Writer) error {
		for _, e := range r.Results {
			if _, err := fmt.Fprintf(w, "[%d] %s\n", e.Index, safeBulkLine(inputRefs[e.Index])); err != nil {
				return err
			}
			if e.OK {
				v := e.Result
				if _, err := fmt.Fprintf(w, "Template revision %d\n", v.TemplateRevision); err != nil {
					return err
				}
				if len(v.Entries) == 0 {
					if _, err := fmt.Fprintln(w, "The template has no rules; nothing to apply."); err != nil {
						return err
					}
				}
				t := output.NewTable(w, "ROLE", "OUTCOME", "LOCKED")
				for _, rule := range v.Entries {
					t.Row(output.SafeCell(rule.Role), output.SafeCell(rule.Outcome), lockedAfter(*v, rule))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				for _, warning := range v.Warnings {
					field := ""
					if warning.Field != nil {
						field = " (" + output.SafeCell(*warning.Field) + ")"
					}
					if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: [%d] %s: %s on role %s%s\n", e.Index, safeBulkLine(inputRefs[e.Index]), output.SafeCell(warning.Code), output.SafeCell(warning.Role), field); err != nil {
						return err
					}
				}
			} else {
				x := e.Error
				if _, err := fmt.Fprintf(w, "%s: %s", output.SafeCell(x.Code), output.SafeCell(x.Message)); err != nil {
					return err
				}
				if x.CurrentRevision != nil {
					if _, err := fmt.Fprintf(w, " (currentRevision %d)", *x.CurrentRevision); err != nil {
						return err
					}
				}
				if x.RequestID != nil {
					if _, err := fmt.Fprintf(w, " (requestId %s)", output.SafeCell(*x.RequestID)); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
		}
		suffix := ""
		if r.DryRun {
			suffix = " (dry run)"
		}
		_, err := fmt.Fprintf(w, "%d succeeded, %d failed%s\n", r.Succeeded, r.Failed, suffix)
		return err
	})
	if err != nil {
		return err
	}
	if r.Failed > 0 {
		return exitcode.Silent(exitcode.Error)
	}
	return nil
}

// Caller refs must remain complete in text (unlike capped server cells), but
// file/stdin input can carry terminal controls too.
func safeBulkLine(ref string) string { return output.SafeLine(ref, utf8.RuneCountInString(ref)+1) }

func bulkReportError(dry bool, what string) error {
	if dry {
		return exitcode.Newf(exitcode.Error, "%s; verify memory configs before retrying (no apply call was sent)", what)
	}
	return exitcode.Newf(exitcode.Error, "%s; some memories may already have changed; verify memory configs before retrying", what)
}
