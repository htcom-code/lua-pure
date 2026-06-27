package luapure

// Symbolic name resolution for error messages (ldebug.c getobjname /
// basicgetobjname / findsetreg). Given the register a bad value lives in and
// the current pc, it recovers a human name and kind ("global 'x'", "local 'x'",
// "field 'x'", "method 'x'", "upvalue 'x'") that PUC appends to type errors.

const luaEnv = "_ENV"

// getObjName returns the kind and name describing register reg at lastpc, or
// ("","") if none can be found.
func getObjName(p *Proto, lastpc, reg int) (kind, name string) {
	kind, name, _ = basicGetObjName(p, lastpc, reg)
	if kind != "" {
		return kind, name
	}
	// Try the instruction that set reg (basicGetObjName advanced to it).
	_, _, setpc := basicGetObjName(p, lastpc, reg)
	if setpc == -1 {
		return "", ""
	}
	i := p.Code[setpc]
	switch GetOpCode(i) {
	case OP_GETTABUP:
		n := knameStr(p, GetArgC(i))
		return isEnv(p, setpc, i, true), n
	case OP_GETTABLE:
		n := rnameStr(p, setpc, GetArgC(i))
		return isEnv(p, setpc, i, false), n
	case OP_GETI:
		return "field", "integer index"
	case OP_GETFIELD:
		n := knameStr(p, GetArgC(i))
		return isEnv(p, setpc, i, false), n
	case OP_SELF:
		return "method", rknameStr(p, setpc, i)
	}
	return "", ""
}

// basicGetObjName resolves locals and the directly-traceable opcodes, returning
// the setting pc so the caller can inspect table-access opcodes (porting the
// dual structure of getobjname/basicgetobjname). setpc is -1 if not found.
func basicGetObjName(p *Proto, pc, reg int) (kind, name string, setpc int) {
	if n := localName(p, reg+1, pc); n != "" {
		return "local", n, pc
	}
	sp := findSetReg(p, pc, reg)
	if sp == -1 {
		return "", "", -1
	}
	i := p.Code[sp]
	switch GetOpCode(i) {
	case OP_MOVE:
		b := GetArgB(i)
		if b < GetArgA(i) {
			return basicGetObjName(p, sp, b)
		}
	case OP_GETUPVAL:
		return "upvalue", upvalName(p, GetArgB(i)), sp
	case OP_LOADK:
		// PUC getobjname names a constant only when it is a string; a numeric
		// constant operand contributes no varinfo (no "(constant '?')").
		if idx := GetArgBx(i); idx >= 0 && idx < len(p.Constants) && p.Constants[idx].IsString() {
			return "constant", p.Constants[idx].Str(), sp
		}
	case OP_LOADKX:
		if idx := GetArgAx(p.Code[sp+1]); idx >= 0 && idx < len(p.Constants) && p.Constants[idx].IsString() {
			return "constant", p.Constants[idx].Str(), sp
		}
	}
	return "", "", sp
}

func upvalName(p *Proto, i int) string {
	if i >= 0 && i < len(p.Upvalues) {
		return p.Upvalues[i].Name
	}
	return "?"
}

func knameStr(p *Proto, index int) string {
	if index >= 0 && index < len(p.Constants) && p.Constants[index].IsString() {
		return p.Constants[index].Str()
	}
	return "?"
}

func rnameStr(p *Proto, pc, c int) string {
	kind, name, _ := basicGetObjName(p, pc, c)
	if kind == "constant" {
		return name
	}
	return "?"
}

func rknameStr(p *Proto, pc int, i Instruction) string {
	if GetArgk(i) != 0 {
		return knameStr(p, GetArgC(i))
	}
	return rnameStr(p, pc, GetArgC(i))
}

// isEnv decides whether an indexed access is a global (table is _ENV) or a
// plain field.
func isEnv(p *Proto, pc int, i Instruction, isup bool) string {
	t := GetArgB(i)
	var name string
	if isup {
		name = upvalName(p, t)
	} else {
		kind, n, _ := basicGetObjName(p, pc, t)
		if kind == "local" || kind == "upvalue" {
			name = n
		}
	}
	if name == luaEnv {
		return "global"
	}
	return "field"
}

func filterPC(pc, jmptarget int) int {
	if pc < jmptarget {
		return -1
	}
	return pc
}

// findSetReg returns the pc of the last instruction (before lastpc) that set
// register reg, or -1 if it cannot be determined (ldebug.c findsetreg).
func findSetReg(p *Proto, lastpc, reg int) int {
	if GetOpCode(p.Code[lastpc]).IsMM() {
		lastpc-- // the MM follow-up was not actually executed
	}
	setreg := -1
	jmptarget := 0
	for pc := 0; pc < lastpc; pc++ {
		i := p.Code[pc]
		op := GetOpCode(i)
		a := GetArgA(i)
		change := false
		switch op {
		case OP_LOADNIL:
			b := GetArgB(i)
			change = a <= reg && reg <= a+b
		case OP_TFORCALL:
			change = reg >= a+2
		case OP_CALL, OP_TAILCALL:
			change = reg >= a
		case OP_JMP:
			dest := pc + 1 + GetArgsJ(i)
			if dest <= lastpc && dest > jmptarget {
				jmptarget = dest
			}
		default:
			change = op.SetsA() && reg == a
		}
		if change {
			setreg = filterPC(pc, jmptarget)
		}
	}
	return setreg
}

// funcNameFromCode resolves how the function called by the instruction at pc was
// named, returning (namewhat, name) — e.g. ("method","sub"), ("global","print"),
// ("field","insert"), ("local","r") — or ("","") if it cannot tell
// (funcnamefromcode in ldebug.c, call cases only).
func funcNameFromCode(p *Proto, pc int) (namewhat, name string) {
	if pc < 0 || pc >= len(p.Code) {
		return "", ""
	}
	i := p.Code[pc]
	var tm TMS
	switch GetOpCode(i) {
	case OP_CALL, OP_TAILCALL:
		return getObjName(p, pc, GetArgA(i))
	case OP_TFORCALL:
		return "for iterator", "for iterator"
	// Other instructions can call metamethods; report the metamethod name so a
	// failed metamethod call reads e.g. "(metamethod 'add')" (PUC funcnamefromcode).
	case OP_SELF, OP_GETTABUP, OP_GETTABLE, OP_GETI, OP_GETFIELD:
		tm = tmIndex
	case OP_SETTABUP, OP_SETTABLE, OP_SETI, OP_SETFIELD:
		tm = tmNewIndex
	case OP_MMBIN, OP_MMBINI, OP_MMBINK:
		tm = TMS(GetArgC(i))
	case OP_UNM:
		tm = tmUnm
	case OP_BNOT:
		tm = tmBNot
	case OP_LEN:
		tm = tmLen
	case OP_CONCAT:
		tm = tmConcat
	case OP_EQ:
		tm = tmEq
	case OP_LT, OP_LTI, OP_GTI:
		tm = tmLt
	case OP_LE, OP_LEI, OP_GEI:
		tm = tmLe
	case OP_CLOSE, OP_RETURN:
		tm = tmClose
	default:
		return "", ""
	}
	return "metamethod", tm.display()
}

// nameInfo returns the " (kind 'name')" suffix for the value in register reg of
// the current Lua frame, or "" when no frame/name is available.
func (L *LState) nameInfo(reg int) string {
	if reg < 0 || L.ci == nil || !L.ci.isLuaFrame() {
		return ""
	}
	cl := L.stack[L.ci.fn].closure()
	if cl == nil || !cl.isLua() {
		return ""
	}
	kind, name := getObjName(cl.proto, L.ci.savedpc-1, reg)
	if kind == "" {
		return ""
	}
	return " (" + kind + " '" + name + "')"
}

// upvalNameInfo describes a value that is a frame upvalue (a failing GETTABUP
// indexing the upvalue directly), e.g. " (upvalue 'up')".
func (L *LState) upvalNameInfo(idx int) string {
	if idx < 0 || L.ci == nil || !L.ci.isLuaFrame() {
		return ""
	}
	cl := L.stack[L.ci.fn].closure()
	if cl == nil || !cl.isLua() {
		return ""
	}
	name := upvalName(cl.proto, idx)
	if name == luaEnv || name == "?" {
		return "" // _ENV is always a table; nothing useful to name
	}
	return " (upvalue '" + name + "')"
}
