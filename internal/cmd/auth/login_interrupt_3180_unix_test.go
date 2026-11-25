//go:build unix

package auth

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	authflow "github.com/hadron-memory/hadron-cli/internal/auth"
)

// Ctrl+C reaches the flow as context cancellation (so a code prompt restores
// the terminal before the process exits), and only while the flow runs.
func TestLoginInterruptCancelsTheFlow_3180(t *testing.T) {
	old := browserLogin
	t.Cleanup(func() { browserLogin = old })
	// A handler of the test's own keeps SIGINT from ending the test process
	// if the flow's were ever missing.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGINT)
	defer signal.Stop(guard)
	ready := make(chan struct{})
	browserLogin = func(ctx context.Context, _ authflow.LoginOptions) (*authflow.Token, error) {
		close(ready)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("SIGINT never reached the flow")
		}
	}
	go func() {
		<-ready
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	_, err := loginInterruptibly(context.Background(), authflow.LoginOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ctrl+C must cancel the flow's context, got %v", err)
	}
}
