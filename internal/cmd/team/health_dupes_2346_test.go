package team

import (
	"fmt"
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

// The boolean is unchanged: for every state shape it equals the old definition
// (a non-empty sorted read) and the stated answer.
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
		{"many resets, one the worker's", resets2346(append(append([][2]string{}, many...), [2]string{"zz", "w1"})...), "w1", true},
		{"empty worker id matches nothing named", resets2346([2]string{"a1", "w1"}), "", false},
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
	hit := resets2346(append(append([][2]string{}, rows...), [2]string{"zz", "w1"})...)
	for name, state := range map[string]*hostpoller.State{"miss": miss, "hit": hit} {
		allocs := testing.AllocsPerRun(100, func() { _ = hasResetRecord(state, "w1") })
		if allocs != 0 {
			t.Fatalf("%s: hasResetRecord allocated %v times per call, want 0", name, allocs)
		}
	}
}

// The sorted read stays exactly where it is load-bearing: only this worker's
// resets, in descending resetId order (so workerResetting's first read is the
// max id and a first-read-only shortcut is killed every run, not by map luck).
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
