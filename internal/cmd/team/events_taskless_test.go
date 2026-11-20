package team

import "testing"

// Every type the shared set names must render a subject in the list cell, so
// the helper and tasklessAlarmCell cannot drift apart (cli#2646).
func TestEveryTasklessAlarmTypeRenders(t *testing.T) {
	w := "01a1f7f90c497b1688299686596bf19c"
	name := "Jerry"
	for _, typ := range tasklessSubjectAlarmTypes {
		e := teamEventDTO{Type: typ, SubjectWorker: &taskPartyDTO{ID: w, Name: &name}}
		if got := tasklessAlarmCell(e); got == "—" {
			t.Errorf("%s is in the set but renders %q", typ, got)
		}
	}
	if got := tasklessAlarmCell(teamEventDTO{Type: "stall.finish-by", SubjectWorker: &taskPartyDTO{ID: w, Name: &name}}); got != "—" {
		t.Errorf("a type outside the set renders %q", got)
	}
}
