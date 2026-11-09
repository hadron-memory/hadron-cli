package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// configSetDTO is the --json shape of `chat config set` (#477). Only the keys
// it wrote are listed; the file's other content is never reported.
type configSetDTO struct {
	Path    string          `json:"path"`
	Created bool            `json:"created"`
	Backup  *string         `json:"backup"`
	Set     []configSetItem `json:"set"`
}

type configSetItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func newCmdConfigSet(f *cmdutil.Factory) *cobra.Command {
	var node, memory, messagesLoc string
	cmd := &cobra.Command{
		Use:   "set (--node REF | -m MEMORY --messages-loc LOC)",
		Short: "Write the chat coordinates into the nearest .hadron/config.json",
		Long: `Write the chat coordinates "chat read" and "chat post" resolve from the project
config: --node (chat.node), or the two-field form -m/--memory (chat.memory)
and/or --messages-loc (chat.messagesLoc). --node cannot be combined with the
two-field flags.

It edits ONLY the keys you name. Every other key in the file — appKey and any
other secret included — is kept byte for byte, in its original order, and is
never printed. It writes the nearest .hadron/config.json found from here
(the file "chat config show" reports); if there is none, it creates
./.hadron/config.json. The change is atomic (temp file + rename), and an
existing file is first copied to config.json.bak.

It refuses, writing nothing, when the file is not valid JSON, is not an object,
has a "chat" that is not an object, has a duplicate key, is a symlink, or when
a value is blank. There is no flag that sets handle, identity or role: they do
not decide who posts, and this command cannot set them. It never posts and
never talks to the server. Nothing is written at bind time or by any other
command.`,
		Example: `  hadron chat config set --node hrn:node:acme.com:tc:chats:api:messages
  hadron chat config set -m hrn:mem:acme.com:tc --messages-loc chats:api:messages --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sets, err := configSetUpdates(cmd, node, memory, messagesLoc)
			if err != nil {
				return err
			}
			dto, err := applyConfigSet(sets)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verb := "updated"
				if dto.Created {
					verb = "created"
				}
				fmt.Fprintf(w, "%s %s\n", verb, dto.Path)
				for _, s := range dto.Set {
					fmt.Fprintf(w, "  chat.%s = %s\n", s.Key, s.Value)
				}
				if dto.Backup != nil {
					fmt.Fprintf(w, "backup: %s\n", *dto.Backup)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "message-parent node URN (hrn:node:<root>:<slug>:<loc>) to store as chat.node")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "chat memory to store as chat.memory")
	cmd.Flags().StringVar(&messagesLoc, "messages-loc", "", "message-parent loc prefix to store as chat.messagesLoc")
	cmd.MarkFlagsMutuallyExclusive("node", "memory")
	cmd.MarkFlagsMutuallyExclusive("node", "messages-loc")
	return cmd
}

// configSetUpdates turns the supplied flags into the (key, value) writes. A
// flag that was supplied empty is refused rather than read as "clear" or
// "absent"; no flag at all is a usage error.
func configSetUpdates(cmd *cobra.Command, node, memory, messagesLoc string) ([]configSetItem, error) {
	var out []configSetItem
	for _, f := range []struct{ flag, key, val string }{
		{"node", "node", node}, {"memory", "memory", memory}, {"messages-loc", "messagesLoc", messagesLoc},
	} {
		if !cmd.Flags().Changed(f.flag) {
			continue
		}
		v := strings.TrimSpace(f.val)
		if f.key == "messagesLoc" {
			// Normalize BEFORE the blank check: ":" trims to "" and must not be stored.
			v = strings.TrimSpace(strings.TrimSuffix(v, ":"))
		}
		if v == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--%s is blank: pass a value (this command sets coordinates; it does not clear them)", f.flag)
		}
		if f.key == "node" {
			if _, _, err := splitNodeURN(v); err != nil {
				return nil, err
			}
		}
		out = append(out, configSetItem{Key: f.key, Value: v})
	}
	if len(out) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "nothing to set — pass --node, or -m/--memory and/or --messages-loc")
	}
	return out, nil
}

// applyConfigSet finds the file the readers would use, rewrites only the named
// chat.* keys, and installs the result atomically after a backup.
func applyConfigSet(sets []configSetItem) (configSetDTO, error) {
	dto := configSetDTO{Set: sets}
	_, cf := findProjectChat()
	path := cf.Path
	var raw []byte
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return dto, exitcode.Newf(exitcode.Usage, "cannot resolve the working directory: %v", err)
		}
		path = filepath.Join(wd, ".hadron", "config.json")
		// A config the readers skipped (unreadable) leaves Path empty, but it is
		// there: creating a file beside or over it would hide or destroy what we
		// could not read.
		if len(cf.Skipped) > 0 {
			return dto, exitcode.Newf(exitcode.Usage, "%s exists but cannot be read (%s) — nothing written; fix its permissions or remove it", cf.Skipped[0].Path, cf.Skipped[0].Reason)
		}
		if _, err := os.Lstat(path); err == nil {
			return dto, exitcode.Newf(exitcode.Usage, "%s exists but cannot be read — nothing written", path)
		}
		dto.Created = true
	} else {
		var err error
		if raw, err = os.ReadFile(path); err != nil {
			return dto, exitcode.Newf(exitcode.Usage, "cannot read %s: %v — nothing written", path, err)
		}
	}
	dto.Path = path

	out, err := editChatConfig(raw, !dto.Created, sets)
	if err != nil {
		return dto, exitcode.Newf(exitcode.Usage, "%s: %v — nothing written", path, err)
	}

	perm := os.FileMode(0o600)
	if !dto.Created {
		fi, err := os.Lstat(path)
		if err != nil {
			return dto, exitcode.Newf(exitcode.Usage, "cannot stat %s: %v — nothing written", path, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return dto, exitcode.Newf(exitcode.Usage, "%s is a symlink; refusing to write through it — nothing written", path)
		}
		perm = fi.Mode().Perm()
		bak := path + ".bak"
		if err := config.WriteFileAtomic(bak, raw, perm); err != nil {
			return dto, exitcode.Newf(exitcode.Usage, "cannot back up %s: %v — nothing written", path, err)
		}
		dto.Backup = &bak
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return dto, exitcode.Newf(exitcode.Usage, "cannot create %s: %v — nothing written", filepath.Dir(path), err)
	}
	if err := config.WriteFileAtomic(path, out, perm); err != nil {
		return dto, exitcode.Newf(exitcode.Usage, "cannot write %s: %v", path, err)
	}
	return dto, nil
}

// editChatConfig sets chat.<key> for each item in raw (nil/blank = a new
// file), preserving every other key's raw bytes and the key order. It errors on
// anything it cannot edit without guessing.
func editChatConfig(raw []byte, exists bool, sets []configSetItem) ([]byte, error) {
	top := &orderedObject{}
	if exists {
		// An existing file that is empty or whitespace is not an object: refuse
		// rather than overwrite what we cannot read.
		var err error
		if top, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("not an editable JSON object (%v)", err)
		}
	}
	chat := &orderedObject{}
	if cur, ok := top.get("chat"); ok {
		var err error
		if chat, err = parseObject(cur); err != nil {
			return nil, fmt.Errorf(`"chat" is not an editable JSON object (%v)`, err)
		}
	}
	for _, s := range sets {
		chat.set(s.Key, jsonString(s.Value))
	}
	top.set("chat", chat.marshal("  "))
	return append(top.marshal(""), '\n'), nil
}

// orderedObject is a JSON object whose members stay raw — key bytes as written
// (escapes included) and value bytes as written — so editing one key cannot
// re-encode, reformat or reorder another. Only the whitespace BETWEEN members
// is regenerated.
type orderedObject struct {
	keys []string // decoded keys, in order (identity for lookup/dup checks)
	raws map[string][]byte
	vals map[string]json.RawMessage
}

// find returns the stored key that encoding/json would match k to — the readers
// decode case-insensitively — or "" when there is none. parseObject refuses
// files with two keys that fold equal, so there is at most one.
func (o *orderedObject) find(k string) string {
	for _, have := range o.keys {
		if strings.EqualFold(have, k) {
			return have
		}
	}
	return ""
}

func (o *orderedObject) get(k string) (json.RawMessage, bool) {
	if have := o.find(k); have != "" {
		return o.vals[have], true
	}
	return nil, false
}

// set replaces k's value, keeping an existing member's raw key; a new member
// gets a freshly encoded key.
func (o *orderedObject) set(k string, v json.RawMessage) {
	if o.vals == nil {
		o.vals, o.raws = map[string]json.RawMessage{}, map[string][]byte{}
	}
	if have := o.find(k); have != "" {
		k = have // edit the member the readers would match, in place
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
		o.raws[k] = jsonString(k)
	}
	o.vals[k] = v
}

func parseObject(raw []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not an object")
	}
	o := &orderedObject{}
	for dec.More() {
		prev := dec.InputOffset()
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		if have := o.find(k); have != "" {
			return nil, fmt.Errorf("duplicate key %q (also %q: the readers match keys case-insensitively)", k, have)
		}
		// The key token's bytes: from the first non-separator after the previous
		// token to the decoder's offset.
		rawKey := bytes.TrimLeft(raw[prev:dec.InputOffset()], " \t\r\n,:")
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
		o.raws[k] = append([]byte(nil), rawKey...)
	}
	if _, err := dec.Token(); err != nil { // closing }
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing content after the object")
	}
	return o, nil
}

func (o *orderedObject) marshal(indent string) []byte {
	var b bytes.Buffer
	b.WriteString("{")
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n" + indent + "  ")
		b.Write(o.raws[k])
		b.WriteString(": ")
		b.Write(o.vals[k])
	}
	b.WriteString("\n" + indent + "}")
	return b.Bytes()
}

// jsonString encodes s without HTML escaping, so a value or key round-trips as
// typed (json.Marshal would rewrite < > & as \u00XX).
func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}
