package team

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// batchPageWithCursor is batchPage1 (one batch, hasNextPage true) ending at
// cursor, or the last page when cursor is "".
func batchPageWithCursor(cursor string) string {
	if cursor == "" {
		return batchPage2
	}
	return strings.Replace(batchPage1, `"endCursor":"c1"`, `"endCursor":"`+cursor+`"`, 1)
}

// #1186: a cursor cycle longer than one page (A, B, A) refuses the read on
// the third page, as the wake read does since #1183. Before, only a cursor
// equal to the previous one was refused, so the read paged until the fake's
// script ran out.
func TestBatchesRefuseAMultiPageCursorCycle(t *testing.T) {
	pages := []string{}
	for _, c := range []string{"A", "B", "A", "B", "A", "B"} {
		pages = append(pages, batchPageWithCursor(c))
	}
	s, calls := deliveryFake(t, map[string][]string{"DeliveryBatches": pages})
	got, err := s.Batches(context.Background())
	if err == nil || got != nil {
		t.Fatalf("got %+v err %v, want the cycle refused with nothing typed", got, err)
	}
	var ce *deliveryCallError
	if !errors.As(err, &ce) || ce.DeliveryCode() != "no-cursor" || ce.call != "read" {
		t.Fatalf("err %v, want delivery read failed: no-cursor", err)
	}
	if n := len(calls()); n != 3 {
		t.Fatalf("paged %d times through an A,B,A cursor cycle; want refusal after 3", n)
	}
}

// The control: distinct cursors over several pages still read to exhaustion.
func TestBatchesReadDistinctCursorsToExhaustion(t *testing.T) {
	pages := []string{}
	for _, c := range []string{"A", "B", "C", ""} {
		pages = append(pages, batchPageWithCursor(c))
	}
	s, calls := deliveryFake(t, map[string][]string{"DeliveryBatches": pages})
	got, err := s.Batches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || len(calls()) != 4 {
		t.Fatalf("got %d batches in %d reads, want 4 in 4", len(got), len(calls()))
	}
	for i, want := range []string{"", "A", "B", "C"} {
		var after string
		if raw, ok := calls()[i].vars["after"]; ok && string(raw) != "null" {
			after = strings.Trim(string(raw), `"`)
		}
		if after != want {
			t.Fatalf("read %d sent after=%q, want %q", i+1, after, want)
		}
	}
}
