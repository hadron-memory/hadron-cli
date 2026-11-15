package api

import (
	"context"
	"testing"
	"time"
)

func TestWithoutSessionPreservesCancellationAndParent2060(t *testing.T) {
	parent, cancel := context.WithTimeout(WithSession(context.Background(), "worker-session"), time.Minute)
	defer cancel()
	child := WithoutSession(parent)
	if sessionFrom(child) != "" || sessionFrom(parent) != "worker-session" {
		t.Fatal("session override corrupted parent or leaked")
	}
	want, _ := parent.Deadline()
	got, ok := child.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatal("deadline lost")
	}
	cancel()
	select {
	case <-child.Done():
	default:
		t.Fatal("cancellation lost")
	}
}
