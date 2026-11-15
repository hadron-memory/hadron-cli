package hostpoller

import "errors"

// PIDNamespaceIdentity is an nsfs device/inode pair. Its trust comes from the
// running host poller that captured it, never from a host-up caller's proc view.
type PIDNamespaceIdentity struct{ Device, Inode uint64 }

// HostPIDNamespace returns the startup snapshot of the host poller. Per the
// host launch contract, this poller is started outside agent unshare namespaces.
// A host-up adapter must read this through that running poller; constructing a
// new Poller inside the caller is not a substitute for that association.
func (p *Poller) HostPIDNamespace() (PIDNamespaceIdentity, bool) {
	if p == nil {
		return PIDNamespaceIdentity{}, false
	}
	n := p.pidNamespace
	return n, n.Device != 0 && n.Inode != 0
}
func verifyPIDNamespace(recorded PIDNamespaceIdentity, read func() (PIDNamespaceIdentity, error)) error {
	if recorded.Device == 0 || recorded.Inode == 0 || read == nil {
		return errors.New("RELAUNCH_UNPROVEN: host PID namespace is unknown")
	}
	current, err := read()
	if err != nil {
		return errors.New("RELAUNCH_UNPROVEN: caller PID namespace cannot be read")
	}
	if current != recorded {
		return errors.New("RELAUNCH_UNPROVEN: caller is outside the recorded host PID namespace")
	}
	return nil
}

// VerifyCallerPIDNamespace compares this process with a witness read through
// the already-running host poller. Never supply a snapshot captured in the
// caller: an inner namespace would then certify itself. No proc/1 read occurs.
func VerifyCallerPIDNamespace(recorded PIDNamespaceIdentity) error {
	return verifyPIDNamespace(recorded, currentPIDNamespace)
}
