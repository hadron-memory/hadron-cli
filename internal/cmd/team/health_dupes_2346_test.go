package team

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// cli#2346 — hasResetRecord answers a boolean and must not pay for the sorted
// slice resetsForWorker builds. The sorted order stays where it is load-bearing
// (workerResetting, the #2055 item-3 deterministic read order).

func resets2346(rows ...[2]string) *hostpoller.State {
	m := map[string]hostpoller.ResetRecord{}
	for _, r := range rows {
		m[r[0]] = hostpoller.ResetRecord{ResetID: r[0], WorkerID: r[1]}
	}
	return &hostpoller.State{Resets: m}
}

// populated2346 is a reset with EVERY field set, so a predicate that treated a
// populated record differently from a bare one (on the progress it had made, the
// relaunch flag, the processes it names) would show in the differential check.
// A ResetRecord has no "ended" field: whether a reset ended is the server's
// endedAt, read by workerResetting, not part of this question.
func populated2346(id, worker string) hostpoller.ResetRecord {
	return hostpoller.ResetRecord{
		ResetID: id, OperationID: "op-" + id, WorkerID: worker, Epoch: 3,
		Old: hostpoller.ProcessIdentity{PID: 4242, StartTime: "12345"}, New: &hostpoller.ProcessIdentity{PID: 4343, StartTime: "12399"},
		Checkpoint: "chk:" + id, Relaunching: true, LateStopMs: 5000,
	}
}

func statesOf2346(records ...hostpoller.ResetRecord) *hostpoller.State {
	m := map[string]hostpoller.ResetRecord{}
	for _, r := range records {
		m[r.ResetID] = r
	}
	return &hostpoller.State{Resets: m}
}

// The boolean is unchanged: for each state shape below, bare and fully
// populated records alike, it equals the old definition (a non-empty sorted
// read) and the stated answer.
func TestHasResetRecordAnswerIsUnchanged2346(t *testing.T) {
	many := make([][2]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, [2]string{fmt.Sprintf("r%02d", i), fmt.Sprintf("other-%d", i%5)})
	}
	for _, tc := range []struct {
		name   string
		state  *hostpoller.State
		worker string
		want   bool
	}{
		{"nil state", nil, "w1", false},
		{"nil resets", &hostpoller.State{}, "w1", false},
		{"empty resets", resets2346(), "w1", false},
		{"only another worker's resets", resets2346([2]string{"a1", "w2"}, [2]string{"b2", "w3"}), "w1", false},
		{"one match", resets2346([2]string{"a1", "w1"}), "w1", true},
		{"a match among others", resets2346([2]string{"a1", "w2"}, [2]string{"b2", "w1"}, [2]string{"c3", "w3"}), "w1", true},
		{"every reset is the worker's", resets2346([2]string{"a1", "w1"}, [2]string{"b2", "w1"}), "w1", true},
		{"many resets, none the worker's", resets2346(many...), "w1", false},
		{"many resets, one the worker's", resets2346(slices.Concat(many, [][2]string{{"zz", "w1"}})...), "w1", true},
		{"an empty worker id matches no named worker", resets2346([2]string{"a1", "w1"}), "", false},
		{"fully populated records of another worker only", statesOf2346(populated2346("a1", "w2"), populated2346("b2", "w3")), "w1", false},
		{"a fully populated record of the worker", statesOf2346(populated2346("a1", "w1")), "w1", true},
		{"a populated record among populated others", statesOf2346(populated2346("a1", "w2"), populated2346("b2", "w1"), populated2346("c3", "w3")), "w1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hasResetRecord(tc.state, tc.worker)
			if got != tc.want {
				t.Fatalf("hasResetRecord = %v, want %v", got, tc.want)
			}
			if old := len(resetsForWorker(tc.state, tc.worker)) > 0; got != old {
				t.Fatalf("hasResetRecord = %v but the old sorted-read definition says %v", got, old)
			}
		})
	}
}

// No allocation and no sort: the boolean read allocates nothing, on a miss (the
// whole map is walked) and on a hit alike. Red while it goes through the sorted
// slice resetsForWorker builds.
func TestHasResetRecordAllocatesNothing2346(t *testing.T) {
	rows := make([][2]string, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, [2]string{fmt.Sprintf("r%02d", i), fmt.Sprintf("other-%d", i%5)})
	}
	miss := resets2346(rows...)
	hit := resets2346(slices.Concat(rows, [][2]string{{"zz", "w1"}})...)
	for name, state := range map[string]*hostpoller.State{"miss": miss, "hit": hit} {
		allocs := testing.AllocsPerRun(100, func() { _ = hasResetRecord(state, "w1") })
		if allocs != 0 {
			t.Fatalf("%s: hasResetRecord allocated %v times per call, want 0", name, allocs)
		}
	}
}

// The sorted read stays exactly where it is load-bearing: only this worker's
// resets, in descending resetId order. That order puts the max id first, so a
// regression where workerResetting decides after only its first read is caught
// on every run (the #2055 item-3 pin) instead of only on the runs where Go's
// map iteration happens to start there.
func TestResetsForWorkerStaysSortedDescending2346(t *testing.T) {
	state := resets2346(
		[2]string{"a1", "w1"}, [2]string{"zz9", "w1"}, [2]string{"m5", "w1"}, [2]string{"b2", "w2"}, [2]string{"q7", "w2"},
	)
	got := resetsForWorker(state, "w1")
	want := []string{"zz9", "m5", "a1"}
	if len(got) != len(want) {
		t.Fatalf("want %d of w1's resets, got %d: %+v", len(want), len(got), got)
	}
	for i, id := range want {
		if got[i].ResetID != id || got[i].WorkerID != "w1" {
			t.Fatalf("position %d: want %s (w1), got %+v (all %+v)", i, id, got[i], got)
		}
	}
}

// The cost the change removes, measured (cli#2346): the early-return boolean
// against the old definition (a built and sorted slice, then len > 0), on a map
// of 200 resets where only the last-checked one matches. Run with
// go test ./internal/cmd/team -run '^$' -bench HasResetRecord2346 -benchmem.
func BenchmarkHasResetRecord2346(b *testing.B) {
	rows := make([][2]string, 0, 201)
	for i := 0; i < 200; i++ {
		rows = append(rows, [2]string{fmt.Sprintf("r%03d", i), fmt.Sprintf("other-%d", i%7)})
	}
	state := resets2346(slices.Concat(rows, [][2]string{{"zz", "w1"}})...)
	b.Run("early-return", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !hasResetRecord(state, "w1") {
				b.Fatal("lost the match")
			}
		}
	})
	b.Run("old-sorted-read", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if len(resetsForWorker(state, "w1")) == 0 {
				b.Fatal("lost the match")
			}
		}
	})
}
