package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type heldWorkerDTO struct {
	ID             string  `json:"id"`
	URN            *string `json:"urn"`
	Name           string  `json:"name"`
	AppID          string  `json:"appId"`
	AppURN         *string `json:"appUrn"`
	AppName        *string `json:"appName"`
	Role           *string `json:"role"`
	HeldAt         *string `json:"heldAt"`
	RetiredAt      *string `json:"retiredAt"`
	HasLiveSession *bool   `json:"hasLiveSession"`
}
type heldWorkerPageDTO struct {
	Items []heldWorkerDTO `json:"items"`
	Total int             `json:"total"`
}

func newCmdWorkerHeld(f *cmdutil.Factory) *cobra.Command {
	var user string
	var retired bool
	var limit, offset int
	c := &cobra.Command{Use: "held --user <ref>", Short: "Read workers currently held by a user across readable Apps", Long: `Read one page, oldest casting first, without changing any hold or session.
--user accepts a user id, handle, URN or email. A missing user, no holds and
holds confined to unreadable Apps return the same empty page: no reason is
inferred. Retired castings are hidden unless --include-retired. Pagination
and the total apply after the server's App-read filter. --app does not narrow
this cross-App query. --json returns {items:[],total:N}, retaining nullable
server values, including hasLiveSession (not availability).`, Example: "  hadron team worker held --user alice --limit 20 --json", Args: cobra.NoArgs}
	c.Flags().StringVar(&user, "user", "", "user id, handle, URN or email (required)")
	c.Flags().BoolVar(&retired, "include-retired", false, "include retired castings")
	c.Flags().IntVar(&limit, "limit", 20, "page size, 0 counts only")
	c.Flags().IntVar(&offset, "offset", 0, "page offset")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(user) == "" {
			return exitcode.Newf(exitcode.Usage, "--user is required")
		}
		if limit < 0 || offset < 0 || int64(limit) > 2147483647 || int64(offset) > 2147483647 {
			return exitcode.Newf(exitcode.Usage, "limit and offset take GraphQL whole numbers from 0 to 2147483647")
		}
		client, e := f.GraphQLClient()
		if e != nil {
			return e
		}
		var includeRetired *bool
		if cmd.Flags().Changed("include-retired") {
			includeRetired = &retired
		}
		r, e := gen.TeamHeldWorkers(cmd.Context(), client, user, includeRetired, &limit, &offset)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.HeldWorkers == nil {
			return exitcode.Newf(exitcode.Error, "server returned no held-workers page")
		}
		d := heldWorkerPageDTO{Items: []heldWorkerDTO{}, Total: r.HeldWorkers.Total}
		for _, v := range r.HeldWorkers.Items {
			if v != nil {
				d.Items = append(d.Items, heldWorkerDTO{v.Id, v.Urn, v.Name, v.AppId, v.AppUrn, v.AppName, v.Role, v.HeldAt, v.RetiredAt, v.HasLiveSession})
			}
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if len(d.Items) == 0 {
				_, e := fmt.Fprintf(w, "No held workers returned on this page.\ntotal: %d; offset: %d; limit: %d\n", d.Total, offset, limit)
				return e
			}
			t := output.NewTable(w, "WORKER", "APP", "ROLE", "HELD AT")
			for _, v := range d.Items {
				t.Row(output.SafeCell(v.Name), output.SafeCell(heldAppText(v)), output.SafeCell(strOr(v.Role)), output.SafeCell(strOr(v.HeldAt)))
			}
			if e := t.Flush(); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "%d shown of %d; offset: %d\n", len(d.Items), d.Total, offset)
			return e
		})
	}
	return c
}

type registerPickEntryDTO struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}
type registerPickDTO struct {
	AppID   string                 `json:"appId"`
	Code    string                 `json:"code"`
	Entries []registerPickEntryDTO `json:"entries"`
}
type registerCandidateDTO struct {
	Depth   int          `json:"depth"`
	Role    *string      `json:"role"`
	TaskURN string       `json:"taskUrn"`
	Worker  taskPartyDTO `json:"worker"`
}
type registerCandidatesDTO struct {
	AppID      string                 `json:"appId"`
	Code       string                 `json:"code"`
	Depth      *int                   `json:"depth"`
	TaskLoc    *string                `json:"taskLoc"`
	Total      int                    `json:"total"`
	Candidates []registerCandidateDTO `json:"candidates"`
}

func newCmdRegisterPicker(f *cmdutil.Factory, pick bool) *cobra.Command {
	verb := "candidates"
	if pick {
		verb = "pick"
	}
	var worker string
	var limit, offset int
	c := &cobra.Command{Use: verb + " <code>", Short: "Experimental: read the server's register role " + verb, Long: `Experimental, read-only register role picker. Prints the server's answer
in its returned order, without re-ranking, filtering, assigning, routing or
delivering anything. pick may return a named worker, one role holder, a
coordinator or managers according to the server. candidates reads one page
of nearest-task-depth ties, with server total and nullable depth/taskLoc.
--app selects the App; configured App or binding otherwise. --worker on pick
is an optional worker id or URN passed unchanged. --json preserves the
selected projection's fields and nullable values; empty arrays stay [].`, Example: "  hadron team register " + verb + " task.finish --app acme.com:eng --json", Args: cobra.ExactArgs(1)}
	if pick {
		c.Flags().StringVar(&worker, "worker", "", "optional worker id or URN")
	} else {
		c.Flags().IntVar(&limit, "limit", 20, "page size (0 counts only); server caps the page")
		c.Flags().IntVar(&offset, "offset", 0, "page offset")
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "code must not be empty")
		}
		if limit < 0 || offset < 0 || int64(limit) > 2147483647 || int64(offset) > 2147483647 {
			return exitcode.Newf(exitcode.Usage, "limit and offset take GraphQL whole numbers from 0 to 2147483647")
		}
		if pick && cmd.Flags().Changed("worker") && strings.TrimSpace(worker) == "" {
			return exitcode.Newf(exitcode.Usage, "--worker must not be empty")
		}
		s, client, e := controlsClient(cmd, f)
		if e != nil {
			return e
		}
		label := lazyAppLabel(cmd.Context(), f, s)
		if pick {
			r, e := gen.TeamRegisterPick(cmd.Context(), client, s.Ref, args[0], optStr(worker))
			if e != nil {
				return api.MapError(e)
			}
			if r == nil || r.RegisterRolePick == nil {
				return exitcode.Newf(exitcode.Error, "server returned no register pick")
			}
			v := r.RegisterRolePick
			d := registerPickDTO{AppID: v.AppId, Code: v.Code, Entries: []registerPickEntryDTO{}}
			for _, entry := range v.Entries {
				if entry != nil {
					d.Entries = append(d.Entries, registerPickEntryDTO{entry.Id, string(entry.Kind)})
				}
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, e := fmt.Fprintf(w, "app: %s\n", label()); e != nil {
					return e
				}
				if len(d.Entries) == 0 {
					_, e := fmt.Fprintln(w, "No register pick returned.")
					return e
				}
				t := output.NewTable(w, "KIND", "ID")
				for _, x := range d.Entries {
					t.Row(output.SafeCell(x.Kind), output.SafeCell(x.ID))
				}
				return t.Flush()
			})
		}
		r, e := gen.TeamRegisterCandidates(cmd.Context(), client, s.Ref, args[0], &limit, &offset)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.RegisterRoleCandidates == nil {
			return exitcode.Newf(exitcode.Error, "server returned no register candidates")
		}
		v := r.RegisterRoleCandidates
		d := registerCandidatesDTO{AppID: v.AppId, Code: v.Code, Depth: v.Depth, TaskLoc: v.TaskLoc, Total: v.Total, Candidates: []registerCandidateDTO{}}
		for _, x := range v.Candidates {
			if x != nil {
				if x.Worker == nil {
					return exitcode.Newf(exitcode.Error, "server returned a candidate without a worker")
				}
				d.Candidates = append(d.Candidates, registerCandidateDTO{Depth: x.Depth, Role: x.Role, TaskURN: x.TaskUrn, Worker: taskPartyDTO{ID: x.Worker.Id, Kind: string(x.Worker.Kind), Name: x.Worker.Name, Urn: x.Worker.Urn}})
			}
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if _, e := fmt.Fprintf(w, "app: %s\n", label()); e != nil {
				return e
			}
			t := output.NewTable(w, "WORKER", "ROLE", "DEPTH", "TASK")
			for _, x := range d.Candidates {
				t.Row(output.SafeCell(x.Worker.ID), output.SafeCell(strOr(x.Role)), fmt.Sprint(x.Depth), output.SafeCell(x.TaskURN))
			}
			if e := t.Flush(); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "%d shown of %d; offset: %d\n", len(d.Candidates), d.Total, offset)
			return e
		})
	}
	return c
}

type attendeeRegisterChannelDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Loc  string `json:"loc"`
}
type attendeeRegisterDTO struct {
	AttendeeURN *string                    `json:"attendeeUrn"`
	NamedBy     string                     `json:"namedBy"`
	CanRead     bool                       `json:"canRead"`
	CanPost     *bool                      `json:"canPost"`
	LastSeenSeq *int                       `json:"lastSeenSeq"`
	Channel     attendeeRegisterChannelDTO `json:"channel"`
}

func newCmdRegisterAttendee(f *cmdutil.Factory) *cobra.Command {
	var agent, worker string
	c := &cobra.Command{Use: "attendee (--agent <id-or-URN> | --worker <id-or-URN>)", Short: "Read an Agent's or worker's resolved Channel register", Long: `Read the server's resolved attendee register for one Agent or worker.
Pass exactly one of --agent or --worker. This does not change the action
register index printed by plain team register. No routing or delivery is
performed. Server visibility and capability values are preserved, including
nullable canPost/attendeeUrn/lastSeenSeq. --json is an array of resolved rows
with channel id/name/loc, namedBy, canRead, canPost, attendeeUrn and lastSeenSeq.
--app selects the App; otherwise configured App or binding.`, Example: "  hadron team register attendee --agent hrn:agent:acme.com:coder --app acme.com:eng --json", Args: cobra.NoArgs}
	c.Flags().StringVar(&agent, "agent", "", "Agent id or URN")
	c.Flags().StringVar(&worker, "worker", "", "worker id or URN")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if (cmd.Flags().Changed("agent") && strings.TrimSpace(agent) == "") || (cmd.Flags().Changed("worker") && strings.TrimSpace(worker) == "") {
			return exitcode.Newf(exitcode.Usage, "attendee reference must not be empty")
		}
		if (strings.TrimSpace(agent) == "") == (strings.TrimSpace(worker) == "") {
			return exitcode.Newf(exitcode.Usage, "pass exactly one of --agent or --worker")
		}
		s, client, e := controlsClient(cmd, f)
		if e != nil {
			return e
		}
		r, e := gen.TeamAttendeeRegister(cmd.Context(), client, s.Ref, optStr(agent), optStr(worker))
		if e != nil {
			return api.MapError(e)
		}
		if r == nil {
			return exitcode.Newf(exitcode.Error, "server returned no attendee register response")
		}
		d := []attendeeRegisterDTO{}
		for _, v := range r.AttendeeRegister {
			if v != nil {
				if v.Channel == nil {
					return exitcode.Newf(exitcode.Error, "server returned an attendee row without a channel")
				}
				d = append(d, attendeeRegisterDTO{v.AttendeeUrn, string(v.NamedBy), v.CanRead, v.CanPost, v.LastSeenSeq, attendeeRegisterChannelDTO{v.Channel.Id, v.Channel.Name, v.Channel.Loc}})
			}
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if _, e := fmt.Fprintf(w, "app: %s\n", lazyAppLabel(cmd.Context(), f, s)()); e != nil {
				return e
			}
			t := output.NewTable(w, "CHANNEL", "NAMED BY", "CAN READ", "CAN POST")
			for _, v := range d {
				post := "withheld"
				if v.CanPost != nil {
					post = fmt.Sprint(*v.CanPost)
				}
				t.Row(output.SafeCell(v.Channel.Name), output.SafeCell(v.NamedBy), fmt.Sprint(v.CanRead), post)
			}
			return t.Flush()
		})
	}
	return c
}

// heldAppText names the App by its display name where the server returned one,
// then its URN, and only then the opaque id.
func heldAppText(v heldWorkerDTO) string {
	if v.AppName != nil && *v.AppName != "" {
		return *v.AppName
	}
	if v.AppURN != nil && *v.AppURN != "" {
		return *v.AppURN
	}
	return v.AppID
}
