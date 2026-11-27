package hostcred

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const testKey = "hk_live_UNITTESTKEY3239"

func tempConfig(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	return filepath.Join(base, "hadron")
}

func entry(app string) Entry {
	return Entry{Server: "https://hadron.example", AppID: app, HostID: "box1", Key: testKey + app}
}

func writeRaw(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPutFindRemoveRoundTrip(t *testing.T) {
	dir := tempConfig(t)
	s, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(entry("a1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(entry("a2")); err != nil {
		t.Fatal(err)
	}
	replaced := entry("a1")
	replaced.Key = "hk_live_REPLACED"
	if err := s.Put(replaced); err != nil {
		t.Fatal(err)
	}
	s.Unlock()

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries %d, want 2 (a Put for the same server and App replaces)", len(got))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, FileName))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %o, want 600", fi.Mode().Perm())
		}
	}

	s, err = Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Unlock()
	if e, ok := s.Find("https://hadron.example/", "a1"); !ok || e.Key != "hk_live_REPLACED" {
		t.Errorf("Find by the server's host: %+v %v", e, ok)
	}
	if ok, err := s.Remove("https://hadron.example", "a1"); !ok || err != nil {
		t.Fatalf("Remove a1: %v %v", ok, err)
	}
	if ok, _ := s.Remove("https://hadron.example", "a1"); ok {
		t.Errorf("a second Remove reported an entry")
	}
	if ok, err := s.Remove("https://hadron.example", "a2"); !ok || err != nil {
		t.Fatalf("Remove a2: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("an empty store left its file behind: %v", err)
	}
}

func TestPutRefusesAPartEntry(t *testing.T) {
	tempConfig(t)
	s, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Unlock()
	for _, e := range []Entry{
		{Server: "https://h", AppID: "a", HostID: "box1"},
		{Server: "https://h", AppID: "a", HostID: "../x", Key: "k"},
		{Server: "", AppID: "a", HostID: "box1", Key: "k"},
		{Server: "https://h", AppID: "a", HostID: "box1", Key: "k with space"},
	} {
		if err := s.Put(e); err != ErrBadEntry {
			t.Errorf("Put(%+v) = %v, want ErrBadEntry", e, err)
		}
	}
}

func TestReadRefusals(t *testing.T) {
	whole := `{"server":"https://h","appId":"a","hostId":"box1","key":"` + testKey + `"}`
	cases := map[string]string{
		"unknown field":  `{"version":1,"entries":[{"server":"https://h","appId":"a","hostId":"box1","key":"` + testKey + `","extra":1}]}`,
		"unknown top":    `{"version":1,"entries":[],"pollInterval":5}`,
		"version":        `{"version":2,"entries":[` + whole + `]}`,
		"duplicate":      `{"version":1,"entries":[` + whole + `,` + whole + `]}`,
		"trailing data":  `{"version":1,"entries":[]} {"version":1}`,
		"part entry":     `{"version":1,"entries":[{"server":"https://h","appId":"a","hostId":"box1","key":""}]}`,
		"malformed JSON": `{"version":1,"entries":[` + testKey,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := tempConfig(t)
			writeRaw(t, dir, body, 0o600)
			_, err := Load()
			if err == nil {
				t.Fatal("Load accepted it")
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the error quotes the key: %v", err)
			}
			if _, err := Lock(); err == nil {
				t.Errorf("Lock accepted it")
			}
		})
	}
}

func TestReadRefusesOpenModesAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode checks are Unix-only")
	}
	body := `{"version":1,"entries":[]}`

	dir := tempConfig(t)
	p := writeRaw(t, dir, body, 0o644)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a 0644 file: %v", err)
	}

	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Errorf("a 0755 directory: %v", err)
	}
	if _, err := Lock(); err == nil {
		t.Errorf("Lock used a 0755 directory")
	}

	dir = tempConfig(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(real, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("a symlink: %v", err)
	}
}

func TestLockSerializesWriters(t *testing.T) {
	tempConfig(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Lock()
			if err != nil {
				errs <- err
				return
			}
			defer s.Unlock()
			if err := s.Put(entry(fmt.Sprintf("app%02d", i))); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Errorf("entries %d, want 20: a write was lost", len(got))
	}
}

func TestKeys(t *testing.T) {
	got := Keys([]Entry{entry("a"), entry("b")})
	if len(got) != 2 || got[0] != testKey+"a" || got[1] != testKey+"b" {
		t.Errorf("Keys = %v", got)
	}
	if Keys(nil) == nil {
		t.Errorf("Keys(nil) is nil, want empty")
	}
}
