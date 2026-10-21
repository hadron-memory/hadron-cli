package api

import (
	"context"
	"sync"
)

// ResponseWarning is one entry of a GraphQL response's `extensions.warnings`
// (server#2198/#2207): advice on an operation that SUCCEEDED, filed under the
// operation's response key (Path). Code and Text are the server's, verbatim;
// the code list is closed per server release and a client tolerates unknown
// ones.
type ResponseWarning struct {
	Path     string
	Code     string
	Text     string
	Register *WarningRegister // nil when the server sent none (a caller with no Agent)
}

// WarningRegister says where the App's register maps a warning's code:
// State is mapped (TaskLoc, TaskURN set), unmapped or unresolved.
type WarningRegister struct {
	State   string
	TaskLoc *string
	TaskURN *string
}

// WarningSink collects the warnings of the requests made with the context
// WithWarningSink returned. It belongs to that context alone: a request made
// with any other context never reaches it.
type WarningSink struct {
	mu   sync.Mutex
	list []ResponseWarning
}

type warningSinkKey struct{}

// WithWarningSink returns a context whose successful requests file their
// response warnings in the returned sink. Commands that never install one see
// no change.
func WithWarningSink(ctx context.Context) (context.Context, *WarningSink) {
	sink := &WarningSink{}
	return context.WithValue(ctx, warningSinkKey{}, sink), sink
}

// For returns the warnings filed under path (an operation's response key), in
// the server's order. It never returns nil.
func (s *WarningSink) For(path string) []ResponseWarning {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ResponseWarning{}
	for _, w := range s.list {
		if w.Path == path {
			out = append(out, w)
		}
	}
	return out
}

// collectWarnings files a successful response's extensions.warnings in the
// context's sink, if it has one. The decode is tolerant: a carrier that is
// not a list, or an entry without a string path, code and text, is skipped,
// and a register without a string state is dropped from its warning. Nothing
// here can fail the request.
func collectWarnings(ctx context.Context, extensions map[string]any) {
	sink, _ := ctx.Value(warningSinkKey{}).(*WarningSink)
	if sink == nil {
		return
	}
	entries, _ := extensions["warnings"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path, okPath := entry["path"].(string)
		code, okCode := entry["code"].(string)
		text, okText := entry["text"].(string)
		if !okPath || !okCode || !okText {
			continue
		}
		w := ResponseWarning{Path: path, Code: code, Text: text}
		if reg, _ := entry["register"].(map[string]any); reg != nil {
			if state, ok := reg["state"].(string); ok {
				w.Register = &WarningRegister{State: state, TaskLoc: optionalString(reg["taskLoc"]), TaskURN: optionalString(reg["taskUrn"])}
			}
		}
		sink.mu.Lock()
		sink.list = append(sink.list, w)
		sink.mu.Unlock()
	}
}

func optionalString(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}
