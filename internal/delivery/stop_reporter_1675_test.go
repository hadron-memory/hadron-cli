package delivery

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordedSeqOutcome1675(t *testing.T) {
	for _, tc := range []struct{ kind, reason, field string }{
		{"author", "readback-mismatch", "author"}, {"body", "readback-mismatch", "body"},
		{"both", "readback-mismatch", "author,body"}, {"missing", "readback-not-found", ""},
		{"unavailable", "readback-unavailable", ""}, {"invalid", "invalid-accepted-record", ""},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := newReporterFixture(t)
			r := f.reporter()
			r.afterPost = func() {
				m := &f.chat.msgs[len(f.chat.msgs)-1]
				switch tc.kind {
				case "author":
					m.AuthorWorkerID = "SECRET-AUTHOR"
				case "body":
					m.Body = "SECRET-BODY"
				case "both":
					m.AuthorWorkerID = "SECRET-AUTHOR"
					m.Body = "SECRET-BODY"
				case "missing":
					f.chat.hideFromRead = true
				case "unavailable":
					f.chat.readErr = errors.New("SECRET-BODY SECRET-AUTHOR")
				case "invalid":
					f.chat.hideFromRead = true
				}
			}
			f.run(r)
			path := filepath.Join(f.box.Dir, ev1Base()+".sending")
			if tc.kind == "invalid" {
				b, _ := os.ReadFile(path)
				var v map[string]any
				_ = json.Unmarshal(b, &v)
				v["acceptedAuthorWorkerId"] = ""
				b, _ = json.Marshal(v)
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			for i := 0; i < 2; i++ {
				out := onlyOutcome(t, f.run(f.reporter()))
				b, _ := json.Marshal(out)
				var got map[string]any
				_ = json.Unmarshal(b, &got)
				if got["reason"] != tc.reason || (tc.field != "" && got["field"] != tc.field) {
					t.Fatalf("structured outcome %s, want %s/%s", b, tc.reason, tc.field)
				}
				if tc.field == "" {
					if _, ok := got["field"]; ok {
						t.Fatalf("unexpected field %s", b)
					}
				}
				if out.Result != "failed" || out.Seq != 2 || f.reported() || f.chat.attempts != 1 {
					t.Fatalf("delivery changed: %+v attempts%d", out, f.chat.attempts)
				}
				status, err := os.ReadFile(filepath.Join(f.box.Dir, ev1Base()+".lastfail"))
				if err != nil {
					t.Fatal(err)
				}
				for _, data := range [][]byte{b, status} {
					if bytes.Contains(data, []byte("SECRET-")) {
						t.Fatalf("secret disclosed %s", data)
					}
				}
				var cache map[string]any
				if err := json.Unmarshal(status, &cache); err != nil {
					t.Fatal(err)
				}
				if cache["reason"] != tc.reason || cache["lastObservedAt"] != f.clock.Format("2006-01-02T15:04:05Z07:00") || len(cache) != 6 {
					t.Fatalf("cache %s", status)
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatal("sending changed")
				}
			}
		})
	}
}

func TestUnrecordedOutcomeNoReason1675(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.failPost = true
	out := onlyOutcome(t, f.run(f.reporter()))
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), `"reason"`) {
		t.Fatalf("unrecorded reason %s", b)
	}
	if _, err := os.Stat(filepath.Join(f.box.Dir, ev1Base()+".lastfail")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrecorded cache: %v", err)
	}
}
