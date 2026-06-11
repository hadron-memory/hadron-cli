package api

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func gqlErr(code string) error {
	return gqlerror.List{{Message: "boom", Extensions: map[string]any{"code": code}}}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitcode.OK},
		{"unauthenticated", gqlErr("UNAUTHENTICATED"), exitcode.AuthRequired},
		{"not found", gqlErr("NOT_FOUND"), exitcode.NotFound},
		{"node not found", gqlErr("NODE_NOT_FOUND"), exitcode.NotFound},
		{"bad input", gqlErr("BAD_USER_INPUT"), exitcode.Usage},
		{"validation", gqlErr("GRAPHQL_VALIDATION_FAILED"), exitcode.Usage},
		{"duplicate", gqlErr("DUPLICATE_APP_AGENT"), exitcode.Conflict},
		{"forbidden", gqlErr("FORBIDDEN"), exitcode.Error},
		{"no extension", gqlerror.List{{Message: "boom"}}, exitcode.Error},
		{"plain", errors.New("network down"), exitcode.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitcode.FromError(MapError(tt.err))
			if got != tt.want {
				t.Errorf("MapError() exit code = %d, want %d", got, tt.want)
			}
		})
	}
}
