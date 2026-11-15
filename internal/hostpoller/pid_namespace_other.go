//go:build !linux

package hostpoller

import "errors"

func currentPIDNamespace() (PIDNamespaceIdentity, error) {
	return PIDNamespaceIdentity{}, errors.New("host PID namespace proof requires Linux")
}
