package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"plain error", errors.New("boom"), Error},
		{"coded", New(NotFound, errors.New("missing")), NotFound},
		{"wrapped coded", fmt.Errorf("context: %w", New(AuthRequired, errors.New("nope"))), AuthRequired},
		{"silent", Silent(Conflict), Conflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromError(tt.err); got != tt.want {
				t.Errorf("FromError() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSilentIsDetectable(t *testing.T) {
	if !errors.Is(Silent(AuthRequired), ErrSilent) {
		t.Error("Silent errors must match ErrSilent")
	}
}
