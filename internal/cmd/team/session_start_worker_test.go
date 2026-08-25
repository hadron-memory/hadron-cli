package team

import (
	"encoding/json"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// sessionStartWorkerDTO strips the four fields the bind itself invalidates, and
// it does so by TWO mechanisms that are invisible to each other.
//
// The command-level test in internal/cmd pins the wire shape: the keys must be
// absent. But that test cannot fail on the nilling of the EMBEDDED activity
// pair, because the shadowed outer fields decide the JSON on their own — a
// mutation run showed exactly that, by reverting the reported bug and watching
// nothing go red. Left there, `dto.HasLiveSession = nil` would be a line that
// looks like a guard and has no input that reaches it.
//
// It is kept rather than deleted, because the two mechanisms answer different
// questions: the SHADOW decides omitted-vs-null, and the NILLING decides
// null-vs-a-stale-value, which is what a Go caller reading the embedded struct
// would see. This test is what makes the second one falsifiable.
func TestSessionStartWorkerDTOClearsWhatTheBindInvalidates(t *testing.T) {
	live, at := true, "2026-08-25T11:00:00Z"
	held, heldAt := "u-dara", "2026-08-20T09:00:00Z"
	got := sessionStartWorkerDTO(gen.WorkerFields{
		Id: "wkr1", Name: "Iris", AppId: "app1", AgentId: "agt1",
		CreatedAt: "2026-08-14T00:00:00Z",
		// The PRE-bind read: idle-or-otherwise, and held by somebody else.
		// Every one of these is about the moment before startSession ran.
		HasLiveSession: &live, LastActiveAt: &at,
		HeldByUserId: &held, HeldAt: &heldAt,
	})

	// The embedded values, which no JSON assertion can see.
	if got.workerDTO.HasLiveSession != nil || got.workerDTO.LastActiveAt != nil {
		t.Errorf("the embedded activity pair must be cleared, got hasLiveSession=%v lastActiveAt=%v",
			got.workerDTO.HasLiveSession, got.workerDTO.LastActiveAt)
	}
	// No `workerDTO.` qualifier here, and the asymmetry is the point: the hold
	// pair is NOT shadowed — it needs none, since workerDTO already tags it
	// omitempty — so this selector reaches the embedded field directly. The two
	// lines above must qualify, because an unqualified HasLiveSession would
	// read the shadowing field and assert nothing about the embedded one.
	if got.HeldByUserID != nil || got.HeldAt != nil {
		t.Errorf("the embedded hold pair must be cleared, got heldByUserId=%v heldAt=%v",
			got.HeldByUserID, got.HeldAt)
	}
	// The shadowing fields are the ones the wire sees, and they are nil too.
	if got.HasLiveSession != nil || got.LastActiveAt != nil {
		t.Errorf("the shadowing activity pair must be nil, got %v / %v", got.HasLiveSession, got.LastActiveAt)
	}

	// And the whole thing still serializes to a real worker rather than to an
	// empty object that would satisfy every assertion above for free.
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(blob, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(keys["name"]) != `"Iris"` {
		t.Fatalf("fixture check: the document must still carry the worker, got %s", blob)
	}
	for _, k := range []string{"hasLiveSession", "lastActiveAt", "heldByUserId", "heldAt"} {
		if raw, present := keys[k]; present {
			t.Errorf("%q must be omitted from the bind receipt, got %s", k, string(raw))
		}
	}
}
