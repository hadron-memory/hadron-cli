package api

import (
	"encoding/json"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestSessionInputAbsentReportsToIsOmitted(t *testing.T) {
	raw, err := json.Marshal(gen.SessionInput{Id: "session"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reportsTo", "family"} {
		if _, present := fields[key]; present {
			t.Fatalf("unset %s sent explicitly: %s", key, raw)
		}
	}
}
