package orcaterm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// #2181: the list carries lastOutputAt, and says when it is not the whole
// roster. A truncated list is never read as "this terminal is gone".

func TestListReadsLastOutputAt2181(t *testing.T) {
	r := &recorder{replies: []string{
		`{"ok":true,"result":{"totalCount":3,"truncated":false,"terminals":[` +
			`{"handle":"a","lastOutputAt":1794735433200},` +
			`{"handle":"b","lastOutputAt":"1794735433201"},` +
			`{"handle":"c"}]}}`,
	}}
	terms, err := NewClient(r.run).List(context.Background())
	if err != nil || len(terms) != 3 {
		t.Fatalf("%+v %v", terms, err)
	}
	got := []string{terms[0].LastOutputAt, terms[1].LastOutputAt, terms[2].LastOutputAt}
	if want := []string{"1794735433200", "1794735433201", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lastOutputAt %q, want %q", got, want)
	}
}

func TestTruncatedListIsRelistedOnceWithTheTotal2181(t *testing.T) {
	r := &recorder{replies: []string{
		`{"ok":true,"result":{"totalCount":3,"truncated":true,"terminals":[{"handle":"a"},{"handle":"b"}]}}`,
		`{"ok":true,"result":{"totalCount":3,"truncated":false,"terminals":[{"handle":"a"},{"handle":"b"},{"handle":"c"}]}}`,
	}}
	terms, err := NewClient(r.run).List(context.Background())
	if err != nil || len(terms) != 3 {
		t.Fatalf("%+v %v", terms, err)
	}
	if len(r.calls) != 2 || !strings.Contains(strings.Join(r.calls[1], " "), "--limit 3") {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestAListThatStaysTruncatedIsAnErrorNeverARoster2181(t *testing.T) {
	trunc := `{"ok":true,"result":{"totalCount":9,"truncated":true,"terminals":[{"handle":"a"}]}}`
	r := &recorder{replies: []string{trunc, trunc}}
	terms, err := NewClient(r.run).List(context.Background())
	if !errors.Is(err, ErrOrca) || terms != nil {
		t.Fatalf("a still-truncated list must fail closed: %+v %v", terms, err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("one retry only: %v", r.calls)
	}
}

func TestAnUntruncatedListIsOneCall2181(t *testing.T) {
	r := &recorder{replies: []string{`{"ok":true,"result":{"totalCount":1,"truncated":false,"terminals":[{"handle":"a"}]}}`}}
	if _, err := NewClient(r.run).List(context.Background()); err != nil || len(r.calls) != 1 {
		t.Fatalf("%v %v", err, r.calls)
	}
}
