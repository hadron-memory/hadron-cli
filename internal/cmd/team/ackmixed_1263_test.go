package team

import (
	"github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// hadron-cli#1263: delivery.ack_mixed values that arrive through a YAML merge
// or through an alias of delivery are judged like a literal value. The decoder
// lets null, ~ and the legacy bool words through (null reads as omitted), so
// these reach the source-node guard; the merge cases die if yamlField stops
// following merge keys (#1258 review mutant M3).

// loadAckMixed1263 loads a host file ending in fragment and returns the label ("line N") the load
// uses for the fragment's first line, read from the file as written, so no test hard-codes a number
// that depends on the fixture helper's output shape (cli#3026).
func loadAckMixed1263(t *testing.T, fragment string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "host.yaml")
	body := "app: app1\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\nworktrees_root: " + dir + "\n" + fragment
	text := hostyaml.Supervised(body)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	at := strings.Index(text, fragment)
	if at < 0 {
		t.Fatalf("the fragment is not in the file:\n%s", text)
	}
	line := "line " + strconv.Itoa(1+strings.Count(text[:at], "\n"))
	_, err := hostpoller.LoadConfig(path)
	return line, err
}

func TestAckMixed1263MergeAndAliasRefuseNonBoolean(t *testing.T) {
	shapes := []struct{ name, fragment string }{
		{"merge", "delivery: {<<: {ack_mixed: %s}, enabled: true, orca_bin: /bin/true}\n"},
		{"alias of delivery", "<<: {delivery: &d {enabled: true, orca_bin: /bin/true, ack_mixed: %s}}\ndelivery: *d\n"},
	}
	// Reach the guard: the decoder accepts these, so only the source-node check
	// refuses them, with its own message and the value's line.
	for _, v := range []string{"null", "~", "yes", "on"} {
		for _, s := range shapes {
			t.Run(s.name+"/"+v, func(t *testing.T) {
				line, err := loadAckMixed1263(t, strings.Replace(s.fragment, "%s", v, 1))
				want := "delivery.ack_mixed at " + line + " must be a boolean"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("want %q, got %v", want, err)
				}
			})
		}
	}
	// The decoder refuses these first; still refused, still naming the line.
	for _, v := range []string{`"true"`, "1", "maybe", "[]", "{}"} {
		for _, s := range shapes {
			t.Run(s.name+"/"+v, func(t *testing.T) {
				line, err := loadAckMixed1263(t, strings.Replace(s.fragment, "%s", v, 1))
				if err == nil || !strings.Contains(err.Error(), line) {
					t.Fatalf("want a refusal naming %s, got %v", line, err)
				}
			})
		}
	}
}

// Item 2: a merged non-boolean is refused even when an explicit key shadows it
// and the decoder's effective value is a real boolean. Deliberately strict
// (a loud over-rejection, never a silent enablement); relaxing it needs its
// own gate. Normal merge, alias and KnownFields behavior is pinned by the
// #1229 tests beside this file.
func TestAckMixed1263ShadowedMergeStaysRefused(t *testing.T) {
	line, err := loadAckMixed1263(t, "delivery: {<<: {ack_mixed: \"yes\"}, ack_mixed: true, enabled: true, orca_bin: /bin/true}\n")
	want := "delivery.ack_mixed at " + line + " must be a boolean"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a shadowed non-boolean merge must stay refused (%q), got %v", want, err)
	}
}
