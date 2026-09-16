package channel

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

// readStateDTO is the stable --json shape of an attendee's cursor.
type readStateDTO struct {
	ChannelID   string  `json:"channelId"`
	AttendeeURN *string `json:"attendeeUrn"`
	LastSeenSeq int     `json:"lastSeenSeq"`
	UpdatedAt   string  `json:"updatedAt"`
}

func newCmdMarkRead(f *cmdutil.Factory) *cobra.Command {
	var (
		attendee string
		seq      int
		appRef   string
	)
	cmd := &cobra.Command{
		Use:   "mark-read <id|address>",
		Short: "Advance an attendee's read cursor on a Channel",
		Long: `Advance where an attendee is up to on a Channel.

MONOTONIC: a seq lower than the stored one is a no-op that returns the cursor
unchanged, so this is safe to call with a stale watermark. The output reports
the cursor the server ended up with, not the seq you asked for — those differ
exactly when the call was a no-op, and presenting the request as the result
would report a rewind that did not happen.`,
		Example: `  hadron channel mark-read <address> --attendee Jonas --seq 631`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if attendee == "" {
				return exitcode.Newf(exitcode.Usage, "--attendee is required (a Worker or Agent name, URN or id)")
			}
			if !cmd.Flags().Changed("seq") {
				return exitcode.Newf(exitcode.Usage, "--seq is required (the last seq this attendee has seen)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var appPtr *string
			if appRef != "" {
				appPtr = &appRef
			}
			resp, err := gen.AdvanceChannelReadState(cmd.Context(), client, args[0], attendee, seq, appPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.AdvanceChannelReadState == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no read state")
			}
			r := resp.AdvanceChannelReadState
			dto := readStateDTO{
				ChannelID:   r.ChannelId,
				AttendeeURN: r.AttendeeUrn,
				LastSeenSeq: r.LastSeenSeq,
				UpdatedAt:   r.UpdatedAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.LastSeenSeq != seq {
					// Monotonic no-op: say so rather than implying the cursor
					// moved to where it was asked to go.
					_, err := fmt.Fprintf(w,
						"cursor unchanged at #%d (a lower seq is a no-op; #%d was already behind it)\n", dto.LastSeenSeq, seq)
					return err
				}
				_, err := fmt.Fprintf(w, "✓ cursor at #%d\n", dto.LastSeenSeq)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&attendee, "attendee", "", "the Worker or Agent whose cursor this is (name, URN or id)")
	cmd.Flags().IntVar(&seq, "seq", 0, "the last seq this attendee has seen")
	cmd.Flags().StringVar(&appRef, "owner-app", "", "the App context for the attendee (ID or URN)")
	return cmd
}

func newCmdReadState(f *cmdutil.Factory) *cobra.Command {
	var (
		attendee string
		appRef   string
	)
	cmd := &cobra.Command{
		Use:   "read-state <id|address>",
		Short: "Show where an attendee is up to on a Channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if attendee == "" {
				return exitcode.Newf(exitcode.Usage, "--attendee is required (a Worker or Agent name, URN or id)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var appPtr *string
			if appRef != "" {
				appPtr = &appRef
			}
			resp, err := gen.ChannelReadState(cmd.Context(), client, args[0], attendee, appPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.ChannelReadState == nil {
				// No row is a real answer — the attendee has read nothing here
				// — and is NOT an error. Reporting it as one would make "never
				// read" indistinguishable from "unreadable".
				return output.Write(f.IOStreams, f.JSON, readStateDTO{LastSeenSeq: 0}, func(w io.Writer) error {
					_, err := io.WriteString(w, "no read state — this attendee has seen nothing here\n")
					return err
				})
			}
			r := resp.ChannelReadState
			dto := readStateDTO{
				ChannelID:   r.ChannelId,
				AttendeeURN: r.AttendeeUrn,
				LastSeenSeq: r.LastSeenSeq,
				UpdatedAt:   r.UpdatedAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "cursor at #%d (updated %s)\n", dto.LastSeenSeq, dto.UpdatedAt)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&attendee, "attendee", "", "the Worker or Agent whose cursor this is (name, URN or id)")
	cmd.Flags().StringVar(&appRef, "owner-app", "", "the App context for the attendee (ID or URN)")
	return cmd
}
