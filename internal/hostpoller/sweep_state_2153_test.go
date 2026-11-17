package hostpoller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSweepOutageScopeRenewsToken2153(t *testing.T) {
	for _, next := range []struct{ app, host string }{{"other-app", "host"}, {"app", "other-host"}, {"other-app", "other-host"}} {
		t.Run(next.app+"/"+next.host, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			s, err := LoadState(path)
			if err != nil {
				t.Fatal(err)
			}
			first, reported, err := s.sweepOutageToken("app", "host")
			if err != nil || first == "" || reported {
				t.Fatalf("%q %v %v", first, reported, err)
			}
			if err := s.markSweepOutage(first); err != nil {
				t.Fatal(err)
			}
			s, err = LoadState(path)
			if err != nil {
				t.Fatal(err)
			}
			same, reported, err := s.sweepOutageToken("app", "host")
			if err != nil || same != first || !reported {
				t.Fatalf("same scope: %q %v %v", same, reported, err)
			}
			second, reported, err := s.sweepOutageToken(next.app, next.host)
			if err != nil || second == "" || second == first || reported {
				t.Fatalf("scope change reused identity/ack: %q %v %v", second, reported, err)
			}
			s, err = LoadState(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.SweepOutage == nil || s.SweepOutage.AppID != next.app || s.SweepOutage.HostID != next.host || s.SweepOutage.RunID != second || s.SweepOutage.Reported {
				t.Fatalf("wrong persisted scope: %+v", s.SweepOutage)
			}
			if err := s.markSweepOutage(first); err != nil {
				t.Fatal(err)
			}
			if s.SweepOutage.Reported {
				t.Fatal("old acknowledgement marked renewed episode")
			}
		})
	}
}

func TestSweepOutageSaveFailuresRollBack2153(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.sweepOutageToken("app", "host")
	if err != nil {
		t.Fatal(err)
	}
	original := *s.SweepOutage
	// A directory at the atomic temporary-file path fails even under root.
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if got, ack, err := s.sweepOutageToken("other", "host"); err == nil || got != "" || ack {
		t.Fatalf("failed renewal reported identity: %q %v %v", got, ack, err)
	}
	if *s.SweepOutage != original {
		t.Fatalf("renewal not rolled back: %+v", s.SweepOutage)
	}
	if err := s.markSweepOutage(token); err == nil || s.SweepOutage.Reported {
		t.Fatalf("failed ack not rolled back: %v %+v", err, s.SweepOutage)
	}
	if err := s.clearSweepOutage(); err == nil || s.SweepOutage == nil || *s.SweepOutage != original {
		t.Fatalf("failed clear not rolled back: %v %+v", err, s.SweepOutage)
	}
	disk, err := LoadState(path)
	if err != nil || disk.SweepOutage == nil || *disk.SweepOutage != original {
		t.Fatalf("disk changed: %v %+v", err, disk)
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s.markSweepOutage(token); err != nil {
		t.Fatal(err)
	}
	if err := s.clearSweepOutage(); err != nil {
		t.Fatal(err)
	}
	disk, err = LoadState(path)
	if err != nil || disk.SweepOutage != nil {
		t.Fatalf("successful clear not persisted: %v %+v", err, disk)
	}
}

func TestSweepOutageInitialSaveFailure2153(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	token, reported, err := s.sweepOutageToken("app", "host")
	if err == nil || token != "" || reported || s.SweepOutage != nil {
		t.Fatalf("failed initial save retained token: %q %v %v %+v", token, reported, err, s.SweepOutage)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("initial save created state: %v", err)
	}
}
