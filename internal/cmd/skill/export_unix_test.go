//go:build unix

package skill

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// A FIFO named SKILL.md is refused without being read: reading it would block
// forever, a dry run included (#696 review). Unix only (no mkfifo on Windows).
func TestExportRefusesANonRegularSkillFileWithoutReadingIt(t *testing.T) {
	for _, dry := range []bool{true, false} {
		h := home(t)
		dir := filepath.Join(h, ".claude", "skills", "demo")
		mkdir(t, dir)
		if err := syscall.Mkfifo(filepath.Join(dir, "SKILL.md"), 0o644); err != nil {
			t.Fatal(err)
		}
		p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionWrite, "b", "")}}
		done := make(chan exportHostDTO, 1)
		go func() {
			hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{dryRun: dry})
			done <- hd
		}()
		select {
		case hd := <-done:
			if len(hd.Failed) != 1 || codes(hd.Failed[0].Reasons)[len(hd.Failed[0].Reasons)-1] != reasonNotRegular {
				t.Errorf("dryRun=%v: failed = %+v written=%v", dry, hd.Failed, names(hd.Written))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("dryRun=%v: export blocked on a FIFO named SKILL.md", dry)
		}
	}
}

// The shared walk (status and export) reports a FIFO named SKILL.md as
// unreadable instead of blocking on it.
func TestWalkDoesNotReadANonRegularSkillFile(t *testing.T) {
	root := filepath.Join(home(t), "skills")
	mkdir(t, filepath.Join(root, "demo"))
	if err := syscall.Mkfifo(filepath.Join(root, "demo", "SKILL.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan []statusUnreadableDTO, 1)
	go func() {
		_, unreadable, _, _ := walkSkillFiles(root)
		done <- unreadable
	}()
	select {
	case u := <-done:
		if len(u) != 1 || u[0].Dir != "demo" {
			t.Errorf("unreadable = %+v", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the walk blocked on a FIFO named SKILL.md")
	}
}
