package delivery

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #2327 (Joost's P2): the manual-delete hint names every user of the session
// binding lock. The set is derived from the call sites, so a new caller cannot
// drift past the hint: it fails here until the hint (LockUsersHint) and this
// table are updated together.
//
// Each entry is a non-test source file that takes the lock, directly or
// through its wrapper, and the user the hint must cover for it.
var lockUserFiles2327 = map[string]string{
	"internal/cmd/team/state.go":                 "session", // withBindingLock/At, updateBinding, clearBinding, recordChatWatermark, bindingIsSession
	"internal/cmd/team/session.go":               "session", // updateBinding, clearBinding
	"internal/cmd/team/session_reset_context.go": "session", // withBindingLockAt
	"internal/cmd/team/chat.go":                  "chat",    // recordChatWatermark
	"internal/cmd/team/chat_mark_read.go":        "chat",    // bindingIsSession
	"internal/cmd/team/chat_attribution.go":      "chat",    // bindingIsSession
	"internal/hostpoller/ops.go":                 "poller",  // p.lockBinding (delivery.LockBinding)
	"internal/hostpoller/relaunch_loop.go":       "poller",  // delivery.LockBinding
	"internal/hostpoller/relaunch_terminals.go":  "poller",  // delivery.LockBinding
	"internal/hostpoller/poller.go":              "poller",  // lockBinding: delivery.LockBinding
}

var lockCaller2327 = regexp.MustCompile(`\b(withBindingLock|withBindingLockAt|updateBinding|clearBinding|recordChatWatermark|bindingIsSession|LockBinding|lockBinding)\(|lockBinding:`)

// scanLockCallers2327 returns the non-test files under root/internal that call
// the binding lock, other than the delivery package itself and generated code.
func scanLockCallers2327(root string) (map[string]bool, error) {
	found := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/delivery/") || strings.HasPrefix(rel, "internal/api/gen/") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "//") || strings.HasPrefix(l, "func ") {
				continue
			}
			if lockCaller2327.MatchString(l) {
				found[rel] = true
				break
			}
		}
		return nil
	})
	return found, err
}

func TestLockUsersHintCoversEveryCallSite2327(t *testing.T) {
	found, err := scanLockCallers2327(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for f := range found {
		got = append(got, f)
	}
	for f := range lockUserFiles2327 {
		want = append(want, f)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lock callers changed; update LockUsersHint and lockUserFiles2327 together.\nfound:\n%s\nlisted:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	kinds := map[string]bool{}
	for _, k := range lockUserFiles2327 {
		kinds[k] = true
	}
	for k := range kinds {
		if !strings.Contains(LockUsersHint, map[string]string{"session": "session", "chat": "chat", "poller": "poller"}[k]) {
			t.Errorf("LockUsersHint does not name the %s lock users: %q", k, LockUsersHint)
		}
	}
}
