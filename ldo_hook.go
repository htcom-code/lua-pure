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
	// Run the hook non-yieldably so it executes in place rather than promoting a
	// synchronously-running coroutine (a debug hook set on a coroutine must fire
	// during that coroutine's run, like PUC's in-line hook dispatch).
	L.callNoYield(L.hook, []Value{MkString(event), lineV}, 0)
	L.top = saved
	L.allowHook = true
}

// traceexec fires the count and line hooks for the instruction about to run at
// pc (already incremented past the instruction). Called only when hookMask != 0.
func (L *LState) traceexec(ci *callInfo, proto *Proto, pc int) {
	if !L.allowHook {
		return
	}
	hasLua := !L.hook.IsNil()
	hasGo := L.goHook != nil
	if !hasLua && !hasGo {
		return
	}
	// --- count hook (Lua and Go keep independent countdowns) ---
	if hasLua && L.hookMask&maskCount != 0 && L.hookCount > 0 {
		L.hookCdown--
		if L.hookCdown <= 0 {
			L.hookCdown = L.hookCount
			L.fireHook("count", -1)
		}
	}
	if hasGo && L.goHookMask&maskCount != 0 && L.goHookCount > 0 {
		L.goHookCdown--
		if L.goHookCdown <= 0 {
			L.goHookCdown = L.goHookCount
			L.fireGoHook(HookCount, -1)
		}
	}
	// --- line hook ---
	wantLuaLine := hasLua && L.hookMask&maskLine != 0
	wantGoLine := hasGo && L.goHookMask&maskLine != 0
	if !wantLuaLine && !wantGoLine {
		return
	}
	// Fire on a backward jump (npci <= oldpc, e.g. a loop iteration even on
	// the same source line) or when the line changes — like PUC
	// luaG_traceexec. npci is the instruction just fetched (pc was advanced
	// past it). Stripped code has no line info (LineAt -1), so only the entry
	// fire happens, with a nil line (fireHook passes nil for line < 0) —
	// db.lua's stripped line-hook test. The fire condition is computed once and
	// dispatched to whichever hooks asked for line events.
	npci := pc - 1
	if npci == 0 && len(proto.Code) > 0 && GetOpCode(proto.Code[0]) == OP_VARARGPREP {
		// A vararg function's leading VARARGPREP never triggers a line hook;
		// PUC's OP_VARARGPREP handler instead sets oldpc = 1 so the *next*
		// instruction is seen as entering a new line. (The old code leaned on
		// VARARGPREP carrying line 0; it now carries luac-faithful line 1, so
		// it must be skipped explicitly.)
		ci.lastHkPc = 1
		return
	}
	line := proto.LineAt(npci)
	stripped := len(proto.LineInfo) == 0
	if (stripped && (npci == 0 || npci <= ci.lastHkPc)) ||
		(line > 0 && (npci <= ci.lastHkPc || line != ci.lastLine)) {
		ci.lastLine = line
		if wantLuaLine {
			L.fireHook("line", line)
		}
		if wantGoLine {
			L.fireGoHook(HookLine, line)
		}
	}
	ci.lastHkPc = npci
}

// fireCallHook fires the call/tail-call hook on entry to a Lua frame.
func (L *LState) fireCallHook(ci *callInfo, tail bool) {
	ci.lastLine = -1
	ci.lastHkPc = -1
	wantLua := L.hookMask&maskCall != 0 && !L.hook.IsNil()
	wantGo := L.goHook != nil && L.goHookMask&maskCall != 0
	if !wantLua && !wantGo {
		return
	}
	// Transfer info for getinfo "r": a Lua call hook reports the fixed
	// parameters (PUC luaD_hookcall passes ftransfer=1, ntransfer=numparams).
	ci.ftransfer = 1
	ci.ntransfer = 0
	if cl := L.stack[ci.fn].closure(); cl != nil && cl.isLua() {
		ci.ntransfer = int(cl.proto.NumParams)
	}
	if wantLua {
		if tail {
			L.fireHook("tail call", -1)
		} else {
			L.fireHook("call", -1)
		}
	}
	if wantGo {
		if tail {
			L.fireGoHook(HookTailCall, -1)
		} else {
			L.fireGoHook(HookCall, -1)
		}
	}
}

// fireReturnHook fires the return hook before a frame's nres results (sitting at
// L.top-nres .. L.top) are moved. Records the transfer range for getinfo "r":
// the results map to getlocal indices ftransfer .. ftransfer+nres-1, where
// getlocal index i reads stack[ci.fn+i] (PUC rethook).
func (L *LState) fireReturnHook(ci *callInfo, nres int) {
	wantLua := L.hookMask&maskRet != 0 && !L.hook.IsNil()
	wantGo := L.goHook != nil && L.goHookMask&maskRet != 0
	if !wantLua && !wantGo {
		return
	}
	// ftransfer is a 1-based getlocal index (relative to ci.base, which a
	// vararg frame moves away from ci.fn+1), so the first result at L.top-nres
	// maps to index (firstres - base + 1).
	ci.ftransfer = (L.top - nres) - ci.base + 1
	ci.ntransfer = nres
	if wantLua {
		L.fireHook("return", -1)
	}
	if wantGo {
		L.fireGoHook(HookReturn, -1)
	}
}
