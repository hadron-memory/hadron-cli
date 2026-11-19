package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type syncPullDTO struct {
	Memory           string         `json:"memory"`
	Operation        string         `json:"operation"`
	Clean            bool           `json:"clean"`
	SyncStatus       string         `json:"syncStatus"`
	SyncError        *string        `json:"syncError"`
	LastSyncedAt     *string        `json:"lastSyncedAt"`
	SyncReportStatus string         `json:"syncReportStatus"`
	SyncReport       *syncReportDTO `json:"syncReport"`
}

func newCmdSync(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "sync", Short: "Sync a memory from its configured repository"}
	var yes bool
	pull := &cobra.Command{
		Use: "pull <memoryRef>", Short: "Pull the repository's tree into a memory (destructive)",
		Long: `Pull mirrors the memory's configured source repository into the memory. The
repository is the source of truth: where a node differs, the repository's
version replaces the one in Hadron, and nodes the repository dropped are
deleted from the memory. The pull is applied as a whole or not at all.
There is no revision check: a pull overwrites edits made in Hadron since the
last sync. Encrypted memories cannot be synced.

A memory needs a source repository first. Set one with:

  hadron memory set <memory> --git-source https://github.com/<owner>/<repo>

(--git-source "" clears it.) The organization also needs the Hadron GitHub
App installed on that repository. A memory without a source, or whose
organization has no installation, is never synced: a memory the CLI reads
as having no source exits 2 with no sync request sent, and when the server
is the one refusing — a source it cannot see, or no installation — the
refusal is SYNC_NOT_ATTEMPTED (reason NO_SOURCE or NO_INSTALLATION, exit
1). hadron memory get <memory> --json shows the recorded source, the sync
status and the last sync time.

The pull asks for confirmation; --yes skips it and is required without a
terminal (otherwise exit 2).

Files the loader cannot take are skipped or held, and the result lists each
with its code: a protected loc, an undeclared role, an attestation refusal.
Fix what the code names — the file in the repository, or the memory's config
for a role — and pull again. A deferred entry or a held move is not a defect
to fix: the source changed while this pull was deciding, or a move could not
yet vacate its destination, and a later pull takes it by itself (a README
name collision recurs until the pair is renamed).

Outcomes: a clean pull prints "synced". Partial, unknown and unavailable
reports exit 0 with a warning and clean:false in --json. A non-OK status
exits 1 as not completed, and so does a failed report; a stored report is
history, not this pull's outcome, and a missing report without a sync
timestamp is never clean. Exit 7 means the sync may have happened: the failed
request is not retried, so use memory get to check the recorded state before
pulling again. --json:
{memory,operation:"pull",clean,syncStatus,syncError,lastSyncedAt,
syncReportStatus,syncReport}. URL credentials in sync errors are masked.`,
		Example: "  hadron memory sync pull acme.com:kb --yes\n  hadron memory sync pull hrn:mem:acme.com:kb --yes --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMemoryRef(args[0]); err != nil {
				return err
			}
			ref := cmdutil.CanonicalMemoryRef(args[0])
			// Canonicalize locally: confirmation MUST precede even a lookup request.
			if err := cmdutil.Confirm(f.IOStreams, yes, "Pull repository into memory "+quoteCapped(ref, 300)+"? The repository wins and overwrites Hadron edits."); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			preflight, err := gen.MemorySyncPrerequisites(cmd.Context(), client, ref)
			if err != nil {
				return syncPullError(err, ref, false)
			}
			// A null read cannot establish absence or authority: delegate to sync.
			// Presence only: never render, persist or echo the stored URL.
			// Encrypted rows always reach the server's writer/encryption gates,
			// even without source. Do not reproduce that refusal client-side.
			if preflight != nil && preflight.Memory != nil && !preflight.Memory.IsEncrypted && (preflight.Memory.Source == nil || *preflight.Memory.Source == "") {
				return exitcode.Newf(exitcode.Usage, "this memory has no configured repository; no sync mutation was sent")
			}
			resp, err := gen.SyncMemory(cmd.Context(), client, ref)
			if err != nil {
				return syncPullError(err, ref, true)
			}
			if resp == nil || resp.SyncMemory == nil || resp.SyncMemory.SyncStatus == "" {
				return exitcode.Newf(exitcode.Unavailable, "no usable answer; the sync may have happened; check `hadron memory get %s`", ref)
			}
			m := resp.SyncMemory
			// A successful GraphQL response can contain an untouched historical
			// row. Do not interpret its report as this invocation's outcome.
			if m.SyncStatus != "OK" {
				dto := syncPullDTO{Memory: ref, Operation: "pull", Clean: false, SyncStatus: string(m.SyncStatus), SyncError: maskSyncError(m.SyncError), LastSyncedAt: m.LastSyncedAt, SyncReportStatus: syncReportUnavailable}
				summary := "pull not completed (status " + output.SafeLine(string(m.SyncStatus), 100) + "); check memory get for the recorded state"
				fmt.Fprintln(f.IOStreams.ErrOut, "Warning: "+summary)
				if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					if _, err := fmt.Fprintln(w, summary); err != nil {
						return err
					}
					if dto.SyncError != nil {
						_, err := fmt.Fprintln(w, "stored sync error: "+quoteCapped(*dto.SyncError, 500))
						return err
					}
					return nil
				}); err != nil {
					return err
				}
				return exitcode.Silent(exitcode.Error)
			}
			report, status := parseSyncReport(m.LastSyncReport)
			failed := syncReportSaysFailed(m.LastSyncReport)
			dto := syncPullDTO{Memory: ref, Operation: "pull", SyncStatus: string(m.SyncStatus), SyncError: maskSyncError(m.SyncError), LastSyncedAt: m.LastSyncedAt, SyncReportStatus: status, SyncReport: report}
			dto.Clean = status == syncReportNone && m.LastSyncedAt != nil && *m.LastSyncedAt != "" && m.SyncError == nil
			summary := "synced; report unavailable"
			switch {
			case failed:
				summary = "sync failed"
			case dto.Clean:
				summary = "synced"
			case report != nil && report.Outcome == "partial":
				summary = fmt.Sprintf("synced with %d file(s) skipped or held", len(report.Entries))
			case report != nil && report.Outcome == "unknown":
				summary = "synced, but the platform could not certify that every file applied"
			}
			if !dto.Clean {
				fmt.Fprintln(f.IOStreams.ErrOut, "Warning: "+summary)
			}
			err = output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintln(w, summary)
				writeSyncBlock(w, memoryDetailDTO{SyncStatus: dto.SyncStatus, LastSyncedAt: dto.LastSyncedAt, SyncError: dto.SyncError, SyncReportStatus: status, SyncReport: report})
				return nil
			})
			if err != nil {
				return err
			}
			if failed {
				return exitcode.Silent(exitcode.Error)
			}
			return nil
		},
	}
	pull.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required without a terminal)")
	group.AddCommand(pull)
	return group
}

type syncPullFailure struct {
	message string
	cause   error
}

func (e *syncPullFailure) Error() string { return e.message }
func (e *syncPullFailure) Unwrap() error { return e.cause }

func syncPullError(err error, ref string, attempted bool) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	message := mapped.Error()
	if api.HasErrorCode(err, "ENCRYPTED_GIT_SYNC_UNAVAILABLE") {
		code = exitcode.Usage
		message = "this memory is encrypted and cannot be synced with git"
	} else if !attempted {
		if code == exitcode.Unavailable {
			message = "could not check the memory sync configuration"
		}
		message += "; no sync mutation was sent"
	} else if code == exitcode.Unavailable {
		message = "no answer; the sync may have happened; check `hadron memory get " + ref + "` before retrying"
	} else if api.ServerCodeOf(err) == "" || api.HasErrorCode(err, "INTERNAL_SERVER_ERROR") {
		message += "; check `hadron memory get " + ref + "` for what was recorded"
	}
	message = *maskSyncError(&message)
	safe := output.SafeLine(message, 2000)
	if fault, ok := api.InternalFaultOf(mapped); ok {
		// Keep only the bounded request-id metadata, without a raw server cause.
		return &syncPullFailure{message: safe, cause: api.NewInternalFaultError(fault.RequestID, nil)}
	}
	return api.WithServerCode(exitcode.Newf(code, "%s", safe), err)
}
