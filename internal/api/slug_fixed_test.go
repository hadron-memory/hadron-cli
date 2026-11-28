package api

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func slugFixedGQL(class any) error {
	ext := map[string]any{"code": "APP_MEMORY_SLUG_FIXED"}
	if class != nil {
		ext["memoryClass"] = class
	}
	return gqlerror.List{&gqlerror.Error{Message: "keeps its slug", Extensions: ext}}
}

// cli#3242: WithSlugFixed's own contract, below the command tests.
func TestWithSlugFixed3242(t *testing.T) {
	plain := errors.New("mapped")
	if got := WithSlugFixed(nil, slugFixedGQL("personal")); got != nil {
		t.Errorf("a nil mapped stays nil: %v", got)
	}
	other := gqlerror.List{&gqlerror.Error{Message: "no", Extensions: map[string]any{"code": "FORBIDDEN"}}}
	if got := WithSlugFixed(plain, other); got != plain {
		t.Errorf("another code returns mapped as it was: %v", got)
	}
	if got := WithSlugFixed(plain, errors.New("transport")); got != plain {
		t.Errorf("a non-GraphQL error returns mapped as it was: %v", got)
	}
	for _, class := range []string{"personal", "private"} {
		got := WithSlugFixed(plain, slugFixedGQL(class))
		if SlugFixedMemoryClass(got) != class {
			t.Errorf("%s: class %q", class, SlugFixedMemoryClass(got))
		}
		var coded interface{ ServerCode() string }
		if !errors.As(got, &coded) || coded.ServerCode() != "APP_MEMORY_SLUG_FIXED" {
			t.Errorf("%s: the server code is carried", class)
		}
		if !errors.Is(got, plain) {
			t.Errorf("%s: the mapped error stays reachable (its exit class)", class)
		}
	}
	for _, class := range []any{"knowledge", "", 7, nil} {
		got := WithSlugFixed(plain, slugFixedGQL(class))
		if SlugFixedMemoryClass(got) != "" {
			t.Errorf("class %v must not be carried, got %q", class, SlugFixedMemoryClass(got))
		}
		if got == plain {
			t.Errorf("class %v: the refusal is still an APP_MEMORY_SLUG_FIXED one", class)
		}
	}
	if SlugFixedMemoryClass(plain) != "" || SlugFixedMemoryClass(nil) != "" {
		t.Error("a plain or nil error carries no class")
	}
}
