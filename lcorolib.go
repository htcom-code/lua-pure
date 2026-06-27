package luapure

import "fmt"

// The coroutine library (lcorolib.c) on a goroutine-per-coroutine model: each
// coroutine runs its Lua code in a dedicated goroutine, and resume/yield hand
// control back and forth over unbuffered channels so exactly one goroutine is
// ever active. Because a yield merely blocks the coroutine's goroutine (its Go
// call stack is preserved), yielding works across any frame — including pcall
// and metamethods — which is broader than PUC's C-boundary restriction.

func (L *LState) OpenCoroutine() {
	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"create":      coCreate,
		"resume":      coResume,
		"yield":       coYieldFn,
		"status":      coStatus,
		"wrap":        coWrap,
		"running":     coRunningFn,
		"isyieldable": coIsYieldable,
		"close":       coClose,
	})
	L.registerTable("coroutine", t)
}

// newThread creates a suspended coroutine sharing this VM's global tables.
func (L *LState) newThread() *LState {
	return &LState{
		stack:     make([]Value, luaMinStack*2+extraStack),
		globals:   L.globals,
		registry:  L.registry,
		stringMT:  L.stringMT,
		pkgLoaded: L.pkgLoaded,
		pkgTable:  L.pkgTable,
		basicMT:   L.basicMT,
		allowHook: true,
		errReg:    -1,
		errUpval:  -1,
		co:        L.co,
		rng:       L.rng,
		status:    coSuspended,
		resumeCh:  make(chan []Value),
		yieldCh:   make(chan coMsg),
	}
}

func (L *LState) checkThread(n int) *LState {
	v := L.Arg(n)
	if v.IsThread() {
		return v.threadv()
	}
	L.typeArgError(n, "coroutine")
	return nil
}

func coCreate(L *LState) int {
	f := L.Arg(1)
	if !f.IsFunction() {
		L.typeArgError(1, "function")
	}
	co := L.newThread()
	co.stack[0] = f // the body sits at the base of the coroutine's stack
	co.top = 1
	L.Push(mkThread(co))
	return 1
}

// coRun is the goroutine body for a coroutine: it calls the stored function and
// reports the outcome (return values or a raised error) back to the resumer.
func (co *LState) coRun(args []Value) {
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(*luaError)
			if !ok {
				le = &luaError{value: MkString(fmt.Sprint(r))}
			}
			// An error unwinding a coroutine closes its to-be-closed variables
			// (PUC: lua_resume's error path runs luaF_close), threading any error
			// a __close raises.
			if len(co.tbc) > 0 {
				if final := co.runCloses(0, le); final != nil {
					le = final
				}
			}
			// Remember the error so coroutine.close reports it once (PUC keeps it
			// on the dead thread until the thread is reset/closed).
			co.deathErr = le
			co.yieldCh <- coMsg{kind: coErrorMsg, err: le}
		}
	}()
	for _, a := range args {
		co.push(a)
	}
	co.call(0, multRet) // function is at stack[0]
	results := make([]Value, co.top)
	copy(results, co.stack[:co.top])
	co.yieldCh <- coMsg{kind: coReturnMsg, vals: results}
}

// resume drives coroutine co with args, returning its yielded/returned values
// or the error it raised.
func (L *LState) resume(co *LState, args []Value) ([]Value, *luaError) {
	if co.status == coDead {
		return nil, &luaError{value: MkString("cannot resume dead coroutine")}
	}
	if co.status != coSuspended {
		return nil, &luaError{value: MkString("cannot resume non-suspended coroutine")}
	}
	co.status = coRunning
	L.status = coNormal
	// The resumed thread continues the resumer's C-call depth (PUC ccall passes
	// nCcalls from 'from'), so unbounded create-and-resume nesting hits the
	// LUAI_MAXCCALLS limit and raises "C stack overflow" instead of spawning
	// goroutines without end.
	co.nCcalls = L.nCcalls + 1
	if !co.started {
		co.started = true
		go co.coRun(args)
	} else {
		co.resumeCh <- args
	}
	m := <-co.yieldCh
	L.status = coRunning
	switch m.kind {
	case coYieldMsg:
		co.status = coSuspended
		return m.vals, nil
	case coReturnMsg:
		co.status = coDead
		return m.vals, nil
	default:
		co.status = coDead
		return nil, m.err
	}
}

func coResume(L *LState) int {
	co := L.checkThread(1)
	args := make([]Value, L.NArgs()-1)
	for i := range args {
		args[i] = L.Arg(i + 2)
	}
	vals, err := L.resume(co, args)
	if err != nil {
		L.Push(False)
		L.Push(err.value)
		return 2
	}
	// The results move onto the resumer's stack; if they would overflow it,
	// report it as a value rather than raising (PUC luaB_coresume checks
	// lua_checkstack(nres+1) and returns false + this message).
	if L.top+len(vals)+1 > MaxStack {
		L.Push(False)
		L.Push(MkString("too many results to resume"))
		return 2
	}
	L.Push(True)
	for _, v := range vals {
		L.Push(v)
	}
	return 1 + len(vals)
}

func coYieldFn(L *LState) int {
	if L.co == nil || L == L.co.mainThread {
		L.errorf("attempt to yield from outside a coroutine")
	}
	if L.noYield > 0 {
		// inside a non-yieldable library callback (e.g. a sort comparator)
		L.runtimeError("attempt to yield across a C-call boundary")
	}
	n := L.NArgs()
	vals := make([]Value, n)
	for i := 0; i < n; i++ {
		vals[i] = L.Arg(i + 1)
	}
	L.yieldCh <- coMsg{kind: coYieldMsg, vals: vals}
	in := <-L.resumeCh // suspend until the next resume
	for _, v := range in {
		L.Push(v)
	}
	return len(in)
}

func coStatus(L *LState) int {
	co := L.checkThread(1)
	var s string
	switch {
	case co == L:
		s = "running"
	case co.status == coSuspended:
		s = "suspended"
	case co.status == coNormal:
		s = "normal"
	case co.status == coDead:
		s = "dead"
	case co.status == coRunning:
		s = "running"
	}
	L.Push(MkString(s))
	return 1
}

func coWrap(L *LState) int {
	f := L.Arg(1)
	if !f.IsFunction() {
		L.typeArgError(1, "function")
	}
	co := L.newThread()
	co.stack[0] = f
	co.top = 1
	// The wrapper resumes co and re-raises any error instead of returning ok.
	wrapper := func(L *LState) int {
		args := make([]Value, L.NArgs())
		for i := range args {
			args[i] = L.Arg(i + 1)
		}
		vals, err := L.resume(co, args)
		if err != nil {
			// luaB_auxwrap: prepend the caller's position to a string error
			// before re-raising; a non-string error object propagates unchanged.
			ev := err.value
			if ev.IsString() {
				ev = MkString(L.where(1) + ev.Str())
			}
			L.throw(ev)
		}
		for _, v := range vals {
			L.Push(v)
		}
		return len(vals)
	}
	L.Push(NewGoFunc("wrapped", wrapper))
	return 1
}

func coRunningFn(L *LState) int {
	L.Push(mkThread(L))
	L.Push(Bool(L == L.co.mainThread))
	return 2
}

func coIsYieldable(L *LState) int {
	target := L
	if L.NArgs() >= 1 && L.Arg(1).IsThread() {
		target = L.Arg(1).threadv()
	}
	// Yieldable only in a non-main coroutine and not across a non-yieldable C
	// boundary (a library callback such as a gsub replacement; PUC's nCcalls
	// C-frame count).
	L.Push(Bool(target.co != nil && target != target.co.mainThread && target.noYield == 0))
	return 1
}

func coClose(L *LState) int {
	co := L.checkThread(1)
	if co.status == coRunning || co.status == coNormal {
		L.errorf("cannot close a %s coroutine", map[int]string{coRunning: "running", coNormal: "normal"}[co.status])
	}
	// lua_closethread: run the pending to-be-closed variables' __close handlers
	// (innermost first). The coroutine's goroutine is parked, so its stack is
	// stable and we can drive the handlers on it from here.
	var closeErr *luaError
	if len(co.tbc) > 0 {
		// Closing continues the caller's C-call depth, so a chain of coroutines
		// that close each other (each __close calling coroutine.close on the
		// next) hits the limit and reports "C stack overflow" (PUC bug #5.4.0).
		co.nCcalls = L.nCcalls + 1
		// Mark it running while its __close handlers execute, so a handler that
		// tries to close the same coroutine fails with "cannot close a running
		// coroutine" (PUC: the thread is the running one during the close).
		co.status = coRunning
		// Drop any message handler left on the coroutine's stack by a suspended
		// xpcall (PUC luaE_resetthread: "stack unwind can throw away the error
		// function"). Otherwise a __close error would be filtered through that
		// handler instead of being reported as-is (coroutine.lua's 5.4 bug test).
		co.errfunc = Nil
		co.inErrfunc = false
		func() {
			defer func() {
				if r := recover(); r != nil {
					le, ok := r.(*luaError)
					if !ok {
						le = &luaError{value: MkString(fmt.Sprint(r))}
					}
					closeErr = le
				}
			}()
			co.closeTBC(0, Nil)
		}()
	}
	co.status = coDead
	co.tbc = nil
	// A coroutine that died from an error reports that error on its first close
	// (PUC lua_closethread returns the thread's error status); a later close of
	// the now-clean thread succeeds.
	if closeErr == nil && co.deathErr != nil {
		closeErr = co.deathErr
	}
	co.deathErr = nil
	if closeErr != nil {
		L.Push(False)
		L.Push(closeErr.value)
		return 2
	}
	L.Push(True)
	return 1
}
