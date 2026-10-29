package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// InternalServerErrorCode is the one generic code the server attaches to an
// internal fault (cor:api:260:01, hadron-server#2486). The fault's detail stays
// in the server log under the request id; a client keys on this code and the
// id, never on the text, and never echoes what the server wrote.
const InternalServerErrorCode = "INTERNAL_SERVER_ERROR"

// requestIDHeader carries the same id on every HTTP response.
const requestIDHeader = "X-Request-Id"

// requestIDPattern bounds what may be printed as a request id. The server mints
// a UUIDv4, but the id arrives from the network and lands in a terminal and in
// --json: anything outside this shape is treated as absent, never echoed.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// SanitizeRequestID returns id when it is safe to print, else "".
func SanitizeRequestID(id string) string {
	id = strings.TrimSpace(id)
	if requestIDPattern.MatchString(id) {
		return id
	}
	return ""
}

// InternalFaultMessage is the one sentence the CLI writes for an internal
// fault. There is no blank placeholder when the id is absent.
func InternalFaultMessage(requestID string) string {
	if requestID == "" {
		return "the server hit an internal error; no request id was returned"
	}
	return "the server hit an internal error (request id: " + requestID + "); report this id when you ask for help"
}

// InternalFault is the additive detail an internal-fault error carries.
type InternalFault struct {
	ServerCode string
	RequestID  string
}

type internalFaultError struct {
	fault InternalFault
	err   error
}

func (e *internalFaultError) Error() string { return InternalFaultMessage(e.fault.RequestID) }
func (e *internalFaultError) Unwrap() error { return e.err }

// NewInternalFaultError builds the exit-1 error for an internal fault. The
// message is CLI-written; cause stays on the chain for errors.As readers but is
// never rendered.
func NewInternalFaultError(requestID string, cause error) error {
	return exitcode.New(exitcode.Error, &internalFaultError{
		fault: InternalFault{ServerCode: InternalServerErrorCode, RequestID: SanitizeRequestID(requestID)},
		err:   cause,
	})
}

// InternalFaultOf reports the detail of an internal-fault error built by
// NewInternalFaultError (the --json envelope adds it additively).
func InternalFaultOf(err error) (InternalFault, bool) {
	var e *internalFaultError
	if errors.As(err, &e) {
		return e.fault, true
	}
	return InternalFault{}, false
}

// headerRequestIDError carries the X-Request-Id of a failed response along the
// error chain. genqlient's HTTPError and error list carry no headers.
type headerRequestIDError struct {
	id  string
	err error
}

func (e *headerRequestIDError) Error() string { return e.err.Error() }
func (e *headerRequestIDError) Unwrap() error { return e.err }

// withHeaderRequestID attaches the response header's id to an internal-fault
// err (no-op otherwise).
func withHeaderRequestID(err error, h http.Header) error {
	if err == nil || h == nil {
		return err
	}
	id := h.Get(requestIDHeader)
	if id == "" {
		return err
	}
	// Only an internal fault reads the header; every other failure keeps its
	// exact error shape.
	if _, ok := classifyInternalFault(err); !ok {
		return err
	}
	return &headerRequestIDError{id: id, err: err}
}

// classifyInternalFault reports an internal fault on a GraphQL error and the id
// to print. It discriminates on extensions.code alone: never on text, never on
// the HTTP status. The body id (extensions.requestId) beats the header.
func classifyInternalFault(err error) (string, bool) {
	for _, e := range graphQLErrors(err) {
		if extensionCode(e) != InternalServerErrorCode {
			continue
		}
		id, _ := e.Extensions["requestId"].(string)
		if id = SanitizeRequestID(id); id != "" {
			return id, true
		}
		var h *headerRequestIDError
		if errors.As(err, &h) {
			return SanitizeRequestID(h.id), true
		}
		return "", true
	}
	return "", false
}

// plainFaultReadCap bounds how much of a non-2xx plain HTTP body is read to look
// for a fault envelope. A real envelope is tiny; the cap only stops a hostile
// or broken endpoint from being buffered whole.
const plainFaultReadCap = 256 << 10

// OversizedBodyNote stands in for a body the CLI refused to read whole.
const OversizedBodyNote = "(response body too large to show)"

// ReadPlainHTTPFault reads a non-2xx plain HTTP body (bounded) and returns the
// leading bytes for the caller's own diagnostic (at most keep bytes) and the
// internal-fault error when the body is one (then no bytes).
//
// A body over the cap is never classified and never echoed: the code could sit
// anywhere past the cap, so a marker in the prefix proves nothing (it may be a
// typed refusal quoting the word), and any prefix could be fault text. The
// caller gets OversizedBodyNote instead.
func ReadPlainHTTPFault(h http.Header, r io.Reader, keep int) ([]byte, error) {
	body, _ := io.ReadAll(io.LimitReader(r, plainFaultReadCap+1))
	if len(body) > plainFaultReadCap {
		return []byte(OversizedBodyNote), nil
	}
	if fault := PlainHTTPInternalFault(h, body); fault != nil {
		return nil, fault
	}
	if len(body) > keep {
		body = body[:keep]
	}
	return body, nil
}

// PlainHTTPInternalFault inspects a non-2xx plain HTTP body (asset endpoints,
// OAuth) and, when it is an INTERNAL_SERVER_ERROR envelope (or an OAuth
// server_error with request_id), returns the CLI error for it. nil for
// every other body: typed refusals and gateway pages keep their own handling.
func PlainHTTPInternalFault(h http.Header, body []byte) error {
	var b struct {
		Code      any `json:"code"`
		Error     any `json:"error"`
		RequestID any `json:"requestId"`
		RequestId any `json:"request_id"`
	}
	if json.Unmarshal(body, &b) != nil {
		return nil
	}
	// Fields are any so a wrongly-typed id (a number, an object) cannot fail
	// the decode and send a fault body down the raw-diagnostic path.
	if b.Code != InternalServerErrorCode && b.Error != "server_error" {
		return nil
	}
	id, _ := b.RequestID.(string)
	id = SanitizeRequestID(id)
	if id == "" {
		alt, _ := b.RequestId.(string)
		id = SanitizeRequestID(alt)
	}
	if id == "" && h != nil {
		id = SanitizeRequestID(h.Get(requestIDHeader))
	}
	return NewInternalFaultError(id, nil)
}

type headerCaptureKey struct{}

// headerCapture is filled by the HTTP wrapper with the response headers of the
// request a MakeRequest call is making, so MakeRequest can attach X-Request-Id
// to a failure.
type headerCapture struct{ h http.Header }
