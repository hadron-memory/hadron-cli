package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// capabilitiesNow is the clock the cache ages by (injectable for tests).
var capabilitiesNow = time.Now

// capabilitiesHTTP is the client GET /capabilities runs on: unauthenticated
// by design, and short-timeout — discovery must never hang a command that
// works fine without the answer.
var capabilitiesHTTP = &http.Client{Timeout: 5 * time.Second}

// capabilitiesCache holds one entry per server: the discovery body is fixed
// for the server's process (computed at mount, ETag-cached), so a CLI
// process may hold it for the response's own max-age and revalidate by ETag
// past that window — the endpoint is rate-limited per IP, and a scripted
// loop of commands would otherwise hammer it (cli#878's gate; Ada's
// per-process ruling, team chat #12500). Entries never leave this process.
var capabilitiesCache = struct {
	sync.Mutex
	byServer map[string]*capabilitiesEntry
}{byServer: map[string]*capabilitiesEntry{}}

type capabilitiesEntry struct {
	body      string
	etag      string
	fetchedAt time.Time
	maxAge    time.Duration
}

// Capabilities reads GET /capabilities — hadron-server#1925, mounted by
// #1930: which deployment switches are ON, readable before sign-in. The
// answer is `{"capabilities":[...]}` with ruled token words ("node-signing",
// "passkey-registration"); an off switch is OMITTED, never false, so a
// deployment with the switches off reads exactly like a build without the
// features. Unauthenticated, ETag-cached, rate-limited, and OUTSIDE GraphQL.
//
// Within one process the response is cached per server for its stated
// max-age; past the window it is revalidated with If-None-Match, where a 304
// means unchanged (and renews the window). Callers treat every failure —
// non-200 (an older server's plain 404 is the expected one), transport
// error, unparsable body — as "no capabilities": no caller may pay a refused
// follow-up round trip (e.g. a signature query an older server would
// reject) just because discovery failed.
func Capabilities(ctx context.Context, baseURL string) (map[string]bool, error) {
	url := strings.TrimRight(baseURL, "/") + "/capabilities"

	capabilitiesCache.Lock()
	entry := capabilitiesCache.byServer[url]
	if entry != nil && capabilitiesNow().Sub(entry.fetchedAt) < entry.maxAge {
		body := entry.body
		capabilitiesCache.Unlock()
		return parseCapabilitiesBody(body)
	}
	capabilitiesCache.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// No Authorization header: the endpoint is readable before sign-in by
	// design, and sending the token to an unauthenticated route would leak
	// a credential into a cacheable response path.
	if entry != nil && entry.etag != "" {
		req.Header.Set("If-None-Match", entry.etag)
	}
	resp, err := capabilitiesHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		// The body is fixed for the server's process: unchanged is the same
		// answer, and the 304 renews the window.
		capabilitiesCache.Lock()
		entry.fetchedAt = capabilitiesNow()
		capabilitiesCache.Unlock()
		return parseCapabilitiesBody(entry.body)
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("GET /capabilities: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	capabilitiesCache.Lock()
	capabilitiesCache.byServer[url] = &capabilitiesEntry{
		body:      string(body),
		etag:      resp.Header.Get("ETag"),
		fetchedAt: capabilitiesNow(),
		maxAge:    cacheMaxAge(resp.Header.Get("Cache-Control")),
	}
	capabilitiesCache.Unlock()
	return parseCapabilitiesBody(string(body))
}

// parseCapabilitiesBody parses the endpoint's fixed shape. Callers see any
// failure as "no capabilities", so a wrong-shaped body is an error, never a
// partial guess.
func parseCapabilitiesBody(body string) (map[string]bool, error) {
	var parsed struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, fmt.Errorf("GET /capabilities: %w", err)
	}
	out := make(map[string]bool, len(parsed.Capabilities))
	for _, c := range parsed.Capabilities {
		out[c] = true
	}
	return out, nil
}

// cacheMaxAge reads the response's own max-age directive; a response
// without one is not cached (the endpoint always sends it today, but the
// cache must not invent a window the server did not state).
func cacheMaxAge(cacheControl string) time.Duration {
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return 0
}
