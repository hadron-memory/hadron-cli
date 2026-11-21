package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestOutputGuard2761(t *testing.T) {
	base := errors.New("refused")
	guarded := WithOutputGuard(base, func(s string) bool { return strings.Contains(s, "secret") })
	if guarded.Error() != "refused" || !errors.Is(guarded, base) {
		t.Fatalf("the guard must not change Error() or the chain: %v", guarded)
	}
	if !OutputCarriesGuarded(guarded, "x secret y") || OutputCarriesGuarded(guarded, "clean") {
		t.Fatal("the guard must reject exactly what its predicate rejects")
	}
	// Found through a later wrapper, as renderError's hint wrapping would.
	if !OutputCarriesGuarded(fmt.Errorf("%w — hint", guarded), "secret") {
		t.Fatal("the guard must be found through wrapping")
	}
	if OutputCarriesGuarded(base, "secret") || WithOutputGuard(nil, func(string) bool { return true }) != nil || WithOutputGuard(base, nil) != base {
		t.Fatal("no guard rejects nothing; nil inputs return unchanged")
	}
}
