package hostpoller

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestTaskkillCommandConstruction2128(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := taskkillCommand(ctx, 4242)
	want := []string{"taskkill", "/PID", "4242", "/T", "/F"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("taskkill arguments=%q want=%q", cmd.Args, want)
	}
	if cmd.WaitDelay != time.Second {
		t.Errorf("WaitDelay=%s want=1s", cmd.WaitDelay)
	}
	if cmd.Cancel == nil {
		t.Fatal("CommandContext cancellation is missing")
	}
	// A cancelled supplied context must prevent a launch. Replace the executable
	// with this private test binary first: even a broken context mutant cannot
	// execute taskkill or target a PID. The mutant runs no tests and exits.
	cancel()
	cmd.Path = os.Args[0]
	cmd.Args = []string{os.Args[0], "-test.run=^$"}
	cmd.Err = nil // taskkill need not exist on the inspection host
	if err := cmd.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("supplied context not honored: %v", err)
	}
}
