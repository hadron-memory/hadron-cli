package orca

import (
	"context"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostPlan1635UnresolvedTerminalIsIncompleteNotUnreachable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := discoverHostInventory(context.Background(), nil, func(context.Context, string, []string) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"terminals":[{"handle":"t","worktreePath":"` + missing + `"}]}}`), nil
	})
	if exitcode.FromError(err) != 1 {
		t.Fatalf("valid Orca with unresolved local path misclassified: %v", err)
	}
	// The public reason must name the resource class without leaking its path.
	if err == nil || strings.Contains(err.Error(), missing) {
		t.Fatalf("unsafe/missing diagnostic: %v", err)
	}
}
