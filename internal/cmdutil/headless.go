package cmdutil

import (
	"encoding/json"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ResolveAppRef resolves the App a headless-run command targets: the explicit
// --app flag when set, otherwise the configured/global App context (the same
// source `hadron app use` and the persistent --app write). Empty both ways is a
// usage error — every run/schedule/webhook command names an App. The ref is
// passed to the server verbatim, which dispatches an ID or a URN.
func ResolveAppRef(f *Factory, flag string) (string, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
		return flag, nil
	}
	app, err := f.App()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(app) == "" {
		return "", exitcode.Newf(exitcode.Usage, "an App is required — pass --app <ref> or set a default App context")
	}
	return app, nil
}

// CanonicalNodeURN validates and normalizes a fully-qualified entry-node URN
// for the headless-run surface (schedule/webhook/trigger entryNodeUrn). It is
// the no-network half of ResolveNodeURN: a scheme-prefixed ref (hrn:/urn:)
// passes through verbatim; a bare <org>::<memory>::<loc> gets the canonical
// hrn:node: prefix; anything without the two `::` separators is rejected as
// ambiguous. The result is a URN the server stores, not a node ID — the entry
// node is resolved at run time, not now.
func CanonicalNodeURN(ref string) (string, error) {
	urn := strings.TrimSpace(ref)
	if urn == "" {
		return "", exitcode.Newf(exitcode.Usage, "an entry node URN is required")
	}
	if strings.HasPrefix(urn, "hrn:") || strings.HasPrefix(urn, "urn:") {
		return urn, nil
	}
	// A full node URN is <org>::<memory>::<loc> — TWO `::` separators. A
	// single-colon ref whose loc carries its own colons has zero `::`, so it is
	// rejected as ambiguous rather than passed on (cf. ResolveNodeURN).
	if strings.Count(urn, "::") < 2 {
		return "", exitcode.Newf(exitcode.Usage,
			"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc> (e.g. hadronmemory.com::dev::tasks:nightly-digest), optionally hrn:node:-prefixed", ref)
	}
	return "hrn:node:" + urn, nil
}

// KeyValsToJSON assembles repeated key=value flags into a JSON object (e.g.
// `--arg k=v` → an eventData object, `--param k=v` → a provider-params object).
// Each value is sent as JSON when it parses as JSON (numbers, booleans, arrays,
// objects), otherwise as a string. Returns nil for no pairs so the variable is
// omitted, not sent as `{}`. `flag` names the source flag in the error.
func KeyValsToJSON(pairs []string, flag string) (*json.RawMessage, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	obj := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--%s must be key=value, got %q", flag, p)
		}
		var jv any
		if json.Unmarshal([]byte(v), &jv) == nil {
			obj[k] = jv
		} else {
			obj[k] = v
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(b)
	return &raw, nil
}

// ParseJSONArg parses a JSON-document flag (--policy, --args-schema). An empty
// string means "not provided" and returns nil so the variable is omitted; a
// non-empty value must be valid JSON. The `what` label names the flag in the
// error.
func ParseJSONArg(s, what string) (*json.RawMessage, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var probe any
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be valid JSON: %v", what, err)
	}
	raw := json.RawMessage(s)
	return &raw, nil
}
