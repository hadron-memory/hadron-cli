package delivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mismatch1675(t *testing.T) *reporterFixture {
	t.Helper()
	f := newReporterFixture(t)
	r := f.reporter()
	r.afterPost = func() { f.chat.msgs[len(f.chat.msgs)-1].Body = "private body" }
	f.run(r)
	return f
}
func readBytes1675(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func writeBytes1675(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestLastFailAdvisory1675(t *testing.T) {
	f := mismatch1675(t)
	r := f.reporter()
	statusPath := filepath.Join(f.box.Dir, ev1Base()+".lastfail")
	sendingPath := filepath.Join(f.box.Dir, ev1Base()+".sending")
	before := readBytes1675(t, sendingPath)
	good := onlyOutcome(t, f.run(r))
	if good.Reason != "readback-mismatch" || good.DiagnosticError != "" {
		t.Fatal(good)
	}
	if err := os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statusPath, 0700); err != nil {
		t.Fatal(err)
	}
	writeBytes1675(t, filepath.Join(statusPath, "keep"), []byte("sentinel"))
	bad := onlyOutcome(t, f.run(r))
	if bad.Reason != good.Reason || bad.Field != good.Field || bad.Result != good.Result || bad.Error != good.Error || bad.Seq != good.Seq || bad.DiagnosticError != "lastfail-write-failed" {
		t.Fatalf("advisory changed outcome: good%+v bad%+v", good, bad)
	}
	if !bytes.Equal(before, readBytes1675(t, sendingPath)) || f.chat.attempts != 1 {
		t.Fatal("sending/repost changed")
	}
	// The marker must become durable before removal; failure to write it keeps status.
	if err := os.RemoveAll(statusPath); err != nil {
		t.Fatal(err)
	}
	f.run(r)
	old := readBytes1675(t, statusPath)
	f.chat.msgs[len(f.chat.msgs)-1].Body = stopDropBody("ada", StopDrop{EventID: "ev-1", Worker: "Gil", Level: "HALT", HoldReason: "draft", HeldAt: f.clock})
	marker := filepath.Join(f.box.Dir, ev1Base()+".reported")
	r.beforeReported = func(string) {
		if err := os.Mkdir(marker, 0700); err != nil {
			t.Fatal(err)
		}
	}
	failed := onlyOutcome(t, f.run(r))
	if failed.Result != "failed" || !bytes.Equal(old, readBytes1675(t, statusPath)) {
		t.Fatal("removed before durable marker")
	}
	r.beforeReported = nil
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	// Force removal failure after the successful marker. The marker wins.
	if err := os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statusPath, 0700); err != nil {
		t.Fatal(err)
	}
	writeBytes1675(t, filepath.Join(statusPath, "keep"), []byte("sentinel"))
	out := onlyOutcome(t, f.run(r))
	if out.Result != "reconciled" || out.DiagnosticError != "lastfail-remove-failed" || !f.reported() {
		t.Fatal(out)
	}
	p, err := r.PendingStatus(0)
	if err != nil || p.Pending != 0 || p.NeedsPerson != 0 || len(p.Records) != 0 {
		t.Fatalf("marker didn't win %+v %v", p, err)
	}
	if !bytes.Equal(before, readBytes1675(t, sendingPath)) {
		t.Fatal("sending changed")
	}
}

func TestLastFailLaterMatchClears1675(t *testing.T) {
	f := mismatch1675(t)
	path := filepath.Join(f.box.Dir, ev1Base()+".lastfail")
	var intent stopSending
	_ = json.Unmarshal(readBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".sending")), &intent)
	f.chat.msgs[len(f.chat.msgs)-1].Body = intent.Body
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "reconciled" {
		t.Fatal(out)
	}
	if _, e := os.Lstat(path); !os.IsNotExist(e) {
		t.Fatalf("cache not cleared %v", e)
	}
}

func TestLastFailRejectedCache1675(t *testing.T) {
	cases := []string{"missing", "corrupt", "oversize", "version", "null", "wrong type", "missing field", "extra field", "wrong event", "wrong seq", "no accepted", "directory", "symlink inside", "symlink outside", "swap symlink"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			f := mismatch1675(t)
			r := f.reporter()
			path := filepath.Join(f.box.Dir, ev1Base()+".lastfail")
			sending := filepath.Join(f.box.Dir, ev1Base()+".sending")
			valid := readBytes1675(t, path)
			var v map[string]any
			_ = json.Unmarshal(valid, &v)
			switch kind {
			case "missing":
				_ = os.Remove(path)
			case "corrupt":
				writeBytes1675(t, path, []byte("{"))
			case "oversize":
				writeBytes1675(t, path, append(valid, bytes.Repeat([]byte(" "), 1025)...))
			case "version":
				v["v"] = 2
			case "null":
				v["field"] = nil
			case "wrong type":
				v["seq"] = "2"
			case "missing field":
				delete(v, "lastObservedAt")
			case "extra field":
				v["body"] = "secret"
			case "wrong event":
				v["eventId"] = "other"
			case "wrong seq":
				v["seq"] = 3
			case "no accepted":
				var i map[string]any
				_ = json.Unmarshal(readBytes1675(t, sending), &i)
				delete(i, "acceptedSeq")
				b, _ := json.Marshal(i)
				writeBytes1675(t, sending, b)
			case "directory":
				_ = os.Remove(path)
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink inside", "symlink outside", "swap symlink":
				dir := f.box.Dir
				if kind != "symlink inside" {
					dir = t.TempDir()
				}
				target := filepath.Join(dir, "private-target")
				writeBytes1675(t, target, valid)
				swap := func() {
					_ = os.Remove(path)
					if err := os.Symlink(target, path); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				}
				if kind == "swap symlink" {
					r.beforeLastFailOpen = func(string) { swap() }
				} else {
					swap()
				}
				defer func() {
					if !bytes.Equal(valid, readBytes1675(t, target)) {
						t.Fatal("target changed")
					}
				}()
			}
			switch kind {
			case "version", "null", "wrong type", "missing field", "extra field", "wrong event", "wrong seq":
				b, _ := json.Marshal(v)
				writeBytes1675(t, path, b)
			}
			sendingBefore := readBytes1675(t, sending)
			r.API = nil // panics if any server call is added
			p, err := r.PendingStatus(time.Minute)
			if err != nil || p.Pending != 1 || p.NeedsPerson != 0 || p.RecordsTotal != 0 || len(p.Records) != 0 {
				t.Fatalf("rejected cache counted %+v %v", p, err)
			}
			if !bytes.Equal(sendingBefore, readBytes1675(t, sending)) {
				t.Fatal("dryrun changed sending")
			}
			// Invalid/stale observations do not control delivery. A failed run overwrites them.
			if kind != "directory" && kind != "no accepted" {
				r.beforeLastFailOpen = nil
				r.API = f.chat
				out := onlyOutcome(t, f.run(r))
				if out.Result != "failed" || out.Reason != "readback-mismatch" {
					t.Fatal(out)
				}
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					t.Fatal("cache not replaced")
				}
			}
		})
	}
}

func TestLastFailBoundedPending1675(t *testing.T) {
	f := newReporterFixture(t)
	if err := os.Remove(filepath.Join(f.box.Dir, ev1Base()+".json")); err != nil {
		t.Fatal(err)
	}
	for i := 50; i >= 0; i-- {
		id := fmt.Sprintf("event-%02d", i)
		held := f.clock.Add(-time.Hour)
		if i == 50 {
			held = held.Add(-time.Hour)
		}
		rec := StopDrop{EventID: id, Worker: "worker\x1b", HeldAt: held}
		if _, e := f.box.Drop(rec); e != nil {
			t.Fatal(e)
		}
		base := strings.TrimSuffix(stopDropName(id), ".json")
		seq := i + 1
		b, _ := json.Marshal(stopSending{AcceptedSeq: seq})
		writeBytes1675(t, filepath.Join(f.box.Dir, base+".sending"), b)
		b, _ = json.Marshal(stopLastFail{V: 1, EventID: id, Seq: seq, Reason: "readback-mismatch", Field: "body", LastObservedAt: f.clock})
		writeBytes1675(t, filepath.Join(f.box.Dir, base+".lastfail"), b)
	}
	r := f.reporter()
	r.API = nil
	p, err := r.PendingStatus(time.Minute)
	if err != nil || p.Pending != 51 || p.Stale != 51 || p.NeedsPerson != 51 || p.RecordsTotal != 51 || len(p.Records) != 50 || !p.RecordsTruncated {
		t.Fatalf("bounds %+v %v", p, err)
	}
	if p.Records[0].EventID != "event-50" || p.Records[1].EventID != "event-00" || p.Records[49].EventID != "event-48" {
		t.Fatal("not heldAt/eventId order")
	}
	if _, e := os.Lstat(filepath.Join(f.box.Dir, ".reporter.lock")); !os.IsNotExist(e) {
		t.Fatal("dryrun took lock")
	}
}

func TestLastFailBadBase1675(t *testing.T) {
	f := newReporterFixture(t)
	old := filepath.Join(f.box.Dir, ev1Base()+".json")
	if e := os.Rename(old, filepath.Join(f.box.Dir, "not-a-hash.json")); e != nil {
		t.Fatal(e)
	}
	r := f.reporter()
	r.afterPost = func() { f.chat.hideFromRead = true }
	out := onlyOutcome(t, f.run(r))
	if out.Reason != "readback-not-found" {
		t.Fatal(out)
	}
	if _, e := os.Lstat(filepath.Join(f.box.Dir, "not-a-hash.lastfail")); !os.IsNotExist(e) {
		t.Fatal("bad base written")
	}
}
