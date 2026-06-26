package luapure

// Debug hook support (ldo.c luaD_hook / ldebug.c luaG_traceexec). Hooks are off
// unless debug.sethook installed one; the dispatch loop gates on L.hookMask so
// the common (no-hook) path pays only a single branch per instruction.

// fireHook calls the installed hook with (event, line). It is re-entrancy safe:
// while a hook runs, allowHook is false so nested calls fire nothing.
func (L *LState) fireHook(event string, line int) {
	if L.hook.IsNil() || !L.allowHook {
		return
	}
	L.allowHook = false
	saved := L.top
	L.top = L.scratchTop()
	lineV := Nil
	if line >= 0 {
		lineV = Int(int64(line))
	}
	// Tag the hook's own frame so debug.getinfo reports namewhat == "hook"
	// (PUC sets CIST_HOOK on the hook's CallInfo).
	L.pendingHookMark = true
	L.CallValue(L.hook, []Value{MkString(event), lineV}, 0)
	L.top = saved
	L.allowHook = true
}

// traceexec fires the count and line hooks for the instruction about to run at
// pc (already incremented past the instruction). Called only when hookMask != 0.
func (L *LState) traceexec(ci *callInfo, proto *Proto, pc int) {
	if L.hook.IsNil() || !L.allowHook {
		return
	}
	if L.hookMask&maskCount != 0 && L.hookCount > 0 {
		L.hookCdown--
		if L.hookCdown <= 0 {
			L.hookCdown = L.hookCount
			L.fireHook("count", -1)
		}
	}
	if L.hookMask&maskLine != 0 {
		// Fire on entry to the function (npci == 0), on a backward jump
		// (npci <= oldpc, e.g. a loop iteration even on the same source line),
		// or when the line changes — like PUC luaG_traceexec. npci is the
		// instruction just fetched (pc was advanced past it). Stripped code has
		// no line info (LineAt -1), so only the entry fire happens, with a nil
		// line (fireHook passes nil for line < 0) — db.lua's stripped line-hook
		// test. A non-entry synthetic line-0 instruction never fires.
		npci := pc - 1
		line := proto.LineAt(npci)
		stripped := len(proto.LineInfo) == 0
		if (stripped && npci == 0) || (line > 0 && (npci <= ci.lastHkPc || line != ci.lastLine)) {
			ci.lastLine = line
			L.fireHook("line", line)
		}
		ci.lastHkPc = npci
	}
}

// fireCallHook fires the call/tail-call hook on entry to a Lua frame.
func (L *LState) fireCallHook(ci *callInfo, tail bool) {
	ci.lastLine = -1
	ci.lastHkPc = -1
	if L.hookMask&maskCall == 0 {
		return
	}
	// Transfer info for getinfo "r": a Lua call hook reports the fixed
	// parameters (PUC luaD_hookcall passes ftransfer=1, ntransfer=numparams).
	ci.ftransfer = 1
	ci.ntransfer = 0
	if cl := L.stack[ci.fn].closure(); cl != nil && cl.isLua() {
		ci.ntransfer = int(cl.proto.NumParams)
	}
	if tail {
		L.fireHook("tail call", -1)
	} else {
		L.fireHook("call", -1)
	}
}

// fireReturnHook fires the return hook before a frame's nres results (sitting at
// L.top-nres .. L.top) are moved. Records the transfer range for getinfo "r":
// the results map to getlocal indices ftransfer .. ftransfer+nres-1, where
// getlocal index i reads stack[ci.fn+i] (PUC rethook).
func (L *LState) fireReturnHook(ci *callInfo, nres int) {
	if L.hookMask&maskRet != 0 {
		// ftransfer is a 1-based getlocal index (relative to ci.base, which a
		// vararg frame moves away from ci.fn+1), so the first result at L.top-nres
		// maps to index (firstres - base + 1).
		ci.ftransfer = (L.top - nres) - ci.base + 1
		ci.ntransfer = nres
		L.fireHook("return", -1)
	}
}
