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
}

// WorkerConfig is one worker this host can launch.
type WorkerConfig struct {
	// Family is the model family it runs as here, e.g. claude or codex.
	Family string `yaml:"family"`
	// Launch is the argv that starts its agent in a worktree. The poller adds
	// HADRON_LAUNCH_GRANT to its environment and nothing else of Hadron's.
	Launch []string `yaml:"launch"`
	// End is an optional argv that prompts the running agent to end its own
	// session with a handoff (worker.end). It is never a kill.
	End []string `yaml:"end"`
	// EndWait bounds how long worker.end waits for the session to end
	// (default 15m).
	EndWait string `yaml:"end_wait"`
	// Worktrees are this worker's existing worktree name tokens, relative to
	// worktrees_root, beyond the ones this poller created.
	Worktrees []string `yaml:"worktrees"`
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
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing host config %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if c.StateFile == "" {
		c.StateFile = filepath.Join(dir, "hadron-host-state.json")
	} else if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(dir, c.StateFile)
	}
	if c.KeyFile != "" && !filepath.IsAbs(c.KeyFile) {
		c.KeyFile = filepath.Join(dir, c.KeyFile)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("host config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	var errs []string
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
		if !familyRE.MatchString(w.Family) {
			errs = append(errs, fmt.Sprintf("worker %q: family %q must match %s", ref, w.Family, familyRE))
		}
		if len(w.Launch) == 0 || strings.TrimSpace(w.Launch[0]) == "" {
			errs = append(errs, fmt.Sprintf("worker %q: launch must name a command", ref))
		}
		if w.EndWait != "" {
			if d, err := time.ParseDuration(w.EndWait); err != nil || d <= 0 {
				errs = append(errs, fmt.Sprintf("worker %q: end_wait %q is not a positive duration", ref, w.EndWait))
			}
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
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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
