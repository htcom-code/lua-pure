package luapure

// Function call and return mechanics (ldo.c) plus upvalue / to-be-closed
// management (lfunc.c). Stack slots are addressed by index; CallInfo frames are
// a simple linked list built per call (no reuse pool).

// precall prepares a call to the function at stack index funcIdx with nresults
// expected. It returns the new Lua frame to execute, or nil when the callee was
// a native function (already run, results in place). Mirrors luaD_precall.
// pushCI returns the call frame to use for a new call: it reuses the frame
// linked after the current one when a prior call left it there (PUC
// luaE_extendCI's ci->next reuse), otherwise allocates and links a fresh one.
// The returned frame still holds stale fields — the caller resets it in full
// (preserving only prev/next) before use.
func (L *LState) pushCI() *callInfo {
	cur := L.ci
	if cur != nil && cur.next != nil {
		return cur.next
	}
	ci := &callInfo{prev: cur}
	if cur != nil {
		cur.next = ci
	}
	return ci
}

// reset configures a (possibly reused) frame for a new call: it sets the
// per-call fields and zeros every transient one, leaving the prev/next reuse
// links untouched. Done field-by-field rather than via a struct literal so the
// call path does not pay for a whole-struct copy (duffcopy) — measured at ~9%
// of CPU on call-heavy workloads.
func (ci *callInfo) reset(fn, base, top, nresults int, status uint16) {
	ci.fn = fn
	ci.base = base
	ci.top = top
	ci.nresults = nresults
	ci.status = status
	ci.savedpc = 0
	ci.nextra = 0
	ci.nres = 0
	ci.lastLine = 0
	ci.lastHkPc = 0
	ci.ftransfer = 0
	ci.ntransfer = 0
}

func (L *LState) precall(funcIdx, nresults int) *callInfo {
	for {
		fn := L.stack[funcIdx]
		if fn.IsFunction() {
			cl := fn.closure()
			if cl.isLua() {
				p := cl.proto
				narg := L.top - funcIdx - 1
				nfix := int(p.NumParams)
				fsize := int(p.MaxStackSize)
				L.checkstack(fsize)
				ci := L.pushCI()
				ci.reset(funcIdx, funcIdx+1, funcIdx+1+fsize, nresults, 0)
				if L.pendingHookMark {
					ci.status |= cistHook
					L.pendingHookMark = false
				}
				if L.pendingFinMark {
					ci.status |= cistFin
					L.pendingFinMark = false
				}
				L.ci = ci
				for ; narg < nfix; narg++ { // complete missing fixed args
					L.stack[L.top] = Nil
					L.top++
				}
				if L.hookMask != 0 {
					L.fireCallHook(ci, false)
				}
				return ci
			}
			L.precallC(funcIdx, nresults, cl)
			return nil
		}
		funcIdx = L.tryCallTM(funcIdx)
	}
}

// precallC runs a native function frame to completion and returns its result
// count (precallC). Arguments are at funcIdx+1..top; results are pushed by the
// function and relocated to funcIdx by poscall.
func (L *LState) precallC(funcIdx, nresults int, cl *Closure) int {
	L.checkstack(luaMinStack)
	ci := L.pushCI()
	ci.reset(funcIdx, funcIdx+1, L.top+luaMinStack, nresults, cistC)
	if L.pendingHookMark {
		ci.status |= cistHook
		L.pendingHookMark = false
	}
	if L.pendingFinMark {
		ci.status |= cistFin
		L.pendingFinMark = false
	}
	L.ci = ci
	// PUC precallC fires the call hook for C functions too (the return hook
	// fires from poscall), so a debug hook sees C-function entry/exit. A C call
	// hook reports all arguments as the transfer (PUC ccall: ftransfer=1,
	// ntransfer=narg) for getinfo "r".
	if L.hookMask&maskCall != 0 {
		ci.ftransfer = 1
		ci.ntransfer = L.top - funcIdx - 1
		L.fireHook("call", -1)
	}
	n := cl.gofn(L)
	L.poscall(ci, n)
	return n
}

// tryCallTM inserts the __call metamethod below func and shifts the arguments
// up, returning the (unchanged) function index to retry (ldo.c tryfuncTM).
func (L *LState) tryCallTM(funcIdx int) int {
	tm := L.gettmByObj(L.stack[funcIdx], tmCall)
	if tm.IsNil() {
		L.callError(L.stack[funcIdx])
	}
	L.checkstack(1)
	for p := L.top; p > funcIdx; p-- {
		L.stack[p] = L.stack[p-1]
	}
	L.top++
	L.stack[funcIdx] = tm
	return funcIdx
}

// poscall finishes a call: moves nres results to the frame's func slot per the
// expected count and pops back to the caller (luaD_poscall).
func (L *LState) poscall(ci *callInfo, nres int) {
	if L.hookMask&maskRet != 0 {
		L.fireReturnHook(ci, nres)
	}
	L.moveresults(ci.fn, nres, ci.nresults)
	L.ci = ci.prev
}

func (L *LState) moveresults(res, nres, wanted int) {
	oldTop := L.top
	switch wanted {
	case 0:
		L.top = res
	case 1:
		if nres == 0 {
			L.stack[res] = Nil
		} else {
			L.stack[res] = L.stack[L.top-nres]
		}
		L.top = res + 1
	default:
		if wanted == multRet {
			wanted = nres
		}
		first := L.top - nres
		if nres > wanted {
			nres = wanted
		}
		for i := 0; i < nres; i++ {
			L.stack[res+i] = L.stack[first+i]
		}
		for i := nres; i < wanted; i++ {
			L.stack[res+i] = Nil
		}
		L.top = res + wanted
	}
	// Clear the slots this call vacated. PUC's GC scans only up to L->top, so
	// stale pointers above it are harmless there; Go scans the whole stack
	// backing array, so a dead slot keeps its object alive (and would defeat
	// weak tables / __gc finalizers) unless we nil it.
	for i := L.top; i < oldTop; i++ {
		L.stack[i] = Nil
	}
}

// pretailcall reuses the current frame ci for a tail call. It returns -1 for a
// Lua callee (ci reconfigured, caller restarts execution) or the native result
// count otherwise (luaD_pretailcall).
func (L *LState) pretailcall(ci *callInfo, funcIdx, narg1, delta int) int {
	for {
		fn := L.stack[funcIdx]
		if fn.IsFunction() {
			cl := fn.closure()
			if cl.isLua() {
				p := cl.proto
				fsize := int(p.MaxStackSize)
				nfix := int(p.NumParams)
				L.checkstack(fsize)
				ci.fn -= delta // restore real func slot (vararg frames)
				for i := 0; i < narg1; i++ {
					L.stack[ci.fn+i] = L.stack[funcIdx+i]
				}
				funcIdx = ci.fn
				for ; narg1 <= nfix; narg1++ {
					L.stack[funcIdx+narg1] = Nil
				}
				ci.base = ci.fn + 1
				ci.top = funcIdx + 1 + fsize
				ci.savedpc = 0
				ci.nextra = 0
				ci.status |= cistTail
				L.top = funcIdx + narg1
				if L.hookMask != 0 {
					L.fireCallHook(ci, true)
				}
				return -1
			}
			return L.precallC(funcIdx, multRet, cl)
		}
		funcIdx = L.tryCallTM(funcIdx)
		narg1++
	}
}

// call invokes the function at funcIdx (args already pushed) collecting nresults
// results, running any Lua frame to completion (ldo.c ccall / luaD_call).
// MaxCCalls (luaconf.go) bounds nested Go-level calls.
func (L *LState) call(funcIdx, nresults int) {
	if L.nCcalls >= MaxCCalls {
		L.runtimeError("C stack overflow")
	}
	L.nCcalls++
	defer func() { L.nCcalls-- }()
	ci := L.precall(funcIdx, nresults)
	if ci != nil {
		ci.status |= cistFresh
		L.execute(ci)
	}
}

// pcall runs call() under recover, turning a raised luaError into a returned
// error and restoring the frame/stack to the pre-call state.
func (L *LState) pcall(funcIdx, nresults int) (err error) {
	savedCI := L.ci
	savedTop := L.top
	savedOpen := len(L.openuv)
	savedTBC := len(L.tbc)
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(*luaError)
			if !ok {
				panic(r)
			}
			// Unwind: close any upvalues/tbc opened by the failed call, then
			// restore the caller's frame and stack.
			L.closeUpvals(funcIdx)
			if savedOpen <= len(L.openuv) {
				// already trimmed by closeUpvals
			}
			L.ci = savedCI
			L.top = savedTop
			if savedTBC < len(L.tbc) {
				L.tbc = L.tbc[:savedTBC]
			}
			err = le
		}
	}()
	L.call(funcIdx, nresults)
	return nil
}

// --- upvalues (lfunc.c) ---

// findupval returns the open upvalue aliasing stack slot level, creating one if
// none exists yet (luaF_findupval).
func (L *LState) findupval(level int) *Upvalue {
	for _, uv := range L.openuv {
		if uv.isOpen() && uv.level() == level {
			return uv
		}
	}
	uv := &Upvalue{l: L, idx: level}
	L.openuv = append(L.openuv, uv)
	return uv
}

// closeUpvals closes (detaches from the stack) every open upvalue at or above
// level, dropping them from the open list (luaF_closeupval).
func (L *LState) closeUpvals(level int) {
	kept := L.openuv[:0]
	for _, uv := range L.openuv {
		if uv.isOpen() && uv.level() >= level {
			uv.close()
		} else {
			kept = append(kept, uv)
		}
	}
	L.openuv = kept
}

// --- to-be-closed variables (lfunc.c) ---

// newtbcupval registers the variable at stack slot level as to-be-closed,
// validating that it has a __close metamethod (luaF_newtbcupval).
func (L *LState) newtbcupval(level int) {
	v := L.stack[level]
	if v.IsFalsy() {
		return // nil/false need no closing
	}
	if L.gettmByObj(v, tmClose).IsNil() {
		// Name the offending variable from debug info (PUC checkclosemth via
		// luaG_findlocal): local number is register+1.
		name := "?"
		if L.ci != nil && L.ci.isLuaFrame() {
			if cl := L.stack[L.ci.fn].closure(); cl != nil && cl.isLua() {
				if n := localName(cl.proto, level-L.ci.base+1, L.ci.savedpc-1); n != "" {
					name = n
				}
			}
		}
		L.runtimeError("variable '%s' got a non-closable value", name)
	}
	L.tbc = append(L.tbc, level)
}

// closeTBC runs the __close metamethods for to-be-closed variables at or above
// level, in reverse (innermost-first) order. Each handler receives the current
// pending error (or nil). A handler that raises replaces the pending error and
// that new error is passed to the remaining handlers and finally re-raised —
// PUC luaD_closeprotected threads the error status through the whole sequence
// rather than aborting on the first __close error.
func (L *LState) closeTBC(level int, errobj Value) {
	var pending *luaError
	if !errobj.IsNil() {
		pending = &luaError{value: errobj}
	}
	if pending = L.runCloses(level, pending); pending != nil {
		// Re-raise the existing error object: any xpcall handler already ran at
		// the __close's error point, so re-panic it directly (not via throw) to
		// avoid applying the handler a second time.
		panic(pending)
	}
}

// runCloses runs the __close handlers for TBC vars at or above level, innermost
// first, threading the pending error (nil = none) through them. It returns the
// final pending error without raising — closeTBC re-raises it; the pcall unwind
// path turns it into the pcall result.
func (L *LState) runCloses(level int, pending *luaError) *luaError {
	errobj := Nil
	if pending != nil {
		errobj = pending.value
	}
	for len(L.tbc) > 0 && L.tbc[len(L.tbc)-1] >= level {
		idx := L.tbc[len(L.tbc)-1]
		L.tbc = L.tbc[:len(L.tbc)-1]
		v := L.stack[idx]
		// Re-fetch __close at close time and call it even if now nil/removed:
		// PUC prepclosingmethod calls whatever it finds, so a removed metamethod
		// raises "attempt to call a nil value (metamethod 'close')".
		tm := L.gettmByObj(v, tmClose)
		if le, raised := L.callClose(tm, v, errobj); raised {
			pending, errobj = le, le.value
		}
	}
	return pending
}

// callClose invokes one __close metamethod protected, returning the error it
// raised (if any) so closeTBC can thread it through the remaining handlers. The
// returned *luaError has already passed through any active message handler.
func (L *LState) callClose(tm, v, errobj Value) (le *luaError, raised bool) {
	savedTop := L.top
	savedCI := L.ci
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*luaError)
			if !ok {
				panic(r)
			}
			L.ci = savedCI
			L.top = savedTop
			le, raised = e, true
		}
	}()
	fi := L.top
	L.push(tm)
	L.push(v)
	L.push(errobj)
	L.call(fi, 0)
	L.top = savedTop
	return nil, false
}

// closeAll closes upvalues then to-be-closed variables at or above level
// (luaF_close), the OP_CLOSE / return path.
func (L *LState) closeAll(level int, errobj Value) {
	L.closeUpvals(level)
	L.closeTBC(level, errobj)
}
