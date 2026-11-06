package orca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

const rosterLimit1635 = 1 << 20

type hostRoster struct {
	Version int                `json:"version"`
	App     string             `json:"app"`
	Workers []hostRosterWorker `json:"workers"`
}
type hostRosterWorker struct {
	Worker string `json:"worker"`
	Repo   string `json:"repo"`
	Family string `json:"family"`
}
type hostPlanWorker struct {
	Worker    string   `json:"worker"`
	ID        string   `json:"id"`
	Repo      string   `json:"repo"`
	Family    string   `json:"family"`
	Worktrees []string `json:"worktrees"`
	Terminals []string `json:"terminals"`
	Sessions  []string `json:"sessions"`
}
type hostPlanAction struct {
	Worker string `json:"worker"`
	Kind   string `json:"kind"`
	Repo   string `json:"repo"`
}
type hostPlanProblem struct {
	Worker string `json:"worker"`
	Reason string `json:"reason"`
}
type hostPlanDTO struct {
	Version     int               `json:"version"`
	App         string            `json:"app"`
	ReadOnly    bool              `json:"readOnly"`
	Complete    bool              `json:"complete"`
	Workers     []hostPlanWorker  `json:"workers"`
	Actions     []hostPlanAction  `json:"actions"`
	Conflicts   []hostPlanProblem `json:"conflicts"`
	Unavailable []hostPlanProblem `json:"unavailable"`
}
type hostWorkerIdentity struct {
	ID, AppID, Family string
	Retired           bool
}
type hostPlanEnv struct {
	App       func(context.Context, string) (string, error)
	Worker    func(context.Context, string, string) (hostWorkerIdentity, error)
	Inventory func(context.Context, []hostRosterWorker) (hostInventory, error)
}

func newCmdHost(f *cmdutil.Factory) *cobra.Command {
	host := &cobra.Command{Use: "host", Short: "Inspect a read-only host plan from an explicit roster",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return exitcode.Newf(exitcode.Usage, "unknown host subcommand; only read-only plan is available")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	host.AddCommand(newCmdHostPlan(f, defaultHostPlanEnv(f)))
	return host
}
func defaultHostPlanEnv(f *cmdutil.Factory) hostPlanEnv {
	return hostPlanEnv{
		App: func(ctx context.Context, ref string) (string, error) {
			client, err := f.GraphQLClient()
			if err != nil {
				return "", err
			}
			r, err := gen.GetAppMetadata(ctx, client, ref)
			if err != nil {
				return "", api.MapErrorKeepingCode(err)
			}
			if r == nil || r.App == nil || r.App.Id == "" {
				return "", exitcode.Newf(exitcode.NotFound, "App identity unavailable")
			}
			return r.App.Id, nil
		},
		Worker: func(ctx context.Context, app, ref string) (hostWorkerIdentity, error) {
			client, err := f.GraphQLClient()
			if err != nil {
				return hostWorkerIdentity{}, err
			}
			r, err := gen.GetWorkerInApp(ctx, client, app, ref)
			if err != nil {
				return hostWorkerIdentity{}, api.MapErrorKeepingCode(err)
			}
			if r == nil || r.Worker == nil || r.Worker.Id == "" || r.Worker.AppId == "" {
				return hostWorkerIdentity{}, exitcode.Newf(exitcode.NotFound, "worker identity unavailable")
			}
			return hostWorkerIdentity{ID: r.Worker.Id, AppID: r.Worker.AppId, Family: deref(r.Worker.Family), Retired: r.Worker.RetiredAt != nil}, nil
		},
		Inventory: readHostInventory,
	}
}
func newCmdHostPlan(f *cmdutil.Factory, env hostPlanEnv) *cobra.Command {
	var file string
	cmd := &cobra.Command{Use: "plan --roster-file <path>", Short: "Describe proposed worktree, terminal and session actions; never execute them", Args: cobra.NoArgs,
		Long: `Read a version-1 JSON roster and inspect existing worker identities and this
host's git/Orca inventory. Actions in the result are DATA ONLY: there is no apply
verb, launch, terminal input, session binding or host-state write.

Roster: {"version":1,"app":"hrn:app:acme.com:team","workers":[{"worker":"Iris","repo":"/absolute/existing/checkout","family":"codex"}]}
Use a regular UTF-8 file, at most 1 MiB, with 1..200 rows. Unknown/duplicate JSON
keys and duplicate resolved workers are errors. Families are claude/codex/glm.
Repo paths must be absolute existing checkout directories, without .. segments;
symlinks are resolved. No shell expansion occurs. The file's App is explicit;
--app is refused here, and ambient App defaults do not select the target.

The plan reads App/worker identity, git worktree and session-binding metadata,
and Orca terminal list only. It does not read terminal screens, ticks or wakes.
Names/path suffixes alone never associate a resource with a worker: a worktree
binding must name that worker id and App id. Recorded bindings and terminal
identity are observations, not proof of a live model or a safe input prompt.
Missing roster rows never delete, retire, close or release anything.

--json returns one version-1 object: app, readOnly, complete, workers, actions,
conflicts, unavailable. Arrays are always []. Input order is stable. A conflict
exits 5, incomplete discovery exits nonzero, and missing/broken Orca exits 10.
A failed discovery is never an empty inventory. Diagnostics omit subprocess
output and server prose. No credentials or transcripts are emitted.

This is a Linux-tested read-only snapshot, not a reviewed execution plan or a
standing grant. Future host-write tooling requires its own authorization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" || f.AppFlag != "" {
				return exitcode.Newf(exitcode.Usage, "name --roster-file; its App is authoritative, so omit --app")
			}
			roster, err := readHostRoster(file)
			if err != nil {
				return err
			}
			dto := hostPlanDTO{Version: 1, App: roster.App, ReadOnly: true, Complete: false, Workers: []hostPlanWorker{}, Actions: []hostPlanAction{}, Conflicts: []hostPlanProblem{}, Unavailable: []hostPlanProblem{}}
			fail := func(worker, reason string, code int) error {
				dto.Unavailable = append(dto.Unavailable, hostPlanProblem{worker, reason})
				if err := writeHostPlan(f, dto); err != nil {
					return err
				}
				return exitcode.Silent(code)
			}
			appID, err := env.App(cmd.Context(), roster.App)
			if err != nil {
				return fail("", "app_identity_unavailable", exitcode.FromError(err))
			}
			if appID == "" {
				return fail("", "app_identity_unavailable", exitcode.Error)
			}
			seen := map[string]bool{}
			for _, row := range roster.Workers {
				w, err := env.Worker(cmd.Context(), roster.App, row.Worker)
				if err != nil {
					return fail(row.Worker, "worker_identity_unavailable", exitcode.FromError(err))
				}
				if w.ID == "" || w.AppID == "" {
					return fail(row.Worker, "worker_identity_unavailable", exitcode.Error)
				}
				if w.AppID != appID || w.Retired {
					return exitcode.Newf(exitcode.Usage, "roster worker is outside the active target App scope")
				}
				if seen[w.ID] {
					return exitcode.Newf(exitcode.Usage, "roster contains duplicate resolved worker identities")
				}
				seen[w.ID] = true
				dto.Workers = append(dto.Workers, hostPlanWorker{Worker: row.Worker, ID: w.ID, Repo: row.Repo, Family: row.Family, Worktrees: []string{}, Terminals: []string{}, Sessions: []string{}})
				if w.Family != "" && w.Family != row.Family {
					dto.Conflicts = append(dto.Conflicts, hostPlanProblem{row.Worker, "recorded_family_mismatch"})
				}
			}
			inv, err := env.Inventory(cmd.Context(), roster.Workers)
			if err != nil {
				code := exitcode.FromError(err)
				return fail("", "host_inventory_unavailable", code)
			}
			assembleHostPlan(&dto, appID, inv)
			dto.Complete = len(dto.Unavailable) == 0
			if err := writeHostPlan(f, dto); err != nil {
				return err
			}
			if len(dto.Unavailable) > 0 {
				return exitcode.Silent(exitcode.Error)
			}
			if len(dto.Conflicts) > 0 {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		}}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return exitcode.Newf(exitcode.Usage, "invalid host plan flag; no apply or start surface is available")
	})
	cmd.Flags().StringVar(&file, "roster-file", "", "bounded version-1 JSON roster (required; host reads only)")
	return cmd
}
func readHostRoster(path string) (hostRoster, error) {
	bad := func() (hostRoster, error) {
		return hostRoster{}, exitcode.Newf(exitcode.Usage, "invalid roster: require a regular UTF-8 version-1 JSON file, at most 1 MiB, with an explicit App and 1..200 valid workers")
	}
	data, err := readHostFile(path, rosterLimit1635)
	if err != nil || !utf8.Valid(data) {
		return bad()
	}
	var r hostRoster
	dec := json.NewDecoder(bytes.NewReader(data))
	if cmdutil.CheckKeys(dec, reflect.TypeFor[hostRoster](), "") != nil {
		return bad()
	}
	if json.Unmarshal(data, &r) != nil || r.Version != 1 || len(r.Workers) < 1 || len(r.Workers) > 200 {
		return bad()
	}
	r.App, err = cmdutil.CanonicalAppRef("roster App", r.App)
	if err != nil || r.App == "" {
		return bad()
	}
	seen := map[string]bool{}
	for i := range r.Workers {
		w := &r.Workers[i]
		if strings.TrimSpace(w.Worker) == "" || strings.ContainsAny(w.Worker, "\x00\r\n") || (w.Family != "claude" && w.Family != "codex" && w.Family != "glm") {
			return bad()
		}
		if !filepath.IsAbs(w.Repo) || strings.ContainsRune(w.Repo, 0) {
			return bad()
		}
		for _, part := range strings.Split(filepath.ToSlash(w.Repo), "/") {
			if part == ".." {
				return bad()
			}
		}
		w.Repo, err = filepath.EvalSymlinks(w.Repo)
		if err != nil {
			return bad()
		}
		info, err := os.Stat(w.Repo)
		if err != nil || !info.IsDir() {
			return bad()
		}
		git, err := os.Stat(filepath.Join(w.Repo, ".git"))
		if err != nil || (!git.IsDir() && !git.Mode().IsRegular()) {
			return bad()
		}
		if seen[w.Worker] {
			return bad()
		}
		seen[w.Worker] = true
	}
	return r, nil
}
func readHostFile(path string, limit int64) ([]byte, error) {
	f, err := openHostReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("file read failed")
	}
	return data, nil
}
func writeHostPlan(f *cmdutil.Factory, dto hostPlanDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "Read-only host plan: %s (complete: %t; nothing executed)\n", output.SafeLine(dto.App, 256), dto.Complete); err != nil {
			return err
		}
		for _, row := range dto.Workers {
			if _, err := fmt.Fprintf(w, "%s: %d worktrees, %d terminals, %d recorded sessions\n", output.SafeLine(row.Worker, 256), len(row.Worktrees), len(row.Terminals), len(row.Sessions)); err != nil {
				return err
			}
		}
		for _, a := range dto.Actions {
			if _, err := fmt.Fprintf(w, "proposed %s for %s\n", a.Kind, output.SafeLine(a.Worker, 256)); err != nil {
				return err
			}
		}
		for _, group := range []struct {
			label string
			items []hostPlanProblem
		}{{"conflict", dto.Conflicts}, {"unavailable", dto.Unavailable}} {
			for _, p := range group.items {
				if _, err := fmt.Fprintf(w, "%s: %s %s\n", group.label, output.SafeLine(p.Worker, 256), p.Reason); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
