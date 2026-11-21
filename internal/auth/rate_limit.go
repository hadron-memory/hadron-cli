package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// maxBackoffSeconds bounds a Retry-After the CLI will repeat to a person: a
// day. A larger or malformed value is not advice worth echoing.
const maxBackoffSeconds = 24 * 60 * 60

// tokenRateLimit recognises the typed refusal hadron-server#4659 gives a token
// request at admission: HTTP 429 with code RATE_LIMITED and/or the OAuth error
// temporarily_unavailable, Retry-After in seconds. It returns nil for anything
// else (an untyped 429 from a gateway, the typed body on another status), which
// keeps the generic failure path.
//
// The refusal is before the one-use code is redeemed, so nothing was issued and
// nothing is stored: it is a wait, not a rejected credential. The CLI gives the
// advice and stops — no retry of its own (a restarted flow would register a new
// client), no echo of the body (the description is the server's wording and
// carries nothing a person can act on).
func tokenRateLimit(status int, h http.Header, body []byte) error {
	if status != http.StatusTooManyRequests {
		return nil
	}
	var b struct {
		Code              string          `json:"code"`
		Error             string          `json:"error"`
		RetryAfterSeconds json.RawMessage `json:"retryAfterSeconds"`
	}
	if json.Unmarshal(body, &b) != nil || (b.Code != "RATE_LIMITED" && b.Error != "temporarily_unavailable") {
		return nil
	}
	secs, ok := parseBackoff(h.Get("Retry-After"))
	if !ok { // the header is the standard; the body's copy is the fallback
		secs, ok = parseBackoff(strings.TrimSpace(string(b.RetryAfterSeconds)))
	}
	wait := "a little while"
	if ok {
		wait = backoffPhrase(secs)
	}
	return exitcode.Newf(exitcode.Error,
		"the server is rate-limiting sign-in attempts right now (this is not a rejected credential, and nothing was signed in or stored); wait %s, then run `hadron auth login` again", wait)
}

// parseBackoff reads delta-seconds: digits only, 0 to a day. A date, a sign, a
// fraction or anything else is not usable.
func parseBackoff(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > maxBackoffSeconds {
		return 0, false
	}
	return n, true
}

func backoffPhrase(secs int) string {
	switch {
	case secs == 1:
		return "1 second"
	case secs < 120:
		return fmt.Sprintf("%d seconds", secs)
	case secs < 2*3600:
		return fmt.Sprintf("%d minutes", (secs+59)/60)
	default:
		return fmt.Sprintf("%d hours", (secs+3599)/3600)
	}
}
