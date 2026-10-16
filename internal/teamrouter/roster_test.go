package teamrouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPutRosterEntryPreservesConfigAndToken(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	doc := map[string]any{"app": "app1", "channel": "c1", "stateDir": dir, "futureKey": "keep", "roster": []map[string]any{{"worker": "w1", "name": "Iris", "worktree": filepath.Join(dir, "iris"), "terminal": "term1", "lifecycle": true, "futureRowKey": "keep"}}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "adopted.token")
	if err := os.WriteFile(token, []byte("unchanged-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := RosterEntry{Worker: "w1", Name: "Iris", Worktree: filepath.Join(dir, "iris"), Coordinator: true}
	var hash string
	for range 2 {
		var err error
		hash, err = PutRosterEntry(file, entry)
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if c.Fingerprint != hash || len(c.Roster) != 1 || !c.Roster[0].Coordinator || !c.Roster[0].Lifecycle || c.Roster[0].Terminal != "term1" {
		t.Fatalf("roster lost fields %+v", c)
	}
	after, _ := os.ReadFile(file)
	var got map[string]any
	_ = json.Unmarshal(after, &got)
	if got["futureKey"] != "keep" || got["roster"].([]any)[0].(map[string]any)["futureRowKey"] != "keep" {
		t.Fatalf("unknown fields lost %s", after)
	}
	if err := RecordReload(c); err != nil {
		t.Fatal(err)
	}
	r, err := ReadReload(c)
	if err != nil || r.Fingerprint != hash || r.At.IsZero() {
		t.Fatalf("bad reload receipt %+v %v", r, err)
	}
	kept, _ := os.ReadFile(token)
	if string(kept) != "unchanged-token" {
		t.Fatal("adopted token touched")
	}
	before := string(after)
	for _, other := range []RosterEntry{{Worker: "w2", Name: "Other", Worktree: entry.Worktree}, {Worker: "w1", Name: "Iris", Worktree: filepath.Join(dir, "other")}, {Worker: "w3", Name: "", Worktree: filepath.Join(dir, "third")}} {
		if _, err := PutRosterEntry(file, other); err == nil {
			t.Fatalf("accepted conflict/invalid entry %+v", other)
		}
		b, _ := os.ReadFile(file)
		if string(b) != before {
			t.Fatal("refusal changed config")
		}
	}
}
