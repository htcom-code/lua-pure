// channel exposes a Go channel to Lua as userdata so two goroutines, each with
// its own LState, communicate by message passing. Only the goroutine-safe
// channel is shared; values are materialized (FromValue/ToValue) so no LState
// heap crosses goroutines.
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

type luaChan struct{ ch chan any }

// bindChan exposes c on L as a global `ch` with :send(v) and :recv().
func bindChan(L *luapure.LState, c *luaChan) {
	mt, _ := L.NewMetatable("Chan")
	m := luapure.NewTable()
	m.SetStr("send", luapure.NewGoFunc("send", func(L *luapure.LState) int {
		ch := L.CheckUserData(1, "Chan").(*luaChan)
		ch.ch <- luapure.FromValue(L.Arg(2)) // materialize a Go value, not a Value
		return 0
	}))
	m.SetStr("recv", luapure.NewGoFunc("recv", func(L *luapure.LState) int {
		ch := L.CheckUserData(1, "Chan").(*luaChan)
		L.Push(L.ToValue(<-ch.ch))
		return 1
	}))
	mt.SetStr("__index", m.Value())
	L.SetGlobal("ch", L.NewUserData(c, mt))
}

func main() {
	c := &luaChan{ch: make(chan any, 2)}

	send := luapure.NewState(luapure.WithOpenLibs())
	bindChan(send, c)
	recv := luapure.NewState(luapure.WithOpenLibs())
	bindChan(recv, c)

	done := make(chan int64)
	go func() {
		res, err := recv.DoString(`local a = ch:recv(); local b = ch:recv(); return a + b`, "=recv")
		if err != nil {
			panic(err)
		}
		done <- res[0].AsInt()
	}()

	if _, err := send.DoString(`ch:send(40); ch:send(2)`, "=send"); err != nil {
		panic(err)
	}
	fmt.Println(<-done) // 42
}
