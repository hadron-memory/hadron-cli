//go:build !linux

package hostpoller

func ValidateResetTarget(ResetIdleTarget) error { return ErrUnsupportedPlatform }
