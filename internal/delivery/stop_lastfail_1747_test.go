package delivery

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// hadron-cli#1747: the five scoped §10 gaps from cli#1746 plus the exact
// cache-size boundary, as test pins over the .lastfail observation cache and
// PendingStatus. Tests-only per the T0 Light gate: no production change.

// cache1747 writes a v1 observation for ev-1/seq 2 with the given reason and
// field, plus the matching accepted intent, into a fresh fixture outbox.
func cache1747(t *testing.T, reason, field string) *reporterFixture {
	t.Helper()
	f := newReporterFixture(t)
	writeBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".sending"), mustJSON1747(stopSending{AcceptedSeq: 2}))
	writeBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".lastfail"),
		mustJSON1747(stopLastFail{V: 1, EventID: "ev-1", Seq: 2, Reason: reason, Field: field, LastObservedAt: f.clock}))
	return f
}

// status1747 reads PendingStatus with a reporter that has no API: a server
// call would panic, pinning the dry-run's local-only contract.
func status1747(t *testing.T, f *reporterFixture) PendingReport {
	t.Helper()
	r := f.reporter()
	r.API = nil
	p, err := r.PendingStatus(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustJSON1747(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestLastFailUnavailableIsNotNeedsPerson_1747: readback-unavailable is a
// valid cached reason (the file is well-formed and readable) but never a
// needsPerson observation while the report is still pending.
func TestLastFailUnavailableIsNotNeedsPerson_1747(t *testing.T) {
	f := cache1747(t, "readback-unavailable", "")
	p := status1747(t, f)
	if p.Pending != 1 || p.NeedsPerson != 0 || p.RecordsTotal != 0 || len(p.Records) != 0 {
		t.Fatalf("unavailable counted as needing a person: %+v", p)
	}
	// The exclusion is reason-specific: a not-found observation over the same
	// shape counts.
	writeBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".lastfail"),
		mustJSON1747(stopLastFail{V: 1, EventID: "ev-1", Seq: 2, Reason: "readback-not-found", LastObservedAt: f.clock}))
	p = status1747(t, f)
	if p.Pending != 1 || p.NeedsPerson != 1 || len(p.Records) != 1 || p.Records[0].Reason != "readback-not-found" {
		t.Fatalf("not-found not counted: %+v", p)
	}
}

// TestLastFailWhitelistRejectsHostileValues_1747: the dry-run line prints the
// cached reason and field as they are, so the reason/field whitelist is what
// keeps a forged file's escape sequences off the terminal. Hostile and
// non-whitelisted values must never become observations.
func TestLastFailWhitelistRejectsHostileValues_1747(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		field  string
	}{
		{"OSC injection in field", "readback-mismatch", "\x1b]0;pwned\x07"},
		{"CSI colour in field", "readback-mismatch", "\x1b[31mauthor"},
		{"trailing escape in field", "readback-mismatch", "author\x1b"},
		{"newline forging two lines", "readback-mismatch", "author\nbody"},
		{"escape in reason", "readback-mismat\x1bch", "author"},
		{"non-whitelisted plain field", "readback-mismatch", "author,body,x"},
		{"padded field", "readback-mismatch", " author "},
		{"field set on an empty-field reason", "readback-not-found", "author"},
		{"mismatch field on invalid-accepted-record", "invalid-accepted-record", "body"},
		{"unknown reason", "worker-on-fire", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cache1747(t, tc.reason, tc.field)
			p := status1747(t, f)
			if p.Pending != 1 || p.NeedsPerson != 0 || len(p.Records) != 0 {
				t.Fatalf("forged observation admitted: %+v", p)
			}
		})
	}
	// Positive contrast: the whitelisted values are admitted, so the pins
	// above reject the value, not the fixture.
	for _, field := range []string{"author", "body", "author,body"} {
		f := cache1747(t, "readback-mismatch", field)
		p := status1747(t, f)
		if p.NeedsPerson != 1 || len(p.Records) != 1 || p.Records[0].Field != field {
			t.Fatalf("whitelisted field %q not counted: %+v", field, p)
		}
	}
}

// TestLastFailInvalidAcceptedRecordCounts_1747 is the positive
// invalid-accepted-record control, plus null-field rejection across every
// reason whose valid field is the empty string (the existing repo null-field
// case exercised mismatch only).
func TestLastFailInvalidAcceptedRecordCounts_1747(t *testing.T) {
	f := cache1747(t, "invalid-accepted-record", "")
	p := status1747(t, f)
	if p.Pending != 1 || p.NeedsPerson != 1 || len(p.Records) != 1 || p.Records[0].Reason != "invalid-accepted-record" || p.Records[0].Field != "" {
		t.Fatalf("invalid-accepted-record not counted: %+v", p)
	}
	for _, reason := range []string{"readback-not-found", "invalid-accepted-record", "readback-unavailable"} {
		t.Run("null field on "+reason, func(t *testing.T) {
			f := cache1747(t, reason, "")
			path := filepath.Join(f.box.Dir, ev1Base()+".lastfail")
			valid := map[string]any{}
			_ = json.Unmarshal(readBytes1675(t, path), &valid)
			valid["field"] = nil
			writeBytes1675(t, path, mustJSON1747(valid))
			p := status1747(t, f)
			if p.Pending != 1 || p.NeedsPerson != 0 || len(p.Records) != 0 {
				t.Fatalf("null field admitted for %s: %+v", reason, p)
			}
		})
	}
}

// TestLastFailExactSizeBoundary_1747 pins the cache's exact byte bound: a
// well-formed observation of exactly 1024 bytes is admitted, and one byte
// more is refused.
func TestLastFailExactSizeBoundary_1747(t *testing.T) {
	f := cache1747(t, "readback-mismatch", "body")
	path := filepath.Join(f.box.Dir, ev1Base()+".lastfail")
	valid := readBytes1675(t, path)
	if len(valid) > 1024 {
		t.Fatalf("fixture cache is already %d bytes", len(valid))
	}
	at := func(n int) []byte {
		return append(append([]byte{}, valid...), bytes.Repeat([]byte(" "), n-len(valid))...)
	}
	writeBytes1675(t, path, at(1024))
	p := status1747(t, f)
	if p.Pending != 1 || p.NeedsPerson != 1 || len(p.Records) != 1 {
		t.Fatalf("a 1024-byte cache is not admitted: %+v", p)
	}
	writeBytes1675(t, path, at(1025))
	p = status1747(t, f)
	if p.Pending != 1 || p.NeedsPerson != 0 || p.RecordsTotal != 0 || len(p.Records) != 0 {
		t.Fatalf("a 1025-byte cache is admitted: %+v", p)
	}
}

// TestLastFailCorruptCacheClearedAfterFirstPost_1747: a corrupt or stale
// advisory file left in the outbox is removed once a first post succeeds and
// its marker is durable — the cleanup QA could not assert on the compiled
// fake.
func TestLastFailCorruptCacheClearedAfterFirstPost_1747(t *testing.T) {
	t.Run("corrupt bytes", func(t *testing.T) {
		f := newReporterFixture(t)
		writeBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".lastfail"), []byte("{corrupt"))
		out := onlyOutcome(t, f.run(f.reporter()))
		if out.Result != "reported" || out.DiagnosticError != "" {
			t.Fatalf("outcome %+v", out)
		}
		if _, e := os.Lstat(filepath.Join(f.box.Dir, ev1Base()+".lastfail")); !os.IsNotExist(e) {
			t.Fatalf("corrupt cache survived a successful first post: %v", e)
		}
		if !f.reported() {
			t.Fatal("no marker")
		}
	})
	t.Run("stale but well-formed", func(t *testing.T) {
		f := newReporterFixture(t)
		// An observation for a seq this outbox never accepted: dead weight
		// from an earlier cycle, with no intent behind it.
		writeBytes1675(t, filepath.Join(f.box.Dir, ev1Base()+".lastfail"),
			mustJSON1747(stopLastFail{V: 1, EventID: "ev-1", Seq: 99, Reason: "readback-mismatch", Field: "body", LastObservedAt: f.clock}))
		p := status1747(t, f)
		if p.Pending != 1 || p.NeedsPerson != 0 {
			t.Fatalf("stale cache counted without an accepted intent: %+v", p)
		}
		out := onlyOutcome(t, f.run(f.reporter()))
		if out.Result != "reported" || out.DiagnosticError != "" {
			t.Fatalf("outcome %+v", out)
		}
		if _, e := os.Lstat(filepath.Join(f.box.Dir, ev1Base()+".lastfail")); !os.IsNotExist(e) {
			t.Fatalf("stale cache survived a successful first post: %v", e)
		}
	})
}
