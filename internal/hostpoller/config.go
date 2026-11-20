// Package hostpoller is the host side of Hadron's host operations
// (hadron-cli#831, cor:agt:070): a poller that registers the workers this
// host can launch, collects the operations issued to it, carries out only
// the closed set of five, and reports every outcome.
//
// It talks to the server through a narrow Server interface and to the host
// through a Host interface, so the loop and every operation's rules are
// tested without a server, git or a real agent.
package hostpoller

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// The host-local configuration. Hadron never sees it: the launch command and
// any AI-plan login are the host's own (Holger #10777, cor:agt:070 Out of
// Scope), and the host key is read from a file named here — never from an
// environment variable, which a same-user agent could read from the
// poller's /proc entry.
type Config struct {
	// sourcePath and sourceHash name the file this Config was loaded from and
	// the content it held; a poller compares them to the file each poll.
	// Empty for a Config built in code. A Config is a snapshot: once loaded
	// it is never changed, and a reload (cli#2623) builds a new one and swaps
	// the poller's pointer, so a pass that took one Config sees one
	// consistent set of workers however long it runs.
	sourcePath string
	sourceHash [sha256.Size]byte
	// Capabilities is an explicit full declaration at every refresh, never inferred.
	Capabilities []string `yaml:"capabilities"`
	// Server is the Hadron server URL; empty means the CLI's usual resolution.
	Server string `yaml:"server"`
	// App is the team App the host serves (hrn:app:<root>:<slug>, or an id).
	App string `yaml:"app"`
	// Label names the host to people; empty means the hostname.
	Label string `yaml:"label"`
	// PollIntervalSeconds is how often the host checks in (1 to 86400).
	PollIntervalSeconds int `yaml:"poll_interval_seconds"`
	// KeyFile holds the App key carrying host:operate for this host
	// (required; mode 0600 or tighter). Relative paths resolve against the
	// configuration's directory.
	KeyFile string `yaml:"key_file"`
	// WorktreesRoot is the directory every worktree name token is relative to.
	WorktreesRoot string `yaml:"worktrees_root"`
	// StateFile records the worktrees this poller created and any it must
	// keep. Empty means hadron-host-state.json beside the config.
	StateFile string `yaml:"state_file"`
	// Repos maps a repo token (as worktree.create names it, e.g.
	// hadron-memory/hadron-cli) to the local clone worktrees are added from.
	Repos map[string]string `yaml:"repos"`
	// Workers maps a worker reference (its name in the App, URN or id) to
	// how this host runs it.
	Workers map[string]WorkerConfig `yaml:"workers"`
	// Delivery turns on chat delivery into this host's Orca terminals
	// (cli#907). Absent, the poller carries out host operations only.
	Delivery DeliveryConfig `yaml:"delivery"`
	// Accounts maps a launcher profile's account name to this host's
	// launcher and Codex home (hadron-cli#2058 C3, G1).
	Accounts map[string]AccountProfile `yaml:"account_profiles"`
	// OrcaLaunch configures launches through Orca (launch_via: orca).
	OrcaLaunch OrcaLaunchConfig `yaml:"orca_launch"`
	// ResetContext reserves the C3 reader path for an explicitly declared reset capability.
	ResetContext ResetContextConfig `yaml:"reset_context"`
}

// ResetContextConfig neither activates resets nor proves a terminal/process binding.
type ResetContextConfig struct {
	OrcaBin string `yaml:"orca_bin"`
}

// ResetContextEnabled reports the operator's explicit capability declaration.
func (c *Config) ResetContextEnabled() bool {
	for _, capability := range c.Capabilities {
		if capability == CapabilityResetContext {
			return true
		}
	}
	return false
}

// OrcaLaunchConfig is what an Orca launch needs from the host.
type OrcaLaunchConfig struct {
	// CleanupDeadTabs opts into C5's conservative post-relaunch tab cleanup.
	// Default false until the operator validates the live close path.
	CleanupDeadTabs bool `yaml:"cleanup_dead_tabs"`
	// OrcaBin is the absolute Orca CLI path.
	OrcaBin string `yaml:"orca_bin"`
	// Group is refused (cli#2121): it ran the command under `sg <group>`,
	// and the launch proof reads the process through /proc, which an
	// sg-started process did not allow on the tested deployment (orca1, even
	// for a group the poller already held). The proof does not support it.
	// Kept only so the config names the reason instead of "unknown key".
	Group string `yaml:"group"`
}

// The launch_via values.
const (
	LaunchViaExec = "exec"
	LaunchViaOrca = "orca"
)

// launchesViaOrca is the one lookup for "this worker starts through Orca".
func (w WorkerConfig) launchesViaOrca() bool { return w.LaunchVia == LaunchViaOrca }

// UsesOrcaLaunch reports whether any worker here starts through Orca.
func (c *Config) UsesOrcaLaunch() bool {
	for _, w := range c.Workers {
		if w.launchesViaOrca() {
			return true
		}
	}
	return false
}

// validateOrcaWorker checks an Orca-launched worker's prerequisites at load,
// so a start never finds them missing: a profile whose argv builds, an account
// that exists, an absolute disk temp and Orca binary, and a profile family
// that is the worker's own launch identity.
func validateOrcaWorker(c *Config, ref string, w WorkerConfig) []string {
	var errs []string
	if w.Profile == nil {
		return append(errs, fmt.Sprintf("worker %q: launch_via orca needs a launcher_profile", ref))
	}
	a, ok := c.Accounts[w.Profile.Account]
	if !ok {
		errs = append(errs, fmt.Sprintf("worker %q: launcher_profile account %q is not in account_profiles", ref, w.Profile.Account))
	} else if _, err := LaunchArgv(*w.Profile, a); err != nil {
		errs = append(errs, fmt.Sprintf("worker %q: %v", ref, err))
	}
	pf, wf := CanonicalFamily(w.Profile.Family), CanonicalFamily(w.Family)
	if !sameLaunchFamily(pf, wf) {
		errs = append(errs, fmt.Sprintf("worker %q: launcher_profile family %q is not the worker's family %q", ref, w.Profile.Family, w.Family))
	}
	if w.DiskTmp == "" || !filepath.IsAbs(w.DiskTmp) {
		errs = append(errs, fmt.Sprintf("worker %q: launch_via orca needs an absolute disk_tmp", ref))
	}
	if !filepath.IsAbs(c.OrcaLaunch.OrcaBin) {
		errs = append(errs, "orca_launch.orca_bin must be an absolute path when a worker launches via orca")
	}
	for _, f := range a.Families {
		if cf := CanonicalFamily(f); !claudeFamily[cf] && cf != "codex" {
			errs = append(errs, fmt.Sprintf("account profile %q: families lists %q, which has no launch template", w.Profile.Account, f))
		}
	}
	if a.CodexHome != "" && !filepath.IsAbs(a.CodexHome) {
		errs = append(errs, fmt.Sprintf("account profile %q: codex_home must be an absolute path", w.Profile.Account))
	}
	return errs
}

// sameLaunchFamily: a profile fits a worker of its own family, and any
// Claude-family profile fits a Claude-family worker (one launcher, Claude Code).
func sameLaunchFamily(a, b string) bool { return a == b || claudeFamily[a] && claudeFamily[b] }

// DeliveryConfig is chat delivery's part of a host configuration.
type DeliveryConfig struct {
	// Enabled turns delivery on. The host key then also needs
	// attention:deliver; without it delivery switches off until a re-check succeeds.
	Enabled bool `yaml:"enabled"`
	// AckMixed asserts that the server carries A-prime (#2072): mixed wake
	// acknowledgment records ticks/tasks without advancing any chat mark.
	AckMixed bool `yaml:"ack_mixed"`
	// OrcaBin is the absolute path of the orca CLI.
	OrcaBin string `yaml:"orca_bin"`
}

// WorkerConfig is one worker this host can launch.
type WorkerConfig struct {
	// Family is the model family it runs as here, e.g. claude or codex.
	Family string `yaml:"family"`
	// Launch is the argv that starts its agent in a worktree. The poller adds
	// HADRON_LAUNCH_GRANT to its environment and nothing else of Hadron's.
	Launch []string `yaml:"launch"`
	// LaunchVia selects how worker.start launches (hadron-cli#2058 C3):
	// "exec" or empty (the default: Launch, as the poller's own child) or
	// "orca" (through an Orca terminal, from Profile; G3: the agent is a
	// child of the Orca daemon in Orca's capped per-terminal scope).
	// TRANSITIONAL: the exec path goes with worker-supervise (C4).
	LaunchVia string `yaml:"launch_via"`
	// Profile is the typed launcher profile an Orca launch builds its argv
	// from (G1). TRANSITIONAL: S1b's server fields replace it.
	Profile *LauncherProfile `yaml:"launcher_profile"`
	// DiskTmp is an absolute directory an Orca launch sets as TMPDIR,
	// CLAUDE_CODE_TMPDIR and the NODE_COMPILE_CACHE parent, so the agent's
	// temp is on disk rather than the RAM /tmp (the 2026-11-12 OOM).
	DiskTmp string `yaml:"disk_tmp"`
	// End is an optional argv that prompts the running agent to end its own
	// session with a handoff (worker.end). It is never a kill.
	End []string `yaml:"end"`
	// EndWait bounds how long worker.end waits for the session to end
	// (default 15m).
	EndWait string `yaml:"end_wait"`
	// StopOnEnd says whether worker.end also stops the worker's agent
	// process on this host (hadron-cli#2057 C2a): never (the default: the
	// end only prompts, as before), manager (stop only when a person who
	// manages the App issued the end, never the platform's own or a worker's),
	// or always (a short-lived worker: every end stops it).
	StopOnEnd string `yaml:"stop_on_end"`
	// ResetOnStart makes worker.start bring the worker's existing worktree back to
	// a clean base before it launches the agent (hadron-cli#2057 C2b; G6: part of
	// starting, no new operation type): a dirty tree or a HEAD on no remote branch
	// refuses the start, a worker without Git starts with no reset. Default off.
	// TRANSITIONAL like stop_on_end: it is meant for a short-lived worker, and
	// goes with host.yaml when Agent.lifecycle reaches the host (S3).
	ResetOnStart bool `yaml:"reset_on_start"`
	// StopWait bounds how long a stopping worker.end gives a delivered end
	// prompt before it stops the process (default 60s). With no end prompt,
	// or one that failed, there is nothing to wait for and the stop is
	// immediate.
	StopWait string `yaml:"stop_wait"`
	// Recover is an optional argv that hands a relaunched agent its recovery
	// prompt after a worker.reset-context (#1043), the way End prompts an
	// end. It gets HADRON_SESSION_ID, HADRON_WORKER_ID and
	// HADRON_RECOVERY_NODE (the checkpoint node's reference): ids only. A
	// worker without it is never reset on this host.
	Recover []string `yaml:"recover"`
	// Worktrees are this worker's existing worktree name tokens, relative to
	// worktrees_root, beyond the ones this poller created. A plain directory
	// (no .git) listed here is a recipient too, found by the terminal handle
	// it records in .hadron-team/terminal.json (cli#1989).
	Worktrees []string `yaml:"worktrees"`
	// TUI is the agent interface its terminal shows, claude or codex, when
	// the model family does not say (a glm worker runs in Claude Code).
	// Delivery holds a terminal that shows anything else.
	TUI string `yaml:"tui"`
	// CodexHome and ClaudeConfigDir are where THIS worker's launcher writes its
	// Codex rollouts (<codex_home>/sessions) and Claude transcripts
	// (<claude_config_dir>/projects), when that is not the health sweep's own
	// CODEX_HOME / CLAUDE_CONFIG_DIR / ~ (hadron-cli#2063: Orca gives each Codex
	// worker an account home). Only the context-size measurement reads them.
	CodexHome       string `yaml:"codex_home"`
	ClaudeConfigDir string `yaml:"claude_config_dir"`
}

// familyAliases are the provider names the server folds into the family a
// host actually runs (hadron-server#2217): one identity per family, so a host
// configured `openai` and an operation naming `codex` are the same family.
var familyAliases = map[string]string{"anthropic": "claude", "openai": "codex"}

// CanonicalFamily is the server's identity for an EXPLICIT family: trimmed,
// lowercased, and a provider alias folded to its family. Every other token is
// itself, so a custom family compares only to itself. It never infers a
// family from a title or a model and does not validate: an empty or malformed
// result is not a launch identity and callers must refuse it (familyRE).
func CanonicalFamily(s string) string {
	f := strings.ToLower(strings.TrimSpace(s))
	if canon, ok := familyAliases[f]; ok {
		return canon
	}
	return f
}

// SameFamily reports whether two explicit families are one launch identity.
// An empty or malformed family is never one, even against itself.
func SameFamily(a, b string) bool {
	ca := CanonicalFamily(a)
	return familyRE.MatchString(ca) && ca == CanonicalFamily(b)
}

// tui is the interface delivery expects: the configured one, else a family
// that names its own interface, else "" (the screen and Orca must agree).
func (w WorkerConfig) tui() string {
	if w.TUI != "" {
		return w.TUI
	}
	if f := CanonicalFamily(w.Family); f == "claude" || f == "codex" {
		return f
	}
	return ""
}

const (
	defaultPollInterval = 30
	defaultEndWait      = 15 * time.Minute
	// HostKeyEnv is never read as a key source (a same-user agent could
	// read it from the poller's environment); it is scrubbed from launched
	// agents in case the operator's shell carries one.
	HostKeyEnv = "HADRON_HOST_KEY"
)

// The server's own token rules (hostOperations/types.ts), applied here too
// so a bad configuration fails at load, not as a refused registration.
var (
	nameTokenRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,255}$`)
	familyRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// ValidNameToken reports whether s is a host name token the server accepts.
func ValidNameToken(s string) bool {
	return nameTokenRE.MatchString(s) && !strings.Contains(s, "..") &&
		!strings.Contains(s, "//") && !strings.HasSuffix(s, "/")
}

// LoadConfig reads and validates a host configuration file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading host config: %w", err)
	}
	return loadConfigBytes(path, raw)
}

// loadConfigBytes is LoadConfig for content already read, so a reload judges
// exactly the bytes it hashed.
func loadConfigBytes(path string, raw []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing host config %s: %w", path, err)
	}
	// Inspect the source node as well: yaml.v3 accepts legacy bool strings and
	// treats null as an omitted bool. Neither is an operator's explicit assertion.
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if err := yamlField(&document, "delivery", map[*yaml.Node]bool{}, func(delivery *yaml.Node) error {
		return yamlField(delivery, "ack_mixed", map[*yaml.Node]bool{}, func(value *yaml.Node) error {
			for value.Kind == yaml.AliasNode {
				value = value.Alias
			}
			if value.Tag != "!!bool" {
				return fmt.Errorf("delivery.ack_mixed at line %d must be a boolean", value.Line)
			}
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("host config %s: %w", path, err)
	}

	// The written path, absolute but not cleaned (AbsWritten): the directory
	// state and key files are joined to is the one the kernel reached, so a
	// ".." after a symlink must survive (cli#2674). Entries are joined with the
	// native separator, not filepath.Join, which would clean the ".." away.
	path = AbsWritten(path)
	dir := writtenDir(path)
	if c.StateFile == "" {
		c.StateFile = joinWritten(dir, "hadron-host-state.json", filepath.Separator)
	} else if !filepath.IsAbs(c.StateFile) {
		c.StateFile = joinWritten(dir, c.StateFile, filepath.Separator)
	}
	if c.KeyFile != "" && !filepath.IsAbs(c.KeyFile) {
		c.KeyFile = joinWritten(dir, c.KeyFile, filepath.Separator)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("host config %s: %w", path, err)
	}
	c.sourcePath, c.sourceHash = path, sha256.Sum256(raw)
	return &c, nil
}

// yamlField visits a field through ordinary YAML aliases and merge mappings,
// without replacing the normal KnownFields decoder or duplicating struct tags.
func yamlField(n *yaml.Node, field string, seen map[*yaml.Node]bool, visit func(*yaml.Node) error) error {
	if n == nil || seen[n] {
		return nil
	}
	seen[n] = true
	switch n.Kind {
	case yaml.AliasNode:
		return yamlField(n.Alias, field, seen, visit)
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range n.Content {
			if err := yamlField(child, field, seen, visit); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key.Value == field {
				if err := visit(value); err != nil {
					return err
				}
			}
			if key.Tag == "!!merge" {
				if err := yamlField(value, field, seen, visit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CodeOrcaGroupUnprovable leads the config refusal of orca_launch.group
// (cli#2121): a process started through sg did not allow the /proc read the
// launch proof needs, as observed on orca1 (even for a group the poller
// already held), so the proof can't pass there. Reversible if a proof channel
// that works under sg turns up, or if another deployment shows otherwise.
const CodeOrcaGroupUnprovable = "ORCA_GROUP_UNPROVABLE"

func (c *Config) validate() error {
	var errs []string
	if c.OrcaLaunch.Group != "" {
		errs = append(errs, CodeOrcaGroupUnprovable+": orca_launch.group is not supported: the launch proves its process through /proc, which a process started through sg did not allow on the tested deployment (orca1, even for a group the poller already held) (cli#2121); remove it, and start the Orca daemon with the groups the agent needs")
	}
	if strings.TrimSpace(c.App) == "" {
		errs = append(errs, "app is required")
	}
	if c.Label == "" {
		h, _ := os.Hostname()
		c.Label = h
	}
	if n := len([]rune(c.Label)); n < 1 || n > 128 {
		errs = append(errs, "label must be 1 to 128 characters")
	}
	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = defaultPollInterval
	}
	if c.PollIntervalSeconds < 1 || c.PollIntervalSeconds > 86400 {
		errs = append(errs, "poll_interval_seconds must be 1 to 86400")
	}
	if !filepath.IsAbs(c.WorktreesRoot) {
		errs = append(errs, "worktrees_root must be an absolute path")
	}
	for token, p := range c.Repos {
		if !ValidNameToken(token) {
			errs = append(errs, fmt.Sprintf("repo %q is not a valid name token", token))
		}
		if !filepath.IsAbs(p) {
			errs = append(errs, fmt.Sprintf("repo %q: path must be absolute", token))
		}
	}
	if len(c.Workers) == 0 {
		errs = append(errs, "workers must name at least one worker")
	}
	if len(c.Workers) > 500 {
		errs = append(errs, "at most 500 workers")
	}
	owner := map[string]string{}
	for _, ref := range c.WorkerRefs() {
		w := c.Workers[ref]
		for _, t := range w.Worktrees {
			if o, taken := owner[t]; taken {
				errs = append(errs, fmt.Sprintf("worktree %q is listed for both %q and %q: a worktree belongs to one worker", t, o, ref))
			}
			owner[t] = ref
		}
		for k, p := range map[string]string{"codex_home": w.CodexHome, "claude_config_dir": w.ClaudeConfigDir} {
			if p != "" && !filepath.IsAbs(p) {
				errs = append(errs, fmt.Sprintf("worker %q: %s must be an absolute path", ref, k))
			}
		}
		if !familyRE.MatchString(w.Family) {
			errs = append(errs, fmt.Sprintf("worker %q: family %q must match %s", ref, w.Family, familyRE))
		}
		// An Orca-launched worker's argv comes from its launcher profile; only
		// the exec path needs a configured command.
		if !w.launchesViaOrca() && (len(w.Launch) == 0 || strings.TrimSpace(w.Launch[0]) == "") {
			errs = append(errs, fmt.Sprintf("worker %q: launch must name a command", ref))
		}
		if w.EndWait != "" {
			if d, err := time.ParseDuration(w.EndWait); err != nil || d <= 0 {
				errs = append(errs, fmt.Sprintf("worker %q: end_wait %q is not a positive duration", ref, w.EndWait))
			}
		}
		switch w.LaunchVia {
		case "", LaunchViaExec:
		case LaunchViaOrca:
			errs = append(errs, validateOrcaWorker(c, ref, w)...)
		default:
			errs = append(errs, fmt.Sprintf("worker %q: launch_via %q must be %s or %s", ref, w.LaunchVia, LaunchViaExec, LaunchViaOrca))
		}
		switch w.StopOnEnd {
		case "", StopOnEndNever, StopOnEndManager, StopOnEndAlways:
		default:
			errs = append(errs, fmt.Sprintf("worker %q: stop_on_end %q must be %s, %s or %s", ref, w.StopOnEnd, StopOnEndNever, StopOnEndManager, StopOnEndAlways))
		}
		if w.StopWait != "" {
			if d, err := time.ParseDuration(w.StopWait); err != nil || d <= 0 {
				errs = append(errs, fmt.Sprintf("worker %q: stop_wait %q is not a positive duration", ref, w.StopWait))
			}
		}
		if w.TUI != "" && w.TUI != "claude" && w.TUI != "codex" {
			errs = append(errs, fmt.Sprintf("worker %q: tui %q must be claude or codex", ref, w.TUI))
		}
		if len(w.Worktrees) > 32 {
			errs = append(errs, fmt.Sprintf("worker %q: at most 32 worktrees", ref))
		}
		for _, t := range w.Worktrees {
			if !ValidNameToken(t) {
				errs = append(errs, fmt.Sprintf("worker %q: worktree %q is not a valid name token", ref, t))
			}
		}
	}
	if c.Delivery.Enabled {
		switch {
		case c.Delivery.OrcaBin == "":
			errs = append(errs, "delivery.orca_bin is required when delivery is enabled")
		case !filepath.IsAbs(c.Delivery.OrcaBin):
			errs = append(errs, "delivery.orca_bin must be an absolute path")
		}
	}
	if c.ResetContextEnabled() && !filepath.IsAbs(c.ResetContext.OrcaBin) {
		errs = append(errs, "reset_context.orca_bin must be an absolute path when worker.reset-context is declared")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// DeliveryStateDir holds delivery's records and per-worker locks: beside the
// state file, where C2's --orca-host-config finds the same locks. It is the
// kernel's directory for the state file (resolvedStateDir), not the cleaned
// written one, so two spellings of one config select the same locks.
func (c *Config) DeliveryStateDir() string { return resolvedStateDir(c.StateFile) }

// Worker is the configuration of one worker reference.
func (c *Config) Worker(ref string) (WorkerConfig, bool) {
	w, ok := c.Workers[ref]
	return w, ok
}

// Repo is the local clone for a repo token.
func (c *Config) Repo(token string) (string, bool) {
	p, ok := c.Repos[token]
	return p, ok
}

// Account is a launcher account profile by name.
func (c *Config) Account(name string) (AccountProfile, bool) {
	a, ok := c.Accounts[name]
	return a, ok
}

// WorkerRefs lists the configured worker references in a stable order.
func (c *Config) WorkerRefs() []string {
	refs := make([]string, 0, len(c.Workers))
	for r := range c.Workers {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

// EndWait is the bounded wait for one worker's worker.end.
func (w WorkerConfig) endWait() time.Duration {
	if d, err := time.ParseDuration(w.EndWait); err == nil && d > 0 {
		return d
	}
	return defaultEndWait
}

// The stop_on_end values (hadron-cli#2057 C2a).
const (
	StopOnEndNever   = "never"
	StopOnEndManager = "manager"
	StopOnEndAlways  = "always"
)

// defaultStopWait is how long a stopping worker.end gives a delivered end
// prompt (#2057: 60 s, so a short-lived worker's process is gone well inside
// the epic's two minutes).
const defaultStopWait = 60 * time.Second

// stopsOnEnd reports whether a worker.end from this issuer also stops the
// worker's process. Anything but an explicit always or manager is never, and
// manager needs a NAMED issuer other than the platform: an operation whose
// issuer did not reach the host stops nothing.
func (w WorkerConfig) stopsOnEnd(issuedBy string) bool {
	switch w.StopOnEnd {
	case StopOnEndAlways:
		return true
	case StopOnEndManager:
		// A WORKER issuer is a bound reports-to worker, which never manages
		// the App (cor:agt:050:09): only a person's end is a manager's.
		return issuedBy == IssuedByUser
	}
	return false
}

// resetsOnStart is the one lookup for "this worker's tree is reset at start":
// the seam Agent.lifecycle replaces (a SHORT_LIVED agent resets), as stopsOnEnd
// is for the stop.
func (w WorkerConfig) resetsOnStart() bool { return w.ResetOnStart }

// stopWait is the bounded wait a stopping worker.end gives its end prompt.
func (w WorkerConfig) stopWait() time.Duration {
	if d, err := time.ParseDuration(w.StopWait); err == nil && d > 0 {
		return d
	}
	return defaultStopWait
}

// ReadHostKey returns the host:operate key from key_file. Never from
// HADRON_TOKEN or the token store (the holder's own credential, which the
// launched agents bind on), and never from an environment variable.
//
// The file must not be readable by group or others. That is the part of
// key custody the poller CAN check. The part it cannot: an agent running as
// the same OS user as the poller can read any file the poller can. Keeping
// the key from a launched agent's environment, argv and files is the
// poller's; keeping it from the agent's OS user is the deployment's — run
// the poller as its own user, with key_file readable by that user only.
func (c *Config) ReadHostKey() (string, error) {
	if c.KeyFile == "" {
		return "", fmt.Errorf("no host key: set key_file in the host config (an App key with host:operate, mode 0600)")
	}
	info, err := os.Stat(c.KeyFile)
	if err != nil {
		return "", fmt.Errorf("reading host key file: %w", err)
	}
	if keyFileTooOpen(info) {
		return "", fmt.Errorf("host key file %s is readable by group or others (mode %o): chmod 600 it", c.KeyFile, info.Mode().Perm())
	}
	raw, err := os.ReadFile(c.KeyFile)
	if err != nil {
		return "", fmt.Errorf("reading host key file: %w", err)
	}
	k := strings.TrimSpace(string(raw))
	if k == "" {
		return "", fmt.Errorf("host key file %s is empty", c.KeyFile)
	}
	return k, nil
}

// WorktreePath is the absolute path of a worktree name token.
func (c *Config) WorktreePath(token string) string {
	return filepath.Join(c.WorktreesRoot, filepath.FromSlash(token))
}

// CheckWorktreeAliases refuses two workers whose worktrees, configured or
// created by this poller, name one directory under different tokens
// (cli#906). validate already refuses one token listed twice; a symlink
// gives one directory a second token. A tree that does not exist yet is
// compared by its cleaned path only. One worker's own aliases are allowed.
func (c *Config) CheckWorktreeAliases(s *State) error {
	type claim struct{ ref, token string }
	owner := map[string]claim{}
	var errs []string
	for _, ref := range c.WorkerRefs() {
		w, _ := c.Worker(ref)
		for _, token := range s.worktreesFor(ref, w.Worktrees) {
			dir := filepath.Clean(c.WorktreePath(token))
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				dir = real
			}
			if o, taken := owner[dir]; taken && o.ref != ref {
				errs = append(errs, fmt.Sprintf("worktrees %q (%s) and %q (%s) are one directory: a worktree belongs to one worker", o.token, o.ref, token, ref))
				continue
			}
			owner[dir] = claim{ref, token}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// joinWritten appends name to dir with sep, never cleaning. The separator is
// the native one so an extended-length Windows path (\\?\C:\cfg), which the
// OS does not convert slashes in, stays usable (cli#2674).
//
// A dir that already ends in a separator (a root) is not doubled, and slashes
// inside name become sep, so a relative "keys/host.key" stays valid under an
// extended-length prefix. Nothing is cleaned.
func joinWritten(dir, name string, sep rune) string {
	if sep != '/' {
		name = strings.ReplaceAll(name, "/", string(sep))
	}
	if dir != "" && dir[len(dir)-1] == byte(sep) {
		return dir + name
	}
	return dir + string(sep) + name
}

// AbsWritten makes path absolute without cleaning it. It returns the path as
// given, still relative, if the working directory cannot be read (callers that
// need an absolute path must check), and native Windows resolution of rooted
// or drive-relative forms may clean parent segments. ".." after a symlink goes
// to the link target's parent, not the written path's lexical parent, so the
// stored path must keep it as written: the reload read, the trust walk and an
// installed unit then all follow the kernel, not filepath.Clean (cli#2668,
// cli#2674). An absolute path returns as given; the rest goes through
// nonAbsoluteConfigPath (cli#2700).
func AbsWritten(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return nonAbsoluteConfigPath(
		path, filepath.VolumeName, os.IsPathSeparator, os.Getwd, filepath.Abs,
	)
}

// nonAbsoluteConfigPath preserves the written form of an ordinary relative
// path. Classify inside this tested helper using native functions; callers
// cannot replace the classification with precomputed empty/false values.
// Function parameters let Windows-shaped cases run on other test hosts.
func nonAbsoluteConfigPath(path string, volumeName func(string) string, isSeparator func(uint8) bool, getwd func() (string, error), abs func(string) (string, error)) string {
	if volumeName(path) != "" || len(path) > 0 && isSeparator(path[0]) {
		if resolved, err := abs(path); err == nil {
			return resolved
		}
		return path
	}
	if wd, err := getwd(); err == nil {
		return wd + "/" + path
	}
	return path
}

// writtenDir is the directory part of an absolute written path, cut at its
// last separator without cleaning: "root/link/../host.yaml" gives
// "root/link/..", which the kernel resolves to the directory it loaded the file
// from.
func writtenDir(path string) string { return writtenDirSep(path, filepath.Separator) }

// writtenDirSep is writtenDir for a given native separator, so the Windows
// root forms can be pinned on any host.
func writtenDirSep(path string, sep rune) string {
	i := strings.LastIndexByte(path, '/')
	if j := strings.LastIndexByte(path, byte(sep)); j > i {
		i = j
	}
	if i < 0 {
		return "."
	}
	if i == 0 { // a root-level file: the parent is the root, never ""
		return path[:1]
	}
	// A drive root, plain or extended-length ("C:\x", "\\?\C:\x"): the parent
	// keeps its separator, as filepath.Dir does.
	if sep == '\\' && path[i-1] == ':' {
		return path[:i+1]
	}
	return path[:i]
}

// resolvedStateDir is the directory the kernel puts the state file in: the
// written parent with its symlinks followed, so the two spellings of one
// config ("root/link/../host.yaml" and "root/real/host.yaml") name one
// directory. A parent that does not exist yet is resolved at its deepest
// existing ancestor, the missing tail cleaned lexically (a missing name cannot
// be a symlink). Anything unresolvable falls back to the lexical parent.
func resolvedStateDir(stateFile string) string {
	cur := writtenDir(stateFile)
	var tail []string
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r
		}
		b := filepath.Base(cur)
		if b == ".." || b == string(filepath.Separator) {
			return filepath.Dir(stateFile)
		}
		up := writtenDir(cur)
		if up == cur {
			return filepath.Dir(stateFile)
		}
		if b != "." { // a "." names the directory it is in: no step, no tail entry
			tail = append(tail, b)
		}
		cur = up
	}
}
