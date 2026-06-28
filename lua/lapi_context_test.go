package luapure_test

import (
	"context"
	"strings"
	"testing"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func TestContextGetter(t *testing.T) {
	L := luapure.NewState()
	if L.Context() != nil {
		t.Fatal("default Context should be nil")
	}
	ctx := context.Background()
	L.SetContext(ctx)
	if L.Context() != ctx {
		t.Fatal("Context should return what SetContext set")
	}
	L.SetContext(nil)
	if L.Context() != nil {
		t.Fatal("Context should be nil after SetContext(nil)")
	}
}

// The motivating use case: a Go callback reads L.Context() to make its own
// blocking wait cancellable, so a blocking call obeys the VM's deadline — the
// channel-recv pattern, now without the host wiring the context in by hand.
func TestContextCancellableCallback(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	block := make(chan struct{}) // never ready
	L.Register("wait", func(L *luapure.LState) int {
		ctx := L.Context()
		select {
		case <-block:
		case <-ctx.Done():
			L.ArgError(1, "cancelled: "+ctx.Err().Error())
		}
		return 0
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	L.SetContext(ctx)

	start := time.Now()
	_, err := L.DoString(`return wait()`, "=w")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("want cancellation error, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("callback not freed by the deadline (%v)", d)
	}
}

// A coroutine inherits the context, so a callback running inside it sees it.
func TestContextCoroutineInherits(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	seen := false
	L.Register("hasctx", func(L *luapure.LState) int {
		seen = L.Context() != nil
		return 0
	})
	L.SetContext(context.Background())
	_, err := L.DoString(`local co = coroutine.create(hasctx); coroutine.resume(co)`, "=co")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("coroutine callback did not see the inherited context")
	}
}
