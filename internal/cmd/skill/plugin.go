package skill

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// `hadron skill plugin` — the plugin producer (#653, docs/plans/plugin-export.md).
//
// It is NOT a mode of `skill export`: under B8 (Holger, team chat #1108) the
// plugin is its own producer, writing to an explicit --out with no git
// requirement, and cor:agt:030:02's user-level destinations govern only
// individual export. So nothing here walks a skills root, pairs a file, moves
// or prunes: a bundle is built fresh, from a no-files EXPORT plan, into an
// artifact directory the command owns.

// Client reason codes specific to the producer (the shared ones are in export.go).
const (
	reasonArtifactIsLink  = "artifact-is-link"
	reasonArtifactNotOurs = "artifact-not-ours"
	reasonDuplicateName   = "duplicate-name"
	reasonNoFormat        = "host-has-no-format"
)

const (
	// pluginMarker names the file that marks an artifact directory as this
	// command's. An existing artifact is replaced wholesale ONLY when it
	// carries one (plan §7 Q5, mirroring #694's "never replace a file it did
	// not write"); anything else at that path is refused and left alone.
	pluginMarker   = ".hadron-plugin"
	pluginProducer = "hadron skill plugin"
	// pluginZipComment, followed by the host, marks a zip this command wrote,
	// for the same rule (see zipComment).
	pluginZipComment = "hadron skill plugin"
	// defaultPluginName is Holger's ruling (2026-09-24): "hadron", not
	// "hadron-skills", because a plugin can carry more than skills.
	defaultPluginName = "hadron"
)

// pluginNameRE is the plugin-name grammar every Claude surface accepts:
// lowercase words joined by hyphens (the Cowork org-upload rule, the
// strictest of them), at most 64 characters.
var pluginNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// zipTime is every zip entry's timestamp, so an unchanged bundle zips to
// identical bytes. 1980-01-01 is the earliest MS-DOS date a zip can hold.
var zipTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// pluginFormat is how one host's artifact is laid out (plan §3, which is
// deliberately outside the cor:agt:030:03 contract).
type pluginFormat struct {
	Name   string // the report's `format`
	Suffix string // appended to the plugin name for the directory and zip
}

// pluginFormats maps each host to its artifact. Claude: one plugin, which
// Claude Code, Cowork and org upload all read. Codex: skill folders for
// ~/.agents/skills, as the portal ships; a native Codex plugin waits on the
// X2/X3 measurements (plan §7 Q8).
var pluginFormats = map[string]pluginFormat{
	skilldoc.HostClaudeSkill: {Name: "claude-plugin"},
	skilldoc.HostCodexSkill:  {Name: "codex-skills", Suffix: "-codex"},
}

// hostRootPairs are the path shapes of a host's skills root, user- or
// project-level. `.codex/skills` is Codex's deprecated root: #621 never
// writes it, but Codex still reads it.
var hostRootPairs = [][2]string{{".claude", "skills"}, {".agents", "skills"}, {".codex", "skills"}}

type pluginFindingDTO struct {
	Node     string `json:"node"`
	NodeID   string `json:"nodeId"`
	Name     string `json:"name"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type pluginScopeDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MemoryCount  int    `json:"memoryCount"`
	DroppedCount int    `json:"droppedCount"`
	// ResolvedVia is the server's answer to which owner's scope of that name
	// won (App > Agent > organization).
	ResolvedVia string `json:"resolvedVia"`
	// source says which App a scope NAME was resolved in, and where that App
	// came from (review:ambient-scope-must-report-its-source). Render-only.
	source string
}

type pluginHostDTO struct {
	Host   string `json:"host"`
	Format string `json:"format"`
	// Artifact and Zip are resolved paths; null when nothing was (or, on a
	// dry run, would be) written for this host.
	Artifact *string `json:"artifact"`
	Zip      *string `json:"zip"`
	// Version is the Claude manifest's version: derived from the bundled
	// content, so an unchanged bundle keeps it and any change moves it (Claude
	// Code compares versions for EQUALITY — measured on 2.1.143, a lower-sorting
	// version still updated). Null for a format with no manifest.
	Version    *string            `json:"version"`
	Failure    *exportReasonDTO   `json:"failure"`
	Scanned    int                `json:"scanned"`
	Judged     int                `json:"judged"`
	Included   []exportItemDTO    `json:"included"`
	Skipped    []exportItemDTO    `json:"skipped"`
	Refused    []exportItemDTO    `json:"refused"`
	Failed     []exportItemDTO    `json:"failed"`
	NotForHost []exportItemDTO    `json:"notForHost"`
	Findings   []pluginFindingDTO `json:"findings"`
}

type pluginDTO struct {
	DryRun       bool                    `json:"dryRun"`
	Name         string                  `json:"name"`
	Out          string                  `json:"out"`
	Scope        *pluginScopeDTO         `json:"scope"`
	Hosts        []pluginHostDTO         `json:"hosts"`
	Unrecognized []exportUnrecognizedDTO `json:"unrecognized"`
}

func newPluginHost(host, format string) pluginHostDTO {
	return pluginHostDTO{
		Host: host, Format: format,
		Included: []exportItemDTO{}, Skipped: []exportItemDTO{}, Refused: []exportItemDTO{},
		Failed: []exportItemDTO{}, NotForHost: []exportItemDTO{}, Findings: []pluginFindingDTO{},
	}
}

type pluginOpts struct {
	out    string
	name   string
	scope  string
	zip    bool
	dryRun bool
}

func newCmdPlugin(f *cmdutil.Factory) *cobra.Command {
	var opts pluginOpts
	cmd := &cobra.Command{
		Use:   "plugin --out <dir>",
		Short: "Build an installable plugin of every enabled task you can read",
		Long: `Build a plugin: one installable unit carrying the skill for every enabled
task you can read, one artifact per host, written under --out.

  claudeSkill  <out>/<name>/        a Claude plugin, which is also a one-plugin
                                    marketplace: install it with
                                      claude plugin marketplace add <out>/<name>
                                      claude plugin install <name>@<name>
  codexSkill   <out>/<name>-codex/  skill folders: copy them into ~/.agents/skills

With --zip, each artifact is also zipped beside it (<name>.zip,
<name>-codex.zip). Upload the Claude zip to Cowork or your organization's
plugin settings.

--out is required and nothing is inferred from the working directory or a git
checkout. The plugin is named "hadron" unless --name says otherwise; the name is
permanent for an install, and skills are invoked as /<name>:<skill>.

WHAT IS INCLUDED. Every enabled declaration you can read, customer and personal
memories too, unless --scope narrows it to one scope's memories. A scope with no
memories you can read builds nothing and exits 2: it never widens to everything.

The server renders every skill and decides what may be included; this command
writes what it planned and reports every item:
  included     bundled into the artifact
  skipped      not bundled, and not an error (a disabled declaration, say)
  refused      not bundled: two skills claim one name, or similar
  failed       not bundled: the skill is over a host limit, or could not be written
  notForHost   declared only for the other host

The artifact directory is replaced wholesale, and only when an earlier run of
this command wrote it. Anything else at that path is refused and left alone, as
is an artifact path that is a symbolic link, or any path at or inside a host's
skills directory (.claude/skills, .agents/skills, .codex/skills): a plugin
there would load every skill twice.

Exit codes: 0 when every item was included or skipped. 5 after the full report
when any item was refused or failed, or a host's artifact could not be written.
2 for a bad flag, an unusable --out or an empty scope. A run that cannot start
(not signed in, server unreachable) exits with that error's code.`,
		Example: `  hadron skill plugin --out ~/plugins --dry-run
  hadron skill plugin --out ~/plugins --zip
  hadron skill plugin --out ./dist --scope research --name research-skills --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlugin(cmd, f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.out, "out", "", "directory to write the artifacts into (required)")
	cmd.Flags().StringVar(&opts.name, "name", defaultPluginName, "plugin name: lowercase words joined by hyphens, at most 64 characters")
	cmd.Flags().StringVar(&opts.scope, "scope", "", "include only the tasks in this scope's memories (name or id)")
	cmd.Flags().BoolVar(&opts.zip, "zip", false, "also write each artifact as a zip beside it")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "report what would be built, and write nothing")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

func runPlugin(cmd *cobra.Command, f *cmdutil.Factory, opts pluginOpts) error {
	// An EMPTY value is not an absent one. `--out ""` satisfies cobra's
	// required check and would resolve to the working directory, which B8
	// rules out; `--scope ""` read as "no scope" would widen to everything.
	if strings.TrimSpace(opts.out) == "" {
		return exitcode.Newf(exitcode.Usage, "--out is empty: name the directory to write the plugin into")
	}
	if cmd.Flags().Changed("scope") && strings.TrimSpace(opts.scope) == "" {
		return exitcode.Newf(exitcode.Usage, "--scope is empty: name a scope, or omit --scope to include every task you can read")
	}
	if !pluginNameRE.MatchString(opts.name) || len(opts.name) > 64 {
		return exitcode.Newf(exitcode.Usage,
			"--name %q is not a plugin name: use lowercase letters and digits in words joined by hyphens, at most 64 characters", opts.name)
	}
	if windowsReserved(opts.name) {
		return exitcode.Newf(exitcode.Usage,
			"--name %q is a Windows device name, which no Windows filesystem can hold as a file or directory: choose another", opts.name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return exitcode.Newf(exitcode.Error, "cannot locate your home directory: %v", err)
	}
	out, err := resolveOut(opts.out, home)
	if err != nil {
		return err
	}
	dto := pluginDTO{DryRun: opts.dryRun, Name: opts.name, Out: out, Hosts: []pluginHostDTO{}, Unrecognized: []exportUnrecognizedDTO{}}

	// Every artifact path is checked BEFORE anything is planned or written:
	// a plugin inside a host's skills root is a usage error, not an item
	// failure (plan §7 Q9, Jane's call in team chat #1436).
	for _, h := range skilldoc.Hosts {
		pf, ok := pluginFormats[h.Key]
		if !ok {
			continue
		}
		artifactDir := filepath.Join(out, opts.name+pf.Suffix)
		literal := filepath.Join(absPath(expandHome(opts.out, home)), opts.name+pf.Suffix)
		if err := refuseHostRoot(artifactDir, literal, home); err != nil {
			return err
		}
		// "Above a root" also means an existing artifact directory that
		// holds one (a project checked out inside it, say). The replace check
		// refuses it again at write time; this makes it a usage error before
		// any request, as documented.
		if fi, err := os.Lstat(artifactDir); err == nil {
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				// A link is refused at write time anyway; one into a skills
				// root is refused here, before any request, as documented.
				if target, err := filepath.EvalSymlinks(artifactDir); err == nil {
					if err := refuseHostRoot(target, target, home); err != nil {
						return err
					}
				}
			case fi.IsDir():
				if root, ok := containsHostRoot(artifactDir); ok {
					return hostRootError(artifactDir, root)
				}
			}
		}
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}

	var memories []string
	if opts.scope != "" {
		scope, ids, err := resolvePluginScope(cmd, f, opts.scope)
		if err != nil {
			return err
		}
		dto.Scope = scope
		// An empty scope must NEVER call skillPlan: `memories` is omitempty,
		// and an omitted list means every memory the caller can read — the
		// opposite of the narrowing asked for (@codex P1 on #700).
		if len(ids) == 0 {
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Scope %s (%s) has no memory you can read (%d hidden from you): nothing was built.\n",
					cmp(scope.Name, scope.ID), scope.source, scope.DroppedCount)
				return err
			}); err != nil {
				return err
			}
			return exitcode.Silent(exitcode.Usage)
		}
		memories = ids
	}

	// Every host's plan first: a host's notForHost list comes from the OTHER
	// host's plan (the portal's otherHostOnly), so no artifact can be built
	// until both are in.
	plans := map[string]*gen.SkillExportPlanSkillPlan{}
	failures := map[string]*exportReasonDTO{}
	for _, h := range skilldoc.Hosts {
		p, err := fetchPluginPlan(cmd, client, h.Key, memories)
		if err != nil {
			// Nothing is written until every plan is in, so an auth or
			// transport error on ANY host is the run's error, not a host
			// failure beside a published partial bundle (@codex on #707).
			mapped := api.MapError(err)
			if code := exitcode.FromError(mapped); code == exitcode.AuthRequired || code == exitcode.Unavailable {
				return mapped
			}
			failures[h.Key] = &exportReasonDTO{Code: reasonPlanRefused, Message: mapped.Error(), Origin: originClient}
			continue
		}
		plans[h.Key] = p
	}

	var unrecognized []*gen.SkillExportPlanSkillPlanUnrecognized
	for _, h := range skilldoc.Hosts {
		pf, ok := pluginFormats[h.Key]
		if !ok {
			hd := newPluginHost(h.Key, "")
			hd.Failure = &exportReasonDTO{Code: reasonNoFormat,
				Message: fmt.Sprintf("this CLI has no plugin format for host %s, so nothing was built for it; upgrade the CLI", h.Key), Origin: originClient}
			failAll(&hd, plans[h.Key], *hd.Failure)
			dto.Hosts = append(dto.Hosts, hd)
			continue
		}
		hd := newPluginHost(h.Key, pf.Name)
		if r := failures[h.Key]; r != nil {
			hd.Failure = r
			dto.Hosts = append(dto.Hosts, hd)
			continue
		}
		p := plans[h.Key]
		if unrecognized == nil {
			unrecognized = p.Unrecognized
		}
		files := bundleHost(&hd, p, otherPlans(plans, h.Key))
		b := buildArtifact(h.Key, opts.name, dto.Scope, files)
		if b.version != "" {
			hd.Version = &b.version
		}
		dir := filepath.Join(out, opts.name+pf.Suffix)
		hd.Artifact = &dir
		var zipPath string
		if opts.zip {
			zipPath = dir + ".zip"
			hd.Zip = &zipPath
		}
		// The replaceability check runs on a dry run too, so a dry run
		// reports the refusal the real run would hit (#694's parity rule).
		res := writeResult{r: checkArtifactPaths(dir, zipPath, h.Key)}
		if res.r == nil && !opts.dryRun {
			res = writePluginArtifact(out, dir, zipPath, b)
		}
		applyWriteResult(&hd, res)
		dto.Hosts = append(dto.Hosts, hd)
	}
	dto.Unrecognized = toUnrecognizedDTO(unrecognized)

	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderPlugin(w, dto)
	}); err != nil {
		return err
	}
	if pluginHasFailures(dto) {
		return exitcode.Silent(exitcode.Conflict)
	}
	return nil
}

// writeResult is how one host's write ended: which pieces were published,
// and the failure, if any. A failure can follow a publish — the zip failed,
// or the previous artifact could not be removed — so the flags and the
// reason are independent.
type writeResult struct {
	dir, zip bool
	r        *exportReasonDTO
}

// applyWriteResult records how one host's write ended. A failure before the
// directory was published means nothing was built: every included skill is
// failed, named. A failure after it leaves a live directory, so the
// directory and its skills stay reported, and the failure says what else
// went wrong.
func applyWriteResult(hd *pluginHostDTO, res writeResult) {
	if res.r == nil {
		return
	}
	r := res.r
	hd.Failure = r
	if !res.zip {
		hd.Zip = nil
	}
	if res.dir {
		return
	}
	hd.Artifact = nil
	for _, it := range hd.Included {
		it.Reasons = append([]exportReasonDTO{*r}, it.Reasons...)
		hd.Failed = append(hd.Failed, it)
	}
	hd.Included = []exportItemDTO{}
}

func fetchPluginPlan(cmd *cobra.Command, client graphql.Client, host string, memories []string) (*gen.SkillExportPlanSkillPlan, error) {
	resp, err := gen.SkillExportPlan(cmd.Context(), client, &gen.SkillPlanInput{
		Intent:   gen.SkillPlanIntentExport,
		Host:     &host,
		Memories: memories,
	})
	if err != nil {
		return nil, err
	}
	if resp.SkillPlan == nil {
		return nil, errors.New("skillPlan returned no result")
	}
	return resp.SkillPlan, nil
}

// resolvePluginScope resolves --scope to the memories the caller can read,
// in scope order. This is CLIENT-side (plan §7 Q7, option a) and temporary:
// selection belongs on the server (a `scope` on SkillPlanInput), so MCP and
// the portal get it too.
func resolvePluginScope(cmd *cobra.Command, f *cmdutil.Factory, ref string) (*pluginScopeDTO, []string, error) {
	ref = strings.TrimSpace(ref)
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, nil, err
	}
	var scopeRef, name, appPtr *string
	source := "by id"
	if cmdutil.IsBareID(ref) {
		scopeRef = &ref
	} else {
		appRef, err := f.App()
		if err != nil {
			return nil, nil, err
		}
		if appRef == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage,
				"a scope name resolves in an App's context — pass --app <ref>, run `hadron app set-active <ref>`, or give the scope's id instead")
		}
		name, appPtr = &ref, &appRef
		from := "the App context"
		if f.AppFlag != "" {
			from = "--app"
		}
		source = fmt.Sprintf("in App %s, from %s", appRef, from)
	}
	resp, err := gen.ScopeExplain(cmd.Context(), client, scopeRef, name, appPtr, nil)
	if err != nil {
		return nil, nil, api.MapError(err)
	}
	if resp == nil || resp.ScopeExplain == nil || resp.ScopeExplain.Scope == nil {
		return nil, nil, exitcode.Newf(exitcode.NotFound, "no scope %q is readable here", ref)
	}
	ex := resp.ScopeExplain
	ids := []string{}
	for _, m := range ex.Memories {
		if m != nil {
			ids = append(ids, m.Id)
		}
	}
	return &pluginScopeDTO{
		ID: ex.Scope.Id, Name: ex.Scope.Name,
		MemoryCount: len(ids), DroppedCount: ex.DroppedCount,
		ResolvedVia: string(ex.ResolvedVia), source: source,
	}, ids, nil
}

func otherPlans(plans map[string]*gen.SkillExportPlanSkillPlan, host string) []*gen.SkillExportPlanSkillPlan {
	var out []*gen.SkillExportPlanSkillPlan
	for _, h := range skilldoc.Hosts {
		if h.Key != host && plans[h.Key] != nil {
			out = append(out, plans[h.Key])
		}
	}
	return out
}

// failAll names every planned entry as failed with reason r, the #621
// blockHost rule: a host that cannot be built still names what it held.
func failAll(hd *pluginHostDTO, p *gen.SkillExportPlanSkillPlan, r exportReasonDTO) {
	if p == nil {
		return
	}
	hd.Scanned, hd.Judged = p.Scanned, p.Judged
	for _, e := range p.Entries {
		if e == nil {
			continue
		}
		it := itemFor(e)
		it.Reasons = append([]exportReasonDTO{r}, it.Reasons...)
		hd.Failed = append(hd.Failed, it)
	}
}

// bundleHost buckets one host's plan by the action the SERVER planned — it
// never re-judges — and returns the skill files to bundle, keyed by skill
// name. The client adds only I/O facts: an unsafe or duplicate name, a WRITE
// with no body, or an action a no-files plan should never produce.
func bundleHost(hd *pluginHostDTO, p *gen.SkillExportPlanSkillPlan, others []*gen.SkillExportPlanSkillPlan) map[string]string {
	hd.Scanned, hd.Judged = p.Scanned, p.Judged
	files := map[string]string{}

	// Two WRITEs claiming one name cannot both be bundled, and picking one
	// would be the client judging: both fail (the portal skips both).
	writes := map[string]int{}
	for _, e := range p.Entries {
		if e != nil && e.ExportPlan != nil && e.ExportPlan.Action == gen.SkillExportActionWrite {
			writes[e.Name]++
		}
	}

	judged := map[string]bool{}
	for _, e := range p.Entries {
		if e == nil {
			continue
		}
		judged[e.NodeId] = true
		it := itemFor(e)
		failed := false
		fail := func(r exportReasonDTO) {
			it.Reasons = append(it.Reasons, r)
			hd.Failed = append(hd.Failed, it)
			failed = true
		}
		switch ep := e.ExportPlan; {
		case ep == nil:
			fail(exportReasonDTO{Code: reasonNoExportPlan, Message: "the server returned no export plan for this skill", Origin: originClient})
		case ep.Action == gen.SkillExportActionSkip:
			hd.Skipped = append(hd.Skipped, it)
		case ep.Action == gen.SkillExportActionRefuse:
			hd.Refused = append(hd.Refused, it)
			failed = true
		case ep.Action == gen.SkillExportActionFail:
			hd.Failed = append(hd.Failed, it)
			failed = true
		case ep.Action == gen.SkillExportActionWrite:
			switch {
			case e.RenderedBody == nil:
				fail(exportReasonDTO{Code: reasonNoBody, Message: "the server planned a write but sent no rendered body", Origin: originClient})
			case !safeDirName(e.Name) || windowsReserved(e.Name):
				fail(unsafeName(e.Name))
			case writes[e.Name] > 1:
				fail(exportReasonDTO{Code: reasonDuplicateName,
					Message: fmt.Sprintf("%d skills in this bundle are named %q, so none of them was included", writes[e.Name], e.Name), Origin: originClient})
			default:
				files[e.Name] = *e.RenderedBody
				hd.Included = append(hd.Included, it)
			}
		default:
			// MOVE and REMOVE act on a file on disk, and a bundle submits none.
			fail(exportReasonDTO{Code: reasonUnknownAction,
				Message: fmt.Sprintf("the server planned %q, which a plugin build cannot act on; nothing was included", ep.Action), Origin: originClient})
		}
		for _, fd := range e.Findings {
			if fd == nil {
				continue
			}
			// An error on a refused or failed entry is carried by its reasons;
			// anything else — a warning, an unknown severity, an error on a
			// SKIPPED (disabled) entry — is surfaced here, and never charged.
			if failed && fd.Severity == "error" {
				continue
			}
			hd.Findings = append(hd.Findings, pluginFindingDTO{
				Node: e.Urn, NodeID: e.NodeId, Name: e.Name, Rule: fd.Rule, Severity: fd.Severity, Message: fd.Message,
			})
		}
	}

	seen := map[string]bool{}
	for _, o := range others {
		for _, e := range o.Entries {
			if e == nil || judged[e.NodeId] || seen[e.NodeId] || deref(e.Class) == "disabled" {
				continue
			}
			seen[e.NodeId] = true
			hd.NotForHost = append(hd.NotForHost, exportItemDTO{Node: e.Urn, NodeID: e.NodeId, Name: e.Name, Reasons: []exportReasonDTO{}, Kept: []string{}})
		}
	}
	return files
}

// windowsReserved reports a Windows device name (CON, NUL, COM1, …), which
// no Windows filesystem can hold as a directory. A bundle is portable — the
// zip is unpacked wherever its user is — so such a skill fails as an item on
// every platform, rather than failing the whole artifact on Windows.
func windowsReserved(name string) bool {
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
		return true
	}
	return false
}

// artifact is one host's bundle, fully laid out in memory: dirFiles are
// written to the directory, zipFiles to the zip. Paths are slash-separated
// and relative to the artifact root.
type artifact struct {
	host     string
	version  string
	dirFiles map[string][]byte
	zipFiles map[string][]byte
}

func buildArtifact(host, name string, scope *pluginScopeDTO, skills map[string]string) artifact {
	skillNames := make([]string, 0, len(skills))
	for n := range skills {
		skillNames = append(skillNames, n)
	}
	sort.Strings(skillNames)

	a := artifact{host: host, dirFiles: map[string][]byte{}, zipFiles: map[string][]byte{}}
	marker := mustJSON(map[string]string{"producer": pluginProducer, "host": host})

	if host != skilldoc.HostClaudeSkill {
		// Codex: skill folders, to be copied into ~/.agents/skills.
		for _, n := range skillNames {
			a.dirFiles[n+"/SKILL.md"] = []byte(skills[n])
			a.zipFiles[n+"/SKILL.md"] = []byte(skills[n])
		}
		a.dirFiles[pluginMarker] = marker
		return a
	}

	desc := "Task skills exported from Hadron"
	if scope != nil {
		desc += " (scope " + cmp(scope.Name, scope.ID) + ")"
	}
	// The version is a hash of everything the version stands for: the
	// manifest's name and description and every skill. Each part is
	// length-prefixed, so no body can be read as the next name. The `h` keeps
	// the pre-release identifier alphanumeric: an all-digit one with a leading
	// zero is not valid semver.
	sum := sha256.New()
	part := func(v string) { _, _ = fmt.Fprintf(sum, "%d:%s", len(v), v) }
	part(name)
	part(desc)
	for _, n := range skillNames {
		part(n)
		part(skills[n])
	}
	a.version = "0.0.0-h" + hex.EncodeToString(sum.Sum(nil))[:12]
	manifest := mustJSON(struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}{name, a.version, desc})
	// The directory is also a one-plugin marketplace whose plugin is itself
	// (`source: "./"`), so `claude plugin marketplace add <dir>` installs it
	// with no git and no wrapper directory. Measured on Claude Code 2.1.143:
	// it validates, installs, and updates when the version moves.
	type owner struct {
		Name string `json:"name"`
	}
	type meta struct {
		Description string `json:"description"`
	}
	type entry struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	marketplace := mustJSON(struct {
		Name     string  `json:"name"`
		Owner    owner   `json:"owner"`
		Metadata meta    `json:"metadata"`
		Plugins  []entry `json:"plugins"`
	}{name, owner{"Hadron"}, meta{desc}, []entry{{name, "./", desc, a.version}}})

	a.dirFiles[".claude-plugin/plugin.json"] = manifest
	a.zipFiles[".claude-plugin/plugin.json"] = manifest
	a.dirFiles[".claude-plugin/marketplace.json"] = marketplace
	for _, n := range skillNames {
		a.dirFiles["skills/"+n+"/SKILL.md"] = []byte(skills[n])
		a.zipFiles["skills/"+n+"/SKILL.md"] = []byte(skills[n])
	}
	a.dirFiles[pluginMarker] = marker
	return a
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // only fixed struct shapes are marshalled here
	}
	return append(b, '\n')
}

// writePluginArtifact writes one host's artifact under out.
//
// THREAT MODEL, stated so the guarantees below are read at their true size
// (review:enumerate-the-paths-before-promising-a-guarantee). It protects
// what is at, or appears at, an ARTIFACT path — a foreign file or directory,
// a link, another host's artifact — at the instant of every move, and
// --out as it resolved when the run began (re-resolved after creation). It
// does not defend against a process that renames --out itself or its
// parents while the run is writing: such a racer can do whatever the user
// can, and closing it would need every write anchored to an opened directory
// handle. That is the configuration-vs-racing-process line #694 drew for
// skill export's hostFS. Each piece is
// built in a temp sibling and renamed into place, so an interrupted run never
// leaves a half-plugin that installs, and the rename never follows a link at
// the destination.
//
// The result says which pieces were published: a failure AFTER the
// directory (publishing the zip, removing the previous artifact) leaves a
// live directory the caller must still report.
func writePluginArtifact(out, dir, zipPath string, a artifact) writeResult {
	fail := func(err error) writeResult {
		r := ioReason(err)
		return writeResult{r: &r}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fail(err)
	}
	// resolveOut resolved only the part of --out that existed; re-resolve now
	// that all of it does, so a component created as a link in between
	// (pointing into a skills directory, say) is refused, not followed.
	if got, err := filepath.EvalSymlinks(out); err != nil || got != out {
		return writeResult{r: &exportReasonDTO{Code: reasonArtifactIsLink,
			Message: fmt.Sprintf("%s changed while the plugin was being built (it now resolves to %s), so nothing was written", out, cmp(got, "nothing")), Origin: originClient}}
	}
	if r := checkArtifactPaths(dir, zipPath, a.host); r != nil {
		return writeResult{r: r}
	}

	tmp, err := os.MkdirTemp(out, "."+filepath.Base(dir)+".tmp-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for rel, body := range a.dirFiles {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			return fail(err)
		}
	}

	var zipTmp string
	if zipPath != "" {
		zf, err := os.CreateTemp(out, "."+filepath.Base(zipPath)+".tmp-")
		if err != nil {
			return fail(err)
		}
		zipTmp = zf.Name()
		defer func() { _ = os.Remove(zipTmp) }()
		werr := writeZip(zf, a.zipFiles, zipComment(a.host))
		if cerr := zf.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fail(werr)
		}
	}

	published, cleanup := swapInto(tmp, dir, true, a.host)
	if !published {
		return writeResult{r: cleanup}
	}
	res := writeResult{dir: true, r: cleanup}
	if zipTmp != "" {
		zpub, zr := swapInto(zipTmp, zipPath, false, a.host)
		if !zpub {
			zr.Message = fmt.Sprintf("%s was written, but its zip was not: %s", dir, zr.Message)
		}
		res.zip = zpub
		res.r = joinReasons(res.r, zr)
	}
	return res
}

// joinReasons keeps every failure of one host's write: a leftover's location
// is its only recovery path, so a later failure must not overwrite it.
func joinReasons(a, b *exportReasonDTO) *exportReasonDTO {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	j := *a
	j.Message += "; and " + b.Message
	return &j
}

func checkArtifactPaths(dir, zipPath, host string) *exportReasonDTO {
	if r := checkReplaceable(dir, true, host); r != nil {
		return r
	}
	if zipPath != "" {
		return checkReplaceable(zipPath, false, host)
	}
	return nil
}

// zipComment marks a zip as this command's, for one host: a Claude build
// never replaces a Codex zip at the same path, or the reverse (@copilot on
// #707 — `--name foo-codex` names Claude's artifact what `--name foo` names
// Codex's).
func zipComment(host string) string { return pluginZipComment + " " + host }

// checkReplaceable is plan §7 Q5: a path that does not exist is free; one
// this command wrote (a directory carrying the marker, a zip carrying the
// comment) may be replaced; anything else — a link included — is refused.
func checkReplaceable(p string, isDir bool, host string) *exportReasonDTO {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r := ioReason(err)
		return &r
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return &exportReasonDTO{Code: reasonArtifactIsLink,
			Message: fmt.Sprintf("%s is a symbolic link, so nothing was written through it", p), Origin: originClient}
	}
	notOurs := &exportReasonDTO{Code: reasonArtifactNotOurs,
		Message: fmt.Sprintf("%s exists and was not written by `hadron skill plugin`, so it was left alone; move it or choose another --out or --name", p), Origin: originClient}
	if isDir {
		if !fi.IsDir() || !hasPluginMarker(p, host) {
			return notOurs
		}
		// A marked directory someone has since put a host skills root inside
		// is refused too: replacing it wholesale would delete that root, and
		// this command never writes one.
		if root, ok := containsHostRoot(p); ok {
			return &exportReasonDTO{Code: reasonArtifactNotOurs,
				Message: fmt.Sprintf("%s holds a host skills directory (%s) that this command did not write, so it was left alone", p, root), Origin: originClient}
		}
		return nil
	}
	if !fi.Mode().IsRegular() {
		return notOurs
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return notOurs
	}
	defer func() { _ = zr.Close() }()
	if zr.Comment != zipComment(host) {
		return notOurs
	}
	return nil
}

// containsHostRoot walks dir (never following a link) for a host skills
// root: a `skills` directory directly inside `.claude`, `.agents` or `.codex`.
//
// It fails CLOSED: a subtree it cannot read is reported as found, since an
// unreadable directory may hold exactly the root this scan protects.
func containsHostRoot(dir string) (string, bool) {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			found = p + " (unreadable, so it could not be checked)"
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// This command never writes a link, so one inside its artifact is
			// someone else's — and may point at a skills root.
			found = p + " (a link, which this command never writes)"
			return fs.SkipAll
		}
		if !d.IsDir() || p == dir {
			return nil
		}
		if _, ok := insideRootShape(strings.TrimPrefix(p, dir)); ok {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found, found != ""
}

// hasPluginMarker reports whether dir carries this command's marker FOR
// host: another host's artifact at the same path is not ours to replace.
func hasPluginMarker(dir, host string) bool {
	p := filepath.Join(dir, pluginMarker)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var m struct {
		Producer string `json:"producer"`
		Host     string `json:"host"`
	}
	return json.Unmarshal(b, &m) == nil && m.Producer == pluginProducer && m.Host == host
}

// swapInto publishes the built tmp (a directory, or a zip when isDir is
// false) at dest. An earlier artifact is moved aside first and re-verified
// AFTER the move, since that copy is what gets deleted: one swapped in after
// checkReplaceable ran is put back and refused, never replaced.
//
// The remaining window is the moved-aside name itself, a fresh random name
// under --out that only this run knows; a process that re-binds it between
// the recheck and the delete is racing this command on purpose, which no
// portable delete can rule out (the same line as #694's hostFS).
//
// published says whether tmp is now at dest. A reason with published=true
// means only the previous artifact could not be removed; it says where it is.
func swapInto(tmp, dest string, isDir bool, host string) (published bool, _ *exportReasonDTO) {
	if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
		return publish(tmp, dest, isDir)
	}
	old := tmp + "-old"
	if err := os.Rename(dest, old); err != nil {
		r := ioReason(err)
		return false, &r
	}
	if r := checkReplaceable(old, isDir, host); r != nil || !pathExists(old) {
		return false, restore(old, dest, &exportReasonDTO{Code: reasonArtifactNotOurs,
			Message: fmt.Sprintf("%s changed while the plugin was being built and is no longer one this command wrote, so it was left alone", dest), Origin: originClient})
	}
	pub, r := publish(tmp, dest, isDir)
	if !pub {
		return false, restore(old, dest, r)
	}
	if err := os.RemoveAll(old); err != nil {
		r = joinReasons(r, &exportReasonDTO{Code: reasonIOError,
			Message: fmt.Sprintf("%s was replaced, but the previous artifact could not be removed (%v) and is still at %s", dest, err, old), Origin: originClient})
	}
	return true, r
}

// linkFile is os.Link, swappable so a test can stand in a filesystem that
// has no hard links.
var linkFile = os.Link

// publish puts tmp at dest WITHOUT replacing anything there. It is the
// ONLY way this command moves anything onto a name the user can see —
// publishing a build, and putting a moved-aside artifact back — so the
// guarantee "a path this command did not write is never replaced" holds at
// the instant of every move, not only when it was last checked (#707).
//   - A file is hard-linked into place, which fails if the name exists, and
//     its temp name is then removed. A filesystem without hard links is
//     refused rather than risked with a rename.
//   - A directory is renamed: a rename cannot replace a file or a non-empty
//     directory on any platform. It CAN replace an EMPTY directory, which
//     holds nothing to lose; that is the one stated residue.
//
// The other stated boundary is the moved-aside name (see swapInto): a fresh
// random name no user path points at.
//
// published says whether tmp is now at dest; a reason with published=true
// means only the temp name could not be removed, and says where it is.
func publish(tmp, dest string, isDir bool) (published bool, _ *exportReasonDTO) {
	occupied := &exportReasonDTO{Code: reasonArtifactNotOurs,
		Message: fmt.Sprintf("%s appeared while the plugin was being built, so it was left alone", dest), Origin: originClient}
	if !isDir {
		err := linkFile(tmp, dest)
		switch {
		case err == nil:
			if err := os.Remove(tmp); err != nil {
				return true, &exportReasonDTO{Code: reasonIOError,
					Message: fmt.Sprintf("%s was published, but its temporary copy could not be removed (%v) and is still at %s", dest, err, tmp), Origin: originClient}
			}
			return true, nil
		case errors.Is(err, fs.ErrExist):
			return false, occupied
		default:
			return false, &exportReasonDTO{Code: reasonIOError,
				Message: fmt.Sprintf("%s could not be published without risking a file that is not ours: this filesystem refused a hard link (%v)", dest, err), Origin: originClient}
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		if pathExists(dest) {
			return false, occupied
		}
		r := ioReason(err)
		return false, &r
	}
	return true, nil
}

// restore puts a moved-aside artifact back with the same no-replace
// publish, so it never displaces something that appeared at dest meanwhile.
// When it cannot, the artifact stays at its moved-aside name, and the
// reason says where.
func restore(old, dest string, r *exportReasonDTO) *exportReasonDTO {
	// Put back by what was actually moved aside, not by what was expected
	// there: a directory that appeared at the zip path must go back as one.
	fi, err := os.Lstat(old)
	if err != nil {
		r.Message += fmt.Sprintf("; the previous artifact could not be put back (%v)", err)
		return r
	}
	switch pub, pr := publish(old, dest, fi.IsDir()); {
	case !pub:
		r.Message += fmt.Sprintf("; the previous artifact could not be put back (%s), so it is now at %s", pr.Message, old)
	case pr != nil:
		r.Message += "; the previous artifact was put back, but " + pr.Message
	}
	return r
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func writeZip(w io.Writer, files map[string][]byte, comment string) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	zw := zip.NewWriter(w)
	for _, n := range names {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, bytes.NewReader(files[n])); err != nil {
			return err
		}
	}
	if err := zw.SetComment(comment); err != nil {
		return err
	}
	return zw.Close()
}

// resolveOut makes --out absolute and resolves the part of it that exists:
// the user named it, so following a link they typed is what they asked for
// (the $HOME rule of #621, moved to a new anchor). The rest is created later.
func resolveOut(raw, home string) (string, error) {
	p := absPath(expandHome(raw, home))
	cur, rest := p, []string{}
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			fi, err := os.Stat(r)
			if err != nil {
				return "", exitcode.Newf(exitcode.Usage, "--out %s: %v", raw, err)
			}
			if !fi.IsDir() {
				return "", exitcode.Newf(exitcode.Usage, "--out %s: %s is not a directory", raw, cur)
			}
			return filepath.Join(append([]string{r}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", exitcode.Newf(exitcode.Usage, "--out %s: %v", raw, err)
		}
		// Missing, or a link to something missing? A dangling link is not a
		// path to create: whatever later appears at its target would be
		// followed.
		if fi, lerr := os.Lstat(cur); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", exitcode.Newf(exitcode.Usage, "--out %s: %s is a link to something that does not exist", raw, cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// refuseHostRoot is plan §7 Q9: an artifact at or inside a host's skills
// root would load every skill twice (bare, and as <plugin>:<skill>), and
// #621's export would report the plugin as a foreign skill. It checks the
// ARTIFACT path, not --out — `--out ~/.claude --name skills` lands exactly on
// the root — both as typed and as resolved, by path shape (so project-level
// roots count too, with no git) and against the user-level roots resolved on
// disk (which catches a root reached through a link).
func refuseHostRoot(resolved, literal, home string) error {
	for _, p := range []string{resolved, literal} {
		if root, ok := insideRootShape(p); ok {
			return hostRootError(resolved, root)
		}
	}
	for _, pair := range hostRootPairs {
		root, err := filepath.EvalSymlinks(filepath.Join(home, pair[0], pair[1]))
		if err != nil {
			continue
		}
		if within(resolved, root) || within(root, resolved) {
			return hostRootError(resolved, root)
		}
	}
	return nil
}

func hostRootError(artifact, root string) error {
	return exitcode.Newf(exitcode.Usage,
		"the plugin would be written to %s, which is at, inside or above the host skills directory %s: a plugin there loads every skill twice. Choose an --out outside every skills directory",
		artifact, root)
}

func insideRootShape(p string) (string, bool) {
	comps := strings.Split(filepath.ToSlash(filepath.Clean(p)), "/")
	for i := 0; i+1 < len(comps); i++ {
		for _, pair := range hostRootPairs {
			// Case-folded: on the default macOS and Windows filesystems
			// `.CLAUDE/Skills` IS `.claude/skills`, and refusing an oddly-cased
			// look-alike on a case-sensitive one costs nothing.
			if strings.EqualFold(comps[i], pair[0]) && strings.EqualFold(comps[i+1], pair[1]) {
				return filepath.FromSlash(strings.Join(comps[:i+2], "/")), true
			}
		}
	}
	return "", false
}

// within reports whether p is root or below it, case-folded for the same
// reason as insideRootShape.
func within(p, root string) bool {
	rel, err := filepath.Rel(strings.ToLower(root), strings.ToLower(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pluginHasFailures is #621's exit rule: 5 after the full report when any
// host has a failure or any item was refused or failed. Findings never count.
func pluginHasFailures(dto pluginDTO) bool {
	for _, h := range dto.Hosts {
		if h.Failure != nil || len(h.Refused) > 0 || len(h.Failed) > 0 {
			return true
		}
	}
	return false
}

func renderPlugin(w io.Writer, dto pluginDTO) error {
	p := func(format string, a ...any) error {
		_, err := fmt.Fprintf(w, format, a...)
		return err
	}
	if dto.DryRun {
		if err := p("Dry run: nothing was written.\n"); err != nil {
			return err
		}
	}
	if err := p("Plugin %q → %s\n", dto.Name, dto.Out); err != nil {
		return err
	}
	if dto.Scope == nil {
		if err := p("No --scope: every enabled task you can read is included, customer and personal memories too.\n"); err != nil {
			return err
		}
	} else if err := p("Scope %s (%s): %d memories (%d you cannot read were left out).\n",
		cmp(dto.Scope.Name, dto.Scope.ID), dto.Scope.source, dto.Scope.MemoryCount, dto.Scope.DroppedCount); err != nil {
		return err
	}
	for _, h := range dto.Hosts {
		where := "(nothing built)"
		if h.Artifact != nil {
			where = *h.Artifact
		}
		if err := p("\n%s → %s\n", h.Host, where); err != nil {
			return err
		}
		if h.Version != nil {
			if err := p("  version %s\n", *h.Version); err != nil {
				return err
			}
		}
		if h.Zip != nil {
			if err := p("  zip %s\n", *h.Zip); err != nil {
				return err
			}
		}
		if h.Failure != nil {
			if err := p("  ✗ %s\n", h.Failure.Message); err != nil {
				return err
			}
		}
		rows := len(h.Included) + len(h.Skipped) + len(h.Refused) + len(h.Failed) + len(h.NotForHost)
		if rows > 0 {
			included := "included"
			if dto.DryRun {
				included = "would include"
			}
			t := output.NewTable(w, "STATE", "SKILL", "DETAIL")
			for _, it := range h.Included {
				// The server's reason on a WRITE describes the file on disk
				// ("no installed skill exists"), which a bundle never has; --json
				// keeps it verbatim, the human table leaves it out.
				t.Row(included, cmp(it.Name, it.Node), "")
			}
			for _, it := range h.Skipped {
				t.Row("skipped", cmp(it.Name, it.Node), reasonText(it.Reasons))
			}
			for _, it := range h.Refused {
				t.Row("refused", cmp(it.Name, it.Node), reasonText(it.Reasons))
			}
			for _, it := range h.Failed {
				t.Row("failed", cmp(it.Name, it.Node), reasonText(it.Reasons))
			}
			for _, it := range h.NotForHost {
				t.Row("other host", cmp(it.Name, it.Node), "declared only for the other host")
			}
			if err := t.Flush(); err != nil {
				return err
			}
		} else if h.Failure == nil {
			if err := p("  no skills for this host\n"); err != nil {
				return err
			}
		}
		for _, fd := range h.Findings {
			if err := p("  %s  %s: %s (%s)\n", fd.Severity, cmp(fd.Name, fd.Node), fd.Message, fd.Rule); err != nil {
				return err
			}
		}
	}
	if len(dto.Unrecognized) > 0 {
		if err := p("\nNot included for any host: these nodes declare an exports key that names no known host.\n"); err != nil {
			return err
		}
		for _, u := range dto.Unrecognized {
			if err := p("  %s (%s): %s\n", cmp(u.Name, u.Node), u.Node, strings.Join(u.Keys, ", ")); err != nil {
				return err
			}
		}
	}
	for _, h := range dto.Hosts {
		if dto.DryRun || h.Artifact == nil || h.Failure != nil {
			continue
		}
		switch h.Host {
		case skilldoc.HostClaudeSkill:
			if err := p("\nInstall in Claude Code:\n  claude plugin marketplace add %s\n  claude plugin install %s@%s\n", *h.Artifact, dto.Name, dto.Name); err != nil {
				return err
			}
		case skilldoc.HostCodexSkill:
			if err := p("\nInstall for Codex: copy the skill folders in %s into ~/.agents/skills\n", *h.Artifact); err != nil {
				return err
			}
		}
	}
	return nil
}
