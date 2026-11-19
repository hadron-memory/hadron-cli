package team

import (
	"strings"
	"testing"
)

// #2347 (follow-ups to #2345): the AT0 handle grammar, the ref-names-handle
// match and the team tree's reserved-row handling, pinned directly
// (cor:agt:050:01: handles are AT<n>(.<n>)*; a segment with a leading zero, or a
// zero below the project level, is malformed).

func TestReservedNoProjectParentGrammar2347(t *testing.T) {
	for handle, want := range map[string]bool{
		"AT0.1":   true,  // valid child of the reserved project
		"AT0.388": true,  // multi-digit
		"AT0.10":  true,  // a zero that is not leading
		"":        false, // missing handle
		"AT0":     false, // the reserved row itself
		"AT0.":    false, // empty child segment
		"AT0.0":   false, // zero below the project level
		"AT0.01":  false, // leading zero
		"AT0.5.3": false, // a subtask: its parent is AT0.5
		"AT0.x":   false, // non-digit
		"AT0.1x":  false,
		"AT0.-1":  false,
		"AT1.2":   false, // a real project
		"at0.1":   false, // the grammar is upper-case
		"X.1":     false,
	} {
		if got := reservedNoProjectParent(handle); got != want {
			t.Errorf("reservedNoProjectParent(%q) = %v, want %v", handle, got, want)
		}
	}
}

// refNamesTask matches the handle a ref asks for, never the App qualifier's
// text: the response is one App's chain (the server resolved the ref), so the
// App is the response's, and a renamed App's old slug still names the task.
func TestRefNamesHandleParsesTheRef2347(t *testing.T) {
	for name, tc := range map[string]struct {
		ref  string
		want bool
	}{
		"id":                       {"at0id", true},
		"bare handle":              {"AT0", true},
		"bare handle, any case":    {"at0", true},
		"flat qualified":           {"hrn:agenttask:acme.com:team:AT0", true},
		"embedded-App qualified":   {"hrn:agenttask:hrn:app:acme.com:team:AT0", true},
		"another App, same handle": {"hrn:agenttask:other.com:eng:AT0", true},
		"renamed App's old slug":   {"hrn:agenttask:acme.com:old-slug:AT0", true},
		"a child's handle":         {"hrn:agenttask:acme.com:team:AT0.5", false},
		"a project's handle":       {"AT1", false},
		"no App part":              {"hrn:agenttask:AT0", false},
		"root without a slug":      {"hrn:agenttask:acme.com:AT0", false},
		"extra segment":            {"hrn:agenttask:acme.com:team:x:AT0", false},
		"not a task ref":           {"hrn:worker:acme.com:team:AT0", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := refNamesTask(tc.ref, "at0id", "AT0"); got != tc.want {
				t.Errorf("refNamesTask(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
	if refNamesTask("AT0", "at0id", "") {
		t.Error("a task with no handle is only named by its id")
	}
}

// renderTeamTree drops EVERY reserved AT0 row as a parent, not just the first:
// the team listing reads one App today (hadron-server#2370, and the help says
// so), but the render must not depend on that (cor:agt:050:01: one AT0 per App).
func TestRenderTeamTreeHoistsChildrenOfEveryReservedRow2347(t *testing.T) {
	row := func(id, handle, parent string) teamTaskDTO {
		t := teamTaskDTO{taskDTO: taskDTO{ID: id, Handle: handle, Status: "PENDING", Title: id}}
		if parent != "" {
			p := parent
			t.ParentTaskID = &p
		}
		return t
	}
	var out strings.Builder
	err := renderTeamTree(&out, []teamTaskDTO{
		row("at0a", "AT0", ""), row("a1", "AT0.1", "at0a"),
		row("at0b", "AT0", ""), row("b1", "AT0.1", "at0b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.HasPrefix(line, " ") {
			t.Errorf("a child of a reserved row nested under it: %q\n%s", line, out.String())
		}
	}
	if got := strings.Count(out.String(), "• "); got != 4 {
		t.Errorf("want 4 rows, got %d:\n%s", got, out.String())
	}
}
