package hostfacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/config"
	"go.yaml.in/yaml/v3"
)

// The accounts file (hadron-server#5210's note, settled): ~/.config/hadron/accounts.yaml in the CLI's config
// directory on Linux and macOS, mode 0600. It names the extra accounts a host can launch an agent under:
//
//	accounts:
//	  work:
//	    family: claude          # claude, codex, glm or kimi (required)
//	    program: /abs/path      # optional; the family's default program otherwise
//	    configDir: /abs/path    # optional; the tool's default account otherwise
//
// Accounts only: a workers, worktrees, repos or host-settings key is refused. `tool` and `version` are
// RESERVED names a newer hadron will use, so a file written for it is answered "needs a newer hadron", never
// "unknown key". An absent `tool` means the tool the family implies, so no file written today changes meaning
// when `tool` exists. Only names ever leave the machine. The file is read at the moment of use, never cached.

var (
	accountNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	programRE     = regexp.MustCompile("^[^ \t\r\n'\"\\\\$`;&|<>*?(){}\\[\\]!#~]+$")
	reservedKeys  = map[string]bool{"tool": true, "version": true}
	entryKeys     = map[string]bool{"family": true, "program": true, "configDir": true}
	knownFamilies = map[string]bool{"claude": true, "codex": true, "glm": true, "kimi": true}
)

// Entry is one named account.
type Entry struct {
	Family    string
	Program   string
	ConfigDir string
}

// Tool is the tool the entry's family runs: claude for claude, glm and kimi; codex for codex. A later `tool`
// key (hadron-cli#3227) overrides it; today it is only the family's.
func (e Entry) Tool() string {
	if e.Family == "codex" {
		return "codex"
	}
	return "claude"
}

// Accounts is the loaded file. An absent file is Present false with no entries.
type Accounts struct {
	Path    string
	Present bool
	Entries map[string]Entry
	Names   []string
}

// AccountsError is a refusal of the file. It names the file and, where it applies, the entry and key.
// NeedsNewer marks a reserved key: the file is not wrong, this hadron is too old for it.
type AccountsError struct {
	Path       string
	Entry      string
	Key        string
	Msg        string
	NeedsNewer bool
}

func (e *AccountsError) Error() string { return e.Path + ": " + e.Msg }

// AccountsPath is accounts.yaml in the CLI's config directory.
func AccountsPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "accounts.yaml"), nil
}

// LoadAccounts reads the file now. An absent file is no accounts and no error.
func LoadAccounts(path string) (Accounts, error) {
	out := Accounts{Path: path, Entries: map[string]Entry{}, Names: []string{}}
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, &AccountsError{Path: path, Msg: "cannot read the accounts file: " + err.Error()}
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return out, &AccountsError{Path: path, Msg: fmt.Sprintf("the accounts file is readable by group or others (mode %04o); run chmod 600 %s", st.Mode().Perm(), path)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out, &AccountsError{Path: path, Msg: "cannot read the accounts file: " + err.Error()}
	}
	out.Present = true
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return out, &AccountsError{Path: path, Msg: "the accounts file is not valid YAML: " + err.Error()}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return out, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return out, &AccountsError{Path: path, Msg: "the accounts file must be a mapping with one key, `accounts`"}
	}
	seenTop := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i].Value, root.Content[i+1]
		switch {
		case seenTop[key]:
			return out, &AccountsError{Path: path, Key: key, Msg: fmt.Sprintf("the key %q appears twice", key)}
		case key == "accounts":
			seenTop[key] = true
			if err := loadEntries(path, val, &out); err != nil {
				return out, err
			}
		case reservedKeys[key]:
			return out, newer(path, "", key)
		default:
			return out, &AccountsError{Path: path, Key: key, Msg: fmt.Sprintf("unknown key %q: this file holds accounts only, under `accounts` (no workers, worktrees, repos or host settings)", key)}
		}
	}
	sort.Strings(out.Names)
	return out, nil
}

func newer(path, entry, key string) *AccountsError {
	where := fmt.Sprintf("the key %q", key)
	if entry != "" {
		where = fmt.Sprintf("entry %q: the key %q", entry, key)
	}
	return &AccountsError{Path: path, Entry: entry, Key: key, NeedsNewer: true,
		Msg: where + " needs a newer hadron (this version does not know it yet): upgrade hadron"}
}

func loadEntries(path string, n *yaml.Node, out *Accounts) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return &AccountsError{Path: path, Key: "accounts", Msg: "`accounts` must be a mapping of account name to its entry"}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name, val := n.Content[i].Value, n.Content[i+1]
		if !accountNameRE.MatchString(name) {
			return &AccountsError{Path: path, Entry: name, Msg: fmt.Sprintf("the account name %q is not allowed: lowercase letters, digits, _ and -, starting with a letter or digit, at most 64 characters", name)}
		}
		if _, dup := out.Entries[name]; dup {
			return &AccountsError{Path: path, Entry: name, Msg: fmt.Sprintf("the account %q appears twice", name)}
		}
		e, err := loadEntry(path, name, val)
		if err != nil {
			return err
		}
		out.Entries[name] = e
		out.Names = append(out.Names, name)
	}
	return nil
}

func loadEntry(path, name string, n *yaml.Node) (Entry, error) {
	if n.Kind != yaml.MappingNode {
		return Entry{}, &AccountsError{Path: path, Entry: name, Msg: fmt.Sprintf("entry %q must be a mapping of family, program and configDir", name)}
	}
	vals := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, v := n.Content[i].Value, n.Content[i+1]
		switch {
		case reservedKeys[key]:
			return Entry{}, newer(path, name, key)
		case !entryKeys[key]:
			return Entry{}, &AccountsError{Path: path, Entry: name, Key: key, Msg: fmt.Sprintf("entry %q: unknown key %q (an entry takes family, program and configDir)", name, key)}
		}
		if _, dup := vals[key]; dup {
			return Entry{}, &AccountsError{Path: path, Entry: name, Key: key, Msg: fmt.Sprintf("entry %q: the key %q appears twice", name, key)}
		}
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" && (key == "program" || key == "configDir") {
			vals[key] = "" // an optional key left empty is an absent key
			continue
		}
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return Entry{}, &AccountsError{Path: path, Entry: name, Key: key, Msg: fmt.Sprintf("entry %q: %s must be a string", name, key)}
		}
		vals[key] = v.Value
	}
	fam := CanonicalFamily(vals["family"])
	if vals["family"] == "" {
		return Entry{}, &AccountsError{Path: path, Entry: name, Key: "family", Msg: fmt.Sprintf("entry %q: family is required (claude, codex, glm or kimi)", name)}
	}
	if !knownFamilies[fam] {
		return Entry{}, &AccountsError{Path: path, Entry: name, Key: "family", Msg: fmt.Sprintf("entry %q: family %q is not one of claude, codex, glm or kimi", name, vals["family"])}
	}
	e := Entry{Family: fam, Program: vals["program"], ConfigDir: vals["configDir"]}
	if e.Program != "" && (!filepath.IsAbs(e.Program) || !programRE.MatchString(e.Program)) {
		return Entry{}, &AccountsError{Path: path, Entry: name, Key: "program", Msg: fmt.Sprintf("entry %q: program %q must be an absolute path with no spaces or shell characters", name, e.Program)}
	}
	if e.ConfigDir != "" && !filepath.IsAbs(e.ConfigDir) {
		return Entry{}, &AccountsError{Path: path, Entry: name, Key: "configDir", Msg: fmt.Sprintf("entry %q: configDir %q must be an absolute path", name, e.ConfigDir)}
	}
	if strings.ContainsAny(e.ConfigDir, "\x00\n") {
		return Entry{}, &AccountsError{Path: path, Entry: name, Key: "configDir", Msg: fmt.Sprintf("entry %q: configDir is not a path", name)}
	}
	return e, nil
}
