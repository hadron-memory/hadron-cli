package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type syncPullDTO struct {
	Memory           string         `json:"memory"`
	Operation        string         `json:"operation"`
	Clean            bool           `json:"clean"`
	SyncStatus       string         `json:"syncStatus"`
	SyncError        *string        `json:"syncError"`
	LastSyncedAt     *string        `json:"lastSyncedAt"`
	SyncReportStatus string         `json:"syncReportStatus"`
	SyncReport       *syncReportDTO `json:"syncReport"`
}

func newCmdSync(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "sync", Short: "Sync a memory from its configured repository"}
	var yes bool
	pull := &cobra.Command{
		Use: "pull <memoryRef>", Short: "Pull the repository's tree into a memory (destructive)",
		Long: `Pull mirrors the memory's configured source repository into the memory. The
repository is the source of truth: where a node differs, the repository's
version replaces the one in Hadron, and nodes the repository dropped are
deleted from the memory. The pull is applied as a whole or not at all.
There is no revision check: a pull overwrites edits made in Hadron since the
last sync. Encrypted memories cannot be synced.

A memory needs a source repository first. Set one with:

  hadron memory set <memory> --git-source https://github.com/<owner>/<repo>

(--git-source "" clears it.) The organization also needs the Hadron GitHub
App installed on that repository. A memory without a source, or whose
organization has no installation, is never synced: a memory the CLI reads
as having no source exits 2 with no sync request sent, and when the server
is the one refusing — a source it cannot see, or no installation — the
refusal is SYNC_NOT_ATTEMPTED (reason NO_SOURCE or NO_INSTALLATION, exit
1). hadron memory get <memory> --json shows the recorded source, the sync
status and the last sync time.

The pull asks for confirmation; --yes skips it and is required without a
terminal (otherwise exit 2).

Files the loader cannot take are skipped or held, and the result lists each
with its code: a protected loc, an undeclared role, an attestation refusal.
Fix what the code names — the file in the repository, or the memory's config
for a role — and pull again. A deferred entry or a held move is not a defect
to fix: the source changed while this pull was deciding, or a move could not
yet vacate its destination, and a later pull takes it by itself (a README
name collision recurs until the pair is renamed).

Outcomes: a clean pull prints "synced". Partial, unknown and unavailable
reports exit 0 with a warning and clean:false in --json. A non-OK status
exits 1 as not completed, and so does a failed report; a stored report is
history, not this pull's outcome, and a missing report without a sync
timestamp is never clean. Exit 7 means the sync may have happened: the failed
request is not retried, so use memory get to check the recorded state before
pulling again. --json:
{memory,operation:"pull",clean,syncStatus,syncError,lastSyncedAt,
syncReportStatus,syncReport}. URL credentials in sync errors are masked.`,
		Example: "  hadron memory sync pull acme.com:kb --yes\n  hadron memory sync pull hrn:mem:acme.com:kb --yes --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMemoryRef(args[0]); err != nil {
				return err
			}
			ref := cmdutil.CanonicalMemoryRef(args[0])
			// Canonicalize locally: confirmation MUST precede even a lookup request.
			if err := cmdutil.Confirm(f.IOStreams, yes, "Pull repository into memory "+quoteCapped(ref, 300)+"? The repository wins and overwrites Hadron edits."); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			preflight, err := gen.MemorySyncPrerequisites(cmd.Context(), client, ref)
			if err != nil {
				return syncPullError(err, ref, false)
			}
			// A null read cannot establish absence or authority: delegate to sync.
			// Presence only: never render, persist or echo the stored URL.
			// Encrypted rows always reach the server's writer/encryption gates,
			// even without source. Do not reproduce that refusal client-side.
			if preflight != nil && preflight.Memory != nil && !preflight.Memory.IsEncrypted && (preflight.Memory.Source == nil || *preflight.Memory.Source == "") {
				return exitcode.Newf(exitcode.Usage, "this memory has no configured repository; no sync mutation was sent")
			}
			resp, err := gen.SyncMemory(cmd.Context(), client, ref)
			if err != nil {
				return syncPullError(err, ref, true)
			}
			if resp == nil || resp.SyncMemory == nil || resp.SyncMemory.SyncStatus == "" {
				return exitcode.Newf(exitcode.Unavailable, "no usable answer; the sync may have happened; check `hadron memory get %s`", ref)
			}
			m := resp.SyncMemory
			// A successful GraphQL response can contain an untouched historical
			// row. Do not interpret its report as this invocation's outcome.
			if m.SyncStatus != "OK" {
				dto := syncPullDTO{Memory: ref, Operation: "pull", Clean: false, SyncStatus: string(m.SyncStatus), SyncError: maskSyncError(m.SyncError), LastSyncedAt: m.LastSyncedAt, SyncReportStatus: syncReportUnavailable}
				summary := "pull not completed (status " + output.SafeLine(string(m.SyncStatus), 100) + "); check memory get for the recorded state"
				fmt.Fprintln(f.IOStreams.ErrOut, "Warning: "+summary)
				if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					if _, err := fmt.Fprintln(w, summary); err != nil {
						return err
					}
					if dto.SyncError != nil {
						_, err := fmt.Fprintln(w, "stored sync error: "+quoteCapped(*dto.SyncError, 500))
						return err
					}
					return nil
				}); err != nil {
					return err
				}
				return exitcode.Silent(exitcode.Error)
			}
			report, status := parseSyncReport(m.LastSyncReport)
			failed := syncReportSaysFailed(m.LastSyncReport)
			dto := syncPullDTO{Memory: ref, Operation: "pull", SyncStatus: string(m.SyncStatus), SyncError: maskSyncError(m.SyncError), LastSyncedAt: m.LastSyncedAt, SyncReportStatus: status, SyncReport: report}
			dto.Clean = status == syncReportNone && m.LastSyncedAt != nil && *m.LastSyncedAt != "" && m.SyncError == nil
			summary := "synced; report unavailable"
			switch {
			case failed:
				summary = "sync failed"
			case dto.Clean:
				summary = "synced"
			case report != nil && report.Outcome == "partial":
				summary = fmt.Sprintf("synced with %d file(s) skipped or held", len(report.Entries))
			case report != nil && report.Outcome == "unknown":
				summary = "synced, but the platform could not certify that every file applied"
			}
			if !dto.Clean {
				fmt.Fprintln(f.IOStreams.ErrOut, "Warning: "+summary)
			}
			err = output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintln(w, summary)
				writeSyncBlock(w, memoryDetailDTO{SyncStatus: dto.SyncStatus, LastSyncedAt: dto.LastSyncedAt, SyncError: dto.SyncError, SyncReportStatus: status, SyncReport: report})
				return nil
			})
			if err != nil {
				return err
			}
			if failed {
				return exitcode.Silent(exitcode.Error)
			}
			return nil
		},
	}
	pull.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required without a terminal)")
	group.AddCommand(pull)
	group.AddCommand(newCmdSyncSetToken(f))
	return group
}

type syncSetTokenDTO struct {
	Memory    string  `json:"memory"`
	Operation string  `json:"operation"`
	TokenSet  bool    `json:"tokenSet"`
	ExpiresAt *string `json:"expiresAt"`
}

// newCmdSyncSetToken sets a memory's write-only source token (cli#2511). The
// token is read from a file or stdin only, never an argument; it is sent as a
// GraphQL variable and appears in no output, error or DTO.
func newCmdSyncSetToken(f *cmdutil.Factory) *cobra.Command {
	var tokenFile, expiresAt string
	cmd := &cobra.Command{
		Use: "set-token <memoryRef> [--token-file <path|@path|->] [--expires-at <rfc3339>]", Short: "Set a memory's write-only source token",
		Long: `Store an encrypted access token for the memory's source repository, for a
repository that is not reached through the organization's GitHub App. The
token is write-only: Hadron never shows it again, and there is no command that
reads it back.

Give the token with --token-file (a path, @path, or - for stdin), never as an
argument; without the flag, set-token reads piped stdin or prompts with input
hidden. Setting a token only stores it: it starts no pull and no push. Run
"hadron memory sync pull" separately.

--expires-at (an RFC 3339 time such as 2027-01-02T03:04:05Z) is passed to the
server along with the token; leave it out for none. An encrypted memory is
never synced, so the server may refuse to store a token for one; its answer is
shown. Setting a token needs write access to the memory, which the server
checks. The token is never printed: not in the output, --json or an error.
--json: {memory,operation:"set-token",tokenSet:true,expiresAt}.`,
		Example: "  printf %s \"$TOKEN\" | hadron memory sync set-token acme.com:kb\n  hadron memory sync set-token acme.com:kb --token-file ./kb.token --expires-at 2027-01-02T03:04:05Z",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMemoryRef(args[0]); err != nil {
				return err
			}
			ref := cmdutil.CanonicalMemoryRef(args[0])
			var exp *string
			if cmd.Flags().Changed("expires-at") {
				if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
					return exitcode.Newf(exitcode.Usage, "--expires-at must be an RFC 3339 time, e.g. 2027-01-02T03:04:05Z")
				}
				exp = &expiresAt
			}
			if cmd.Flags().Changed("token-file") && tokenFile == "" {
				return exitcode.Newf(exitcode.Usage, "--token-file requires a path or -")
			}
			token, err := cmdutil.ReadSecretInput(f.IOStreams, tokenFile, "token-file", "Source token", "source token")
			if err != nil {
				return api.MapError(err)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetMemorySourceToken(cmd.Context(), client, ref, token, exp)
			if err != nil {
				return setTokenError(err, token, f.JSON)
			}
			// A null field with no error cannot establish that a token is stored.
			if resp == nil || resp.SetMemorySourceToken == nil {
				return exitcode.Newf(exitcode.Unavailable, "no usable answer; the token may not be stored; check the memory and set it again")
			}
			// Name the memory as the server did; fall back to what was typed.
			// A urn that contains the token (raw or JSON-escaped) is not spliced
			// ("hrn:[REDACTED]e:..." for a very short token): the typed ref names
			// the memory instead. For a degenerate one-character token the typed
			// ref is what the user typed on their own command line, so "never
			// printed" is strict only for real tokens.
			named := ref
			if u := resp.SetMemorySourceToken.Urn; u != "" && !containsAnySpelling(u, token) {
				named = u
			}
			dto := syncSetTokenDTO{Memory: named, Operation: "set-token", TokenSet: true, ExpiresAt: exp}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "source token set for %s\n", quoteCapped(named, 300))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "read the token from a file (path or @path) or - for stdin; never an inline value")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "token expiry, RFC 3339 (omit for none)")
	return cmd
}

// setTokenError maps a refusal without ever carrying the token: the server's
// own answer is kept (an encrypted memory's refusal must reach the user), a
// server code that reflects the token — the literal token or one of its
// generated JSON spellings (cli#2704 Jack F2) — is dropped, and the message
// is only partially redacted when that provably writes the token nowhere
// (cli#2714 round 4). Otherwise a fixed refusal replaces the whole message:
// the scrubber's only replacement is the marker, and any replacement can
// itself (re)create token text. The refusal is itself output, so it is
// chosen by the same test — a fixed text that carries this token is never
// printed (cli#2714 round 5; see tokenSafeRefusal).
func setTokenError(err error, token string, jsonOut bool) error {
	out := setTokenErrorUnguarded(err, token, jsonOut)
	if token == "" {
		return out
	}
	// cli#2761: text a later layer appends (renderError's stored-credential
	// hint) is output too; it is judged by the same predicate as the message,
	// including the --json envelope form (cli#2777) that can re-form a spelling.
	return api.WithOutputGuard(out, func(candidate string) bool {
		return containsAnySpelling(candidate, token) || (jsonOut && envelopeReformsToken(candidate, token))
	})
}

func setTokenErrorUnguarded(err error, token string, jsonOut bool) error {
	mapped := api.MapError(err)
	if token == "" {
		// Nothing to redact, but the same cap and control-character guard as below.
		return api.WithServerCode(exitcode.Newf(exitcode.FromError(mapped), "%s", output.SafeLine(mapped.Error(), maxErrMsgRunes)), err)
	}
	// Partial redaction is refused whenever it cannot be proven safe (Eden's
	// round-4 ruling, cli#2714 Bruno P2 / Dara P2): the marker replacement
	// re-creates a token contained in it ("REDACTED" -> "[REDACTED]"), leaves
	// a token containing it untouched, re-forms a seam-spanning token
	// ("x[" in "xx[" -> "x[REDACTED]"), and a message carrying any covered
	// spelling is not scrubbed piecemeal at all — the fixed refusal carries
	// nothing from it. The candidate is judged in its FINAL rendered form
	// (round 5, Dara's BLIND delta note): output.SafeLine neutralises control
	// characters to a space and can append an ellipsis at the rune bound, and
	// either transform can join characters into a spelling the raw message
	// never carried (message "a\x00b", token "a b"). The code (and, in the
	// receipt path, the URN) is kept only when it itself carries no spelling.
	msg := output.SafeLine(redactToken(mapped.Error(), token), maxErrMsgRunes)
	// Under --json the message is JSON-ENCODED on stdout, which can re-form a
	// spelling the string never carried (cli#2777: token a\"b, message a"b ->
	// a\"b in the envelope bytes). The candidate is judged in that form too.
	if tokenOverlapsMarker(token) || containsAnySpelling(mapped.Error(), token) || containsAnySpelling(msg, token) ||
		(jsonOut && envelopeReformsToken(msg, token)) {
		safe := exitcode.Newf(exitcode.FromError(mapped), "%s", tokenSafeRefusal(token))
		if containsAnySpelling(api.ServerCodeOf(err), token) || (jsonOut && envelopeReformsToken(api.ServerCodeOf(err), token)) {
			return safe
		}
		return api.WithServerCode(safe, err)
	}
	safe := exitcode.Newf(exitcode.FromError(mapped), "%s", msg)
	// containsAnySpelling, not a redactToken(code) != code fixpoint: a
	// sentinel-equal token leaves redactToken the identity, so the fixpoint
	// form (main's cli#2671 shape) would keep a code carrying it.
	if code := api.ServerCodeOf(err); containsAnySpelling(code, token) || (jsonOut && envelopeReformsToken(code, token)) {
		return safe
	}
	return api.WithServerCode(safe, err)
}

// envelopeReformsToken reports whether s, JSON-encoded the way the --json
// failure envelope writes it (output.WriteJSON: no HTML escaping), carries the
// token or one of its spellings. The envelope encoder re-escapes quotes,
// backslashes and control characters, so text that is clean as a string can
// become the token's own spelling in the stdout bytes (cli#2777). The HTML-
// escaped form is checked as well, so a change to the encoder cannot open it.
func envelopeReformsToken(s, token string) bool {
	if token == "" || s == "" {
		return false
	}
	for _, escapeHTML := range [...]bool{false, true} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		if enc.Encode(s) != nil {
			continue
		}
		raw := strings.TrimSuffix(buf.String(), "\n")
		if containsAnySpelling(raw[1:len(raw)-1], token) {
			return true
		}
	}
	return false
}

// tokenOverlapsMarker reports containment in EITHER direction, nothing wider:
// the token contains the marker (a token holding the marker is left untouched
// by the replacement, so it stays in the output) or the marker contains the
// token (re-created inside every replacement, "REDACTED" -> "[REDACTED]"). It is deliberately not named for every replacement hazard:
// a seam-spanning token ("x[" over "xx[" -> "x[REDACTED]") is caught by the
// message arm of the drop guard instead, which is why both arms stand
// (cli#2714 round 4; rename per Dara's round-4 delta P3).
func tokenOverlapsMarker(token string) bool {
	return strings.Contains(token, redactionMarker) || strings.Contains(redactionMarker, token)
}

// The drop path's message is a cascade of fixed refusals, not one constant:
// any fixed text can itself carry the token (cli#2714 round 5, Bruno's
// round-4 delta P2: a token "server refused" sits inside "the server refused
// the source token"), so every tier is checked with the same predicate before
// it is used, and the last resort is no echo at all — the empty message
// provably carries no spelling of a non-empty token, which this path
// guarantees.
const (
	// fullTokenRefusal is the message whenever it provably carries no
	// spelling of the token.
	fullTokenRefusal = "the server refused the source token"
	// secondTokenRefusal shares no whole word with fullTokenRefusal, so a token
	// equal to a word of the first refusal usually still gets a readable
	// answer instead of silence. A shared SUBSTRING is not ruled out ("ed" is
	// in both), which is why tokenSafeRefusal tests this text against the
	// token before using it.
	secondTokenRefusal = "input declined"
)

// tokenSafeRefusal picks the drop path's message for this token: the full
// refusal, then a second fixed text sharing no word with it, then no echo at
// all (Eden's round-5 rule: the fixed refusal is the drop path's output, so
// the token check must hold against the refusal text too).
func tokenSafeRefusal(token string) string {
	for _, refusal := range [...]string{fullTokenRefusal, secondTokenRefusal, ""} {
		if !containsAnySpelling(refusal, token) {
			return refusal
		}
	}
	return "" // unreachable: no spelling of a non-empty token fits the empty text
}

// redactionMarker is what redactToken writes over every spelling of the
// token. Exported nowhere: a caller cannot choose the marker, because a
// token made of the marker's own text is exactly the case the marker cannot
// scrub (see setTokenError).
const redactionMarker = "[REDACTED]"

// containsAnySpelling reports whether s carries the token in any spelling
// redactToken covers: the literal or a JSON-escaped form (cli#2704 Jack F1/F2).
// A containment test, deliberately not a redaction fixpoint: a token equal to
// the sentinel "[REDACTED]" leaves redactToken the identity on every input,
// so leaning on redactToken(s, token) == s would accept token-carrying text.
func containsAnySpelling(s, token string) bool {
	if token == "" {
		return false
	}
	if strings.Contains(s, token) {
		return true
	}
	for _, esc := range escapedSpellings(token) {
		if strings.Contains(s, esc) {
			return true
		}
	}
	return false
}

// maxErrMsgRunes bounds a server answer shown in an error.
const maxErrMsgRunes = 2000

// redactToken replaces every occurrence of token in s, and of its JSON-escaped
// spellings (a quote or backslash in the token; with and without the HTML
// escaping of < > & that Go's encoder adds and a Node server's does not; and
// with Go's \u2028/\u2029 line-separator escaping undone the way a Node
// JSON.stringify writes it), which a server that echoes the token inside a
// JSON string would produce. An empty token is a no-op. Best effort: other
// encodings are not covered.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, redactionMarker)
	for _, esc := range escapedSpellings(token) {
		s = strings.ReplaceAll(s, esc, redactionMarker)
	}
	return s
}

// nodeJSONForm rewrites a Go-encoded JSON string body into the form Node's
// JSON.stringify writes: every \u2028/\u2029 escape becomes the raw character.
// It scans by whole JSON escape units — an escaped backslash (\\) is consumed
// as a unit, so the six characters \u2028 appearing as LITERAL text in the
// token (Go-escaped to \\\u2028) are left alone; only Go's own
// line-separator escapes are unfolded (cli#2704 Rook F1, cli#2714 Dara P2).
func nodeJSONForm(goEscaped string) string {
	var b strings.Builder
	b.Grow(len(goEscaped))
	for i := 0; i < len(goEscaped); {
		if goEscaped[i] == '\\' && i+1 < len(goEscaped) {
			next := goEscaped[i+1]
			if next == '\\' { // an escaped backslash: one unit, never the start of uXXXX
				b.WriteString(`\\`)
				i += 2
				continue
			}
			if rest := goEscaped[i:]; strings.HasPrefix(rest, `\u2028`) {
				b.WriteString(" ")
				i += 6
				continue
			} else if strings.HasPrefix(rest, `\u2029`) {
				b.WriteString(" ")
				i += 6
				continue
			}
		}
		b.WriteByte(goEscaped[i])
		i++
	}
	return b.String()
}

// escapedSpellings returns the JSON-string spellings of token that differ from
// it, without the surrounding quotes.
func escapedSpellings(token string) []string {
	var out []string
	add := func(esc string) {
		if esc != token && esc != "" {
			for _, have := range out {
				if have == esc {
					return
				}
			}
			out = append(out, esc)
		}
	}
	// json.Marshal of a string cannot fail.
	b, _ := json.Marshal(token)
	add(string(b[1 : len(b)-1]))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(token) == nil {
		raw := strings.TrimSuffix(buf.String(), "\n")
		inner := raw[1 : len(raw)-1]
		add(inner)
		// The Node form derives from the non-HTML spelling by whole escape
		// units: JSON.stringify leaves U+2028/U+2029 raw where Go escapes
		// them, and a literal backslash-u stays a literal backslash-u.
		add(nodeJSONForm(inner))
	}
	return out
}

type syncPullFailure struct {
	message string
	cause   error
}

func (e *syncPullFailure) Error() string { return e.message }
func (e *syncPullFailure) Unwrap() error { return e.cause }

func syncPullError(err error, ref string, attempted bool) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	message := mapped.Error()
	if api.HasErrorCode(err, "ENCRYPTED_GIT_SYNC_UNAVAILABLE") {
		code = exitcode.Usage
		message = "this memory is encrypted and cannot be synced with git"
	} else if !attempted {
		if code == exitcode.Unavailable {
			message = "could not check the memory sync configuration"
		}
		message += "; no sync mutation was sent"
	} else if code == exitcode.Unavailable {
		message = "no answer; the sync may have happened; check `hadron memory get " + ref + "` before retrying"
	} else if api.ServerCodeOf(err) == "" || api.HasErrorCode(err, "INTERNAL_SERVER_ERROR") {
		message += "; check `hadron memory get " + ref + "` for what was recorded"
	}
	message = *maskSyncError(&message)
	safe := output.SafeLine(message, 2000)
	if fault, ok := api.InternalFaultOf(mapped); ok {
		// Keep only the bounded request-id metadata, without a raw server cause.
		return &syncPullFailure{message: safe, cause: api.NewInternalFaultError(fault.RequestID, nil)}
	}
	return api.WithServerCode(exitcode.Newf(code, "%s", safe), err)
}
