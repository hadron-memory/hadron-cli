package hostpoller

import "testing"

func TestPollerLaunchReservationSerializesC3AndC52060(t *testing.T) {
	p := New(nil, nil, nil, nil, "", nil)
	release, err := p.reserveWorktreeLaunch("repo/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.reserveWorktreeLaunch("repo/w"); err == nil {
		t.Fatal("second start admitted")
	}
	other, err := p.reserveWorktreeLaunch("repo/other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	release, err = p.reserveWorktreeLaunch("repo/w")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
