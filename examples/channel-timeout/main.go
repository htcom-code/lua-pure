// channel-timeout shows the cancellable receive pattern: a Go callback that
// blocks on a channel reads L.Context() and selects on its Done() channel, so a
// SetContext deadline frees the goroutine instead of blocking forever. This is
// the recommended way to do blocking I/O in a callback — a plain <-ch would
// ignore the deadline (the VM only checks the context between instructions).
//
// On timeout recv raises a clean, position-prefixed error with L.RaiseError
// (PUC luaL_error), which the script catches with pcall — so a stuck receive
// surfaces as a normal Lua error instead of hanging.
package main

import (
	"context"
	"fmt"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

type luaChan struct{ ch chan any }

func bindChan(L *luapure.LState, c *luaChan) {
	mt, _ := L.NewMetatable("Chan")
	m := luapure.NewTable()
	m.SetStr("send", luapure.NewGoFunc("send", func(L *luapure.LState) int {
		L.CheckUserData(1, "Chan").(*luaChan).ch <- luapure.FromValue(L.Arg(2))
		return 0
	}))
	// recv blocks until a value arrives OR the state's context is done.
	m.SetStr("recv", luapure.NewGoFunc("recv", func(L *luapure.LState) int {
		ch := L.CheckUserData(1, "Chan").(*luaChan)
		ctx := L.Context()
		if ctx == nil {
			L.Push(L.ToValue(<-ch.ch))
			return 1
		}
		select {
		case v := <-ch.ch:
			L.Push(L.ToValue(v))
			return 1
		case <-ctx.Done():
			// Raise a clean, position-prefixed error the script can pcall.
			return L.RaiseError("recv timed out: %v", ctx.Err())
		}
	}))
	mt.SetStr("__index", m.Value())
	L.SetGlobal("ch", L.NewUserData(c, mt))
}

func main() {
	c := &luaChan{ch: make(chan any)} // unbuffered, no fixed sender
	L := luapure.NewState(luapure.WithOpenLibs())
	bindChan(L, c)

	// Case A: a value arrives within the deadline -> recv returns it.
	go func() { time.Sleep(20 * time.Millisecond); c.ch <- int64(42) }()
	ctxA, cancelA := context.WithTimeout(context.Background(), time.Second)
	defer cancelA()
	L.SetContext(ctxA)
	res, err := L.DoString(`return ch:recv()`, "=recvA")
	L.SetContext(nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("A: received", res[0].AsInt()) // A: received 42

	// Case B: nothing arrives -> the 50ms deadline frees the blocked recv, which
	// returns (nil, message) instead of hanging.
	ctxB, cancelB := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelB()
	L.SetContext(ctxB)
	start := time.Now()
	res, err = L.DoString(`local ok, err = pcall(function() return ch:recv() end); return ok, err`, "=recvB")
	L.SetContext(nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("B: ok=%v after %v (%s)\n",
		res[0].AsBool(), time.Since(start).Round(10*time.Millisecond), res[1].Str())
	// B: ok=false after 50ms (recvB:1: recv timed out: context deadline exceeded)
}
