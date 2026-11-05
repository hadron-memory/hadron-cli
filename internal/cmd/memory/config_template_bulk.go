package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

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

func runBulkTemplateApply(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, template string, typed []string, file string, hasFile bool, pinned *int, dryRun, yes bool) error {
	typed = append([]string{}, typed...)
	if hasFile {
		in := f.IOStreams.In
		if file != "-" {
			h, err := os.Open(file)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "read --memories-file: %v", err)
			}
			defer func() { _ = h.Close() }()
			in = h
		}
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				typed = append(typed, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return exitcode.Newf(exitcode.Usage, "read --memories-file: %v", err)
		}
	}
	if len(typed) == 0 {
		return exitcode.Newf(exitcode.Usage, "at least one memory ref is required (arguments or --memories-file)")
	}
	refs := make([]string, len(typed))
	for i, r := range typed {
		refs[i] = cmdutil.CanonicalMemoryRef(r)
	}
	preview, err := applyBulkTemplate(cmd, client, template, refs, pinned, true)
	if err != nil {
		return err
	}
	if dryRun {
		return writeBulkApplyResult(f, preview, typed)
	}
	var rev *int
	replacements := []string{}
	for _, entry := range preview.Results {
		if !entry.OK {
			continue
		}
		r := entry.Result
		if rev != nil && *rev != r.TemplateRevision {
			return exitcode.Newf(exitcode.Conflict, "template revision changed during preview; nothing applied")
		}
		n := r.TemplateRevision
		rev = &n
		if roles := rolesWith(*r, "REPLACED"); len(roles) > 0 {
			replacements = append(replacements, fmt.Sprintf("%s: %s", typed[entry.Index], output.SafeCell(strings.Join(roles, ", "))))
		}
	}
	if rev == nil {
		return writeBulkApplyResult(f, preview, typed)
	}
	if len(replacements) > 0 {
		if err := cmdutil.Confirm(f.IOStreams, yes, "Apply required template? It REPLACES and locks rules in "+strings.Join(replacements, "; ")+"."); err != nil {
			return err
		}
	}
	result, err := applyBulkTemplate(cmd, client, template, refs, rev, false)
	if err != nil {
		return err
	}
	return writeBulkApplyResult(f, result, typed)
}
func applyBulkTemplate(cmd *cobra.Command, client graphql.Client, template string, refs []string, expected *int, dry bool) (bulkApplyDTO, error) {
	resp, err := gen.ApplyMemoryConfigTemplateToMemories(cmd.Context(), client, template, refs, expected, dry)
	if err != nil {
		return bulkApplyDTO{}, api.MapError(err)
	}
	r := resp.ApplyMemoryConfigTemplateToMemories
	if r == nil {
		return bulkApplyDTO{}, exitcode.Newf(exitcode.Error, "the server returned no bulk apply result")
	}
	dto := bulkApplyDTO{DryRun: r.DryRun, Succeeded: r.Succeeded, Failed: r.Failed, Results: []bulkApplyEntryDTO{}}
	if r.DryRun != dry || len(r.Results) != len(refs) {
		return dto, exitcode.Newf(exitcode.Error, "the server returned an incomplete bulk apply report; verify memory configs before retrying")
	}
	succeeded := 0
	for i, e := range r.Results {
		if e == nil || e.Index != i || e.MemoryRef != refs[i] || (e.Ok && (e.Result == nil || e.Error != nil)) || (!e.Ok && (e.Error == nil || e.Result != nil)) {
			return dto, exitcode.Newf(exitcode.Error, "the server returned an invalid bulk apply entry; verify memory configs before retrying")
		}
		entry := bulkApplyEntryDTO{Index: e.Index, MemoryRef: e.MemoryRef, OK: e.Ok}
		if e.Ok {
			v := applyDTO(&e.Result.MemoryConfigApplyFields)
			entry.Result = &v
			succeeded++
		} else {
			entry.Error = &bulkApplyErrorDTO{Code: e.Error.Code, Message: e.Error.Message, CurrentRevision: e.Error.CurrentRevision, RequestID: e.Error.RequestId}
		}
		dto.Results = append(dto.Results, entry)
	}
	if dto.Succeeded != succeeded || dto.Failed != len(refs)-succeeded {
		return dto, exitcode.Newf(exitcode.Error, "the server returned inconsistent bulk apply counts; verify memory configs before retrying")
	}
	return dto, nil
}
func writeBulkApplyResult(f *cmdutil.Factory, r bulkApplyDTO, typed []string) error {
	err := output.Write(f.IOStreams, f.JSON, r, func(w io.Writer) error {
		for _, e := range r.Results {
			if _, err := fmt.Fprintf(w, "[%d] %s\n", e.Index, typed[e.Index]); err != nil {
				return err
			}
			if e.OK {
				v := e.Result
				if _, err := fmt.Fprintf(w, "Template revision %d\n", v.TemplateRevision); err != nil {
					return err
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
					if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s on role %s%s\n", output.SafeCell(warning.Code), output.SafeCell(warning.Role), field); err != nil {
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
