//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCensusUnreadableCwdUIDBoundary2195(t *testing.T) {
	foreign := os.Geteuid() + 1000000
	for _, tc := range []struct {
		name, status string
		skip         bool
	}{
		{"foreign", fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", foreign, foreign, foreign, foreign), true},
		{"missing status", "", false},
		{"malformed", "Uid:\t1000000\tbroken\t1000000\t1000000\n", false},
		{"missing field", "Uid:\t1000000\t1000000\t1000000\n", false},
		{"duplicate", "Uid:\t1000000\t1000000\t1000000\t1000000\nUid:\t1000000\t1000000\t1000000\t1000000\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc, work := t.TempDir(), t.TempDir()
			dir := censusFixture2060(t, proc, work, 41)
			censusFixture2060(t, proc, work, 42)
			if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
				t.Fatal(err)
			}
			if tc.status != "" {
				if err := os.WriteFile(filepath.Join(dir, "status"), []byte(tc.status), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil })
			if tc.skip {
				if err != nil || len(got) != 1 || got[0].PID != 42 {
					t.Fatalf("foreign exclusion: agents=%+v err=%v", got, err)
				}
			} else if err == nil {
				t.Fatal("unknown ownership proved absence")
			}
		})
	}
	for _, uid := range []int{0, os.Geteuid()} {
		for field := 0; field < 4; field++ {
			t.Run(fmt.Sprintf("protected-%d-field-%d", uid, field), func(t *testing.T) {
				proc, work := t.TempDir(), t.TempDir()
				dir := censusFixture2060(t, proc, work, 41)
				if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
					t.Fatal(err)
				}
				fields := []string{fmt.Sprint(foreign), fmt.Sprint(foreign), fmt.Sprint(foreign), fmt.Sprint(foreign)}
				fields[field] = fmt.Sprint(uid)
				if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t"+strings.Join(fields, "\t")+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil }); err == nil {
					t.Fatal("same-uid/root unreadable cwd accepted")
				}
			})
		}
	}
}
