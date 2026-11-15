//go:build linux

package hostpoller

import (
	"errors"
	"testing"
)

func TestPollerCapturesOwnPIDNamespace2060(t *testing.T) {
	current, err := currentPIDNamespace()
	if err != nil {
		t.Fatal(err)
	}
	p := New(nil, nil, nil, nil, "", nil)
	captured, ok := p.HostPIDNamespace()
	if !ok || captured != current {
		t.Fatalf("captured=%+v current=%+v known=%v", captured, current, ok)
	}
	p.pidNamespace = PIDNamespaceIdentity{}
	if _, ok := p.HostPIDNamespace(); ok {
		t.Fatal("zero snapshot claimed trusted")
	}
}
func TestPIDNamespaceProofRequiresKnownMatchingIdentity2060(t *testing.T) {
	host := PIDNamespaceIdentity{Device: 4, Inode: 123}
	for _, test := range []struct {
		name              string
		recorded, current PIDNamespaceIdentity
		err               error
		pass              bool
	}{
		{"same", host, host, nil, true},
		{"missing", PIDNamespaceIdentity{}, host, nil, false},
		{"different inode", host, PIDNamespaceIdentity{Device: 4, Inode: 124}, nil, false},
		{"different device", host, PIDNamespaceIdentity{Device: 5, Inode: 123}, nil, false},
		{"read error", host, host, errors.New("unreadable"), false},
		{"unknown caller", host, PIDNamespaceIdentity{}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyPIDNamespace(test.recorded, func() (PIDNamespaceIdentity, error) { return test.current, test.err })
			if (err == nil) != test.pass {
				t.Fatalf("pass=%v error=%v", test.pass, err)
			}
		})
	}
}
