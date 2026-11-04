package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// Memory.lastSyncReport (hadron-cli#1014; provider hadron-server#2366,
// src/integrations/github/syncReport.ts) is version-1 JSON text the server
// writes after every sync attempt, or null after a clean completed one. The
// CLI renders it and never repairs it: anything it cannot read is "report
// unavailable", never a clean or a failed claim of its own.

// syncReportDTO is the parsed v1 report in `memory get --json`.
type syncReportDTO struct {
	Version    int                  `json:"version"`
	Outcome    string               `json:"outcome"`
	RecordedAt string               `json:"recordedAt"`
	Entries    []syncReportEntryDTO `json:"entries"`
	Code       *string              `json:"code,omitempty"`
}

type syncReportEntryDTO struct {
	Kind    string  `json:"kind"`
	Loc     string  `json:"loc"`
	FromLoc *string `json:"fromLoc,omitempty"`
	Code    string  `json:"code"`
}

// syncReportStatus values: whether the server stored a report, and whether the
// CLI could read it.
const (
	syncReportRecorded    = "recorded"
	syncReportNone        = "none"
	syncReportUnavailable = "unavailable"
)

// parseSyncReport reads the stored string. A nil input is "none"; anything
// that is not a well-formed v1 report (bad JSON, another version, a non-object,
// an outcome or field outside v1, an omitted or null entries array) is
// "unavailable" with no report. Unknown
// entry kinds and codes are NOT a reason to refuse: the server may add
// vocabulary within v1, and those render as "other".
func parseSyncReport(raw *string) (*syncReportDTO, string) {
	if raw == nil {
		return nil, syncReportNone
	}
	var wire struct {
		Version    *int    `json:"version"`
		Outcome    *string `json:"outcome"`
		RecordedAt *string `json:"recordedAt"`
		Entries    *[]struct {
			Kind    *string `json:"kind"`
			Loc     *string `json:"loc"`
			FromLoc *string `json:"fromLoc"`
			Code    *string `json:"code"`
		} `json:"entries"`
		Code *string `json:"code"`
	}
	trimmed := bytes.TrimSpace([]byte(*raw))
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &wire) != nil {
		return nil, syncReportUnavailable
	}
	if wire.Version == nil || *wire.Version != 1 || wire.Outcome == nil || wire.RecordedAt == nil || wire.Entries == nil {
		return nil, syncReportUnavailable
	}
	switch *wire.Outcome {
	case "partial", "failed", "unknown":
	default:
		return nil, syncReportUnavailable
	}
	// recordedAt is printed bare, so it must be the timestamp it claims to be.
	if _, err := time.Parse(time.RFC3339Nano, *wire.RecordedAt); err != nil {
		return nil, syncReportUnavailable
	}
	rep := &syncReportDTO{
		Version:    1,
		Outcome:    *wire.Outcome,
		RecordedAt: *wire.RecordedAt,
		Entries:    []syncReportEntryDTO{},
		Code:       wire.Code,
	}
	for _, e := range *wire.Entries {
		if e.Kind == nil || e.Loc == nil || e.Code == nil {
			return nil, syncReportUnavailable
		}
		rep.Entries = append(rep.Entries, syncReportEntryDTO{Kind: *e.Kind, Loc: *e.Loc, FromLoc: e.FromLoc, Code: *e.Code})
	}
	return rep, syncReportRecorded
}

// Human labels for the v1 vocabulary (syncReport.ts). Anything else is shown
// quoted under "other".
var (
	syncKindLabels = map[string]string{
		"protected":            "protected",
		"object_type_rejected": "object type rejected",
		"undeclared_role":      "undeclared role",
		"invalid_include":      "invalid include",
		"deferred":             "deferred",
		"held_move":            "move held",
	}
	syncEntryCodes = map[string]bool{
		"PROTECTED_LOC": true, "PROTECTED_COMMENT": true, "PROTECTED_BOOT_PROMPT": true, "PROTECTED_DECISION": true,
		"OBJECT_TYPE_REJECTED": true, "ROLE_NOT_DECLARED": true,
		"INVALID_INCLUDE": true, "INCLUDE_CYCLE": true, "INCLUDE_TARGET_UNAVAILABLE": true,
		"ROW_CHANGED": true, "MOVE_HELD": true, "DESTINATION_PROTECTED": true,
	}
	syncReportCodes = map[string]bool{"SYNC_FAILED": true, "BAD_USER_INPUT": true}
)

const (
	syncEntryCap    = 50  // entries printed before "… and N more"
	syncStringCap   = 200 // runes of a loc / unknown kind or code
	syncErrorRunCap = 500 // runes of syncError
)

// quoteCapped renders a server-supplied string for a terminal: truncated to
// max runes (marked with a trailing …) and Go-quoted, so control and escape
// sequences print as escapes instead of acting on the terminal.
func quoteCapped(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return strconv.Quote(s)
	}
	return strconv.Quote(string(r[:max])) + "…"
}

func vocab(s string, known bool) string {
	if known {
		return s
	}
	return "other " + quoteCapped(s, syncStringCap)
}

func kindLabel(kind string) string {
	if l, ok := syncKindLabels[kind]; ok {
		return l
	}
	return vocab(kind, false)
}

// urlUserinfo matches the credentials part of a URL (`scheme://user:pass@`),
// up to the LAST @ before the host so an unencoded @ in a password is masked
// whole. syncError is a server-stored message; the server already replaces git
// failures with a fixed text, and this keeps any other error that quotes an
// authenticated remote from printing its token. Scheme-less scp-style remotes
// (`user:token@host:path`) are not masked: the mask covers URL remotes only.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s?#]+@`)

func maskSyncError(s *string) *string {
	if s == nil {
		return nil
	}
	m := urlUserinfo.ReplaceAllString(*s, "${1}[redacted]@")
	return &m
}

// writeSyncBlock prints the text-mode sync lines. The summary never claims
// more than the stored facts: a null report reads as clean only after a
// completed OK sync, and never for a memory that has not synced.
func writeSyncBlock(w io.Writer, dto memoryDetailDTO) {
	fmt.Fprintf(w, "  sync: %s\n", syncSummary(dto))
	last := "never"
	if dto.LastSyncedAt != nil {
		last = *dto.LastSyncedAt
	}
	fmt.Fprintf(w, "  sync status: %s, last synced %s\n", dto.SyncStatus, last)
	if rep := dto.SyncReport; rep != nil {
		fmt.Fprintf(w, "  sync report recorded: %s\n", rep.RecordedAt)
		for i, e := range rep.Entries {
			if i == syncEntryCap {
				fmt.Fprintf(w, "    … and %d more\n", len(rep.Entries)-syncEntryCap)
				break
			}
			from := ""
			if e.FromLoc != nil {
				from = " from " + quoteCapped(*e.FromLoc, syncStringCap)
			}
			fmt.Fprintf(w, "    - %s: %s%s (%s)\n", kindLabel(e.Kind), quoteCapped(e.Loc, syncStringCap), from, vocab(e.Code, syncEntryCodes[e.Code]))
		}
	}
	if dto.SyncError != nil {
		fmt.Fprintf(w, "  sync error: %s\n", quoteCapped(*dto.SyncError, syncErrorRunCap))
	}
}

func syncSummary(dto memoryDetailDTO) string {
	switch dto.SyncReportStatus {
	case syncReportUnavailable:
		return "report unavailable"
	case syncReportRecorded:
		rep := dto.SyncReport
		switch rep.Outcome {
		case "partial":
			return fmt.Sprintf("last sync committed with %d file(s) skipped or held", len(rep.Entries))
		case "failed":
			if rep.Code != nil {
				return "last sync failed (" + vocab(*rep.Code, syncReportCodes[*rep.Code]) + ")"
			}
			return "last sync failed"
		default: // "unknown"
			return "last sync completed but some skips could not be listed"
		}
	}
	// No stored report: the status and the sync time decide.
	switch {
	case dto.SyncStatus == "ERROR":
		return "last sync failed, no per-file report recorded"
	case dto.SyncStatus == "SYNCING":
		return "sync in progress, no per-file report recorded"
	case dto.LastSyncedAt == nil:
		return "never synced"
	case dto.SyncStatus == "PENDING":
		return "sync pending, no per-file report recorded"
	default:
		return "last sync completed, no per-file issues recorded"
	}
}

// hasSyncHistory: print the block only for a memory that syncs or has synced,
// so a plain memory's get output is unchanged.
func hasSyncHistory(dto memoryDetailDTO, rawReport *string) bool {
	return (dto.Source != nil && *dto.Source != "") || dto.LastSyncedAt != nil || rawReport != nil || dto.SyncError != nil
}
