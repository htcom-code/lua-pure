package luapure

import (
	"runtime"
	"sync/atomic"
)

// __gc finalizers (PUC lgc.c GCTM / luaC_checkfinalizer).
//
// PUC runs an object's __gc metamethod on the main thread when the collector
// finds the object unreachable, passing the object itself (so it may be
// resurrected), once only, with errors demoted to warnings. luapure delegates
// liveness to Go's GC, whose finalizers run asynchronously on a separate
// goroutine where touching an LState is unsafe. So a Go finalizer here only
// enqueues the object under a mutex; the Lua main thread drains the queue and
// runs __gc synchronously at safe points (collectgarbage and the VM poll).
//
// Resurrection falls out for free: the queued Value holds a strong pointer, so
// the object is alive again until __gc has run and it is dropped from the queue.
// runtime.SetFinalizer is one-shot, matching PUC's once-only guarantee.

// checkFinalizer attaches a Go finalizer the first time a table or full
// userdata is given a metatable carrying __gc (PUC luaC_checkfinalizer, called
// from lua_setmetatable). The finalizer closure captures only the shared
// coState, never the object, so the object stays collectable.
func (L *LState) checkFinalizer(v Value) {
	co := L.co
	switch v.tag {
	case tagTable:
		t := v.tablev()
		if t.finReg || t.meta == nil || t.meta.rawgetStr("__gc").IsNil() {
			return
		}
		t.finReg = true
		runtime.SetFinalizer(t, func(p *Table) { co.enqueueFinalizer(mkTable(p)) })
	case tagUserData:
		u := v.userData()
		if u.finReg || u.meta == nil || u.meta.rawgetStr("__gc").IsNil() {
			return
		}
		u.finReg = true
		runtime.SetFinalizer(u, func(p *userData) { co.enqueueFinalizer(mkUserData(p)) })
	}
}

// enqueueFinalizer is called from a Go finalizer goroutine; it must not touch
// any LState, only the lock-protected queue.
func (co *coState) enqueueFinalizer(v Value) {
	co.finMu.Lock()
	co.finQueue = append(co.finQueue, v)
	co.finMu.Unlock()
	atomic.AddInt32(&co.finPending, 1)
}

// finGCPoll is how many VM instructions pass between finalizer polls. A Lua
// loop can spin on a finalizer side effect (gc.lua's `repeat u={} until finish`)
// without ever calling a function, so we poll between instructions to run any
// __gc the Go GC has queued on its background goroutine. The per-instruction
// cost is a plain counter increment; only every finGCPoll'th instruction do we
// touch the atomic, and only when something is actually queued do we drain.
const finGCPoll = 1024

// pollFinalizers is called from the VM dispatch prologue once every finGCPoll
// instructions. It drains any __gc that the background Go GC has queued.
// Returns true if it ran a finalizer (so the caller refreshes base). It does
// NOT force a collection: such spin loops allocate (the dead object's slot is
// cleared by moveresults, so it is genuinely unreachable), and Go's GC reclaims
// it under that allocation pressure — forcing a full GC here instead would tank
// every long-running test that merely holds an open file (__gc) handle.
// weakGCEvery is how many finalizer polls pass between forced collections while
// weak tables are live: pollFinalizers runs every finGCPoll instructions, so a
// nudge happens roughly every weakGCEvery*finGCPoll instructions. Coarse enough
// that weak-table programs keep most of their speed, frequent enough that a
// weak spin loop (closure.lua's `while x[1] do … end`) clears within a fraction
// of a second instead of waiting on the Go GC pacer.
const weakGCEvery = 32

func (L *LState) pollFinalizers() bool {
	drained := false
	if atomic.LoadInt32(&L.co.finPending) > 0 {
		L.drainFinalizers()
		drained = true
	}
	// While weak tables exist, periodically force a collection so referents
	// dropped inside a tight loop are actually reclaimed (the Go GC may not run
	// on its own in a CPU-bound bytecode loop). Gated + coarse: weak-free
	// programs never reach here.
	if atomic.LoadInt32(&liveWeakTables) > 0 {
		L.weakGCTick++
		if L.weakGCTick >= weakGCEvery {
			L.weakGCTick = 0
			runtime.GC()
		}
	}
	return drained
}

// drainFinalizers runs every queued __gc on the current (main-loop) thread,
// in reverse order of registration like PUC. Each call is balanced on the
// stack, so it is safe to invoke between VM instructions.
func (L *LState) drainFinalizers() {
	co := L.co
	for {
		co.finMu.Lock()
		n := len(co.finQueue)
		if n == 0 {
			co.finMu.Unlock()
			return
		}
		v := co.finQueue[n-1]
		co.finQueue = co.finQueue[:n-1]
		co.finMu.Unlock()
		atomic.AddInt32(&co.finPending, -1)
		L.runGCMetamethod(v)
	}
}

// runGCMetamethod invokes one __gc metamethod protected. A raised error is
// demoted (PUC luaE_warnerror "__gc"): finalizers never propagate errors.
func (L *LState) runGCMetamethod(v Value) {
	tm := L.gettmByObj(v, tmGC)
	if tm.IsNil() {
		return
	}
	savedTop := L.top
	savedCI := L.ci
	// Push above the running frame's registers, not at L.top: a poll fires
	// mid-instruction where L.top can sit below the live registers, so pushing
	// there would clobber them (callTM uses scratchTop for the same reason).
	funcIdx := L.scratchTop()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*luaError); ok {
				L.ci = savedCI
				L.top = savedTop
				return // swallow: __gc errors become warnings, never raise
			}
			panic(r)
		}
	}()
	L.top = funcIdx
	L.push(tm)
	L.push(v)
	L.pendingFinMark = true // tag the __gc frame so getinfo reports "metamethod"
	L.call(funcIdx, 0)
	L.top = savedTop
}

// finalizeAll forces Go collections and drains the finalizers they fire.
// collectgarbage() uses it to approximate PUC's synchronous finalization:
// SetFinalizer callbacks run on a separate goroutine shortly after runtime.GC(),
// so yield to let them enqueue before each drain, and loop so a finalizer that
// makes another object unreachable (a chain) is collected too.
func (L *LState) finalizeAll() {
	runtime.GC()
	runtime.Gosched()
	L.drainFinalizers()
}
