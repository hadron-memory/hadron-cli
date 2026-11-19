package orcaterm

import "strings"

// FilterProof is a diagnostic classification only; it never authorizes delivery.
// Reuse the notice patterns used by IdleProof, rather than matching conversation
// or terminal titles. A contiguous body plus idle prompt chrome is required.
func FilterProof(sc *Screen) (classification, note string) {
	if !sc.usable() {
		return "UNREADABLE", "read_failed"
	}
	// Use the same prompt window as IdleProof. A notice in conversation
	// history or a thread title below the prompt is not a current block.
	lines := sc.Tail
	if at := lastPromptLine(lines); at >= 0 {
		lines = lines[max(0, at-16):at]
	} else {
		lines = lines[max(0, len(lines)-16):]
	}
	header, body, strong := false, false, false
	for i, line := range lines {
		if noticeHeader.MatchString(line) {
			header = true
			for j := i + 1; j < len(lines) && matchesAny(noticeBody, squash(lines[j])); j++ {
				body = true
				strong = true
			}
		}
		if matchesAny(noticeBody, squash(line)) {
			body = true
		}
	}
	if !header && !body {
		return "NOT_BLOCKED", ""
	}
	for _, line := range sc.Tail {
		if strings.Contains(strings.ToLower(squash(line)), "esc to interrupt") {
			return "AMBIGUOUS", "turn_running"
		}
	}
	if strong {
		raw := []string{}
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				raw = append(raw, line)
			}
		}
		skip := recapBlockSkip(raw)
		i := len(raw) - 1
		for i >= 0 && (isWeakChrome(squash(raw[i])) || skip[i]) {
			i--
		}
		if i < 0 || !noticeHeader.MatchString(raw[i]) {
			return "AMBIGUOUS", "older_notice"
		}
		live, _ := IdleProof(sc.Tail, "codex")
		if live == Idle {
			return "BLOCKED", "provider_notice"
		}
		return "AMBIGUOUS", "idle_unproven"
	}
	if header {
		return "AMBIGUOUS", "header_only"
	}
	return "AMBIGUOUS", "body_only"
}
