package luapure

import (
	"fmt"
	"strings"
)

// DisasmInst renders a single instruction as "NAME operands", decoding operands
// according to the opcode's format (lopcodes.h getOpMode). The k flag, when set,
// is appended as "k". This is a compact, deterministic form for tests and
// debugging — not the exact `luac -l` listing.
func DisasmInst(i Instruction) string {
	op := GetOpCode(i)
	name := op.Name()
	switch op.Mode() {
	case IABx:
		return fmt.Sprintf("%-10s %d %d", name, GetArgA(i), GetArgBx(i))
	case IAsBx:
		return fmt.Sprintf("%-10s %d %d", name, GetArgA(i), GetArgsBx(i))
	case IAx:
		return fmt.Sprintf("%-10s %d", name, GetArgAx(i))
	case IsJ:
		return fmt.Sprintf("%-10s %d", name, GetArgsJ(i))
	default: // IABC
		s := fmt.Sprintf("%-10s %d %d %d", name, GetArgA(i), GetArgB(i), GetArgC(i))
		if Testk(i) {
			s += "k"
		}
		return s
	}
}

// Disasm renders a Proto's bytecode as a numbered listing, one instruction per
// line: "<pc> [<line>] NAME operands".
func Disasm(p *Proto) string {
	var b strings.Builder
	for pc, i := range p.Code {
		line := 0
		if pc < len(p.LineInfo) {
			line = int(p.LineInfo[pc])
		}
		fmt.Fprintf(&b, "%d\t[%d]\t%s\n", pc, line, DisasmInst(i))
	}
	return b.String()
}

// LuacInst renders one instruction with exactly the operand columns `luac -l`
// prints (lopcodes/luac.c PrintCode), minus the trailing "; ..." comment. This
// lets the codegen tests diff our output against the reference compiler. The k
// flag is shown as luac does: a "k" suffix on the last operand for the
// store/self/return family, or a separate 0/1 column for the test family.
func LuacInst(i Instruction) string {
	op := GetOpCode(i)
	name := op.Name()
	a, b, c := GetArgA(i), GetArgB(i), GetArgC(i)
	sb, sc := GetArgsB(i), GetArgsC(i)
	bx, sbx, ax := GetArgBx(i), GetArgsBx(i), GetArgAx(i)
	k := GetArgk(i)
	isk := ""
	if k != 0 {
		isk = "k"
	}
	f := func(format string, args ...any) string { return name + " " + fmt.Sprintf(format, args...) }
	switch op {
	case OP_MOVE, OP_LOADNIL, OP_GETUPVAL, OP_SETUPVAL,
		OP_UNM, OP_BNOT, OP_NOT, OP_LEN, OP_CONCAT:
		return f("%d %d", a, b)
	case OP_LOADI, OP_LOADF:
		return f("%d %d", a, sbx)
	case OP_LOADK, OP_CLOSURE, OP_FORLOOP, OP_FORPREP, OP_TFORPREP, OP_TFORLOOP:
		return f("%d %d", a, bx)
	case OP_LOADKX, OP_LOADFALSE, OP_LFALSESKIP, OP_LOADTRUE,
		OP_CLOSE, OP_TBC, OP_VARARGPREP, OP_RETURN1:
		return f("%d", a)
	case OP_GETTABUP, OP_GETTABLE, OP_GETI, OP_GETFIELD, OP_NEWTABLE,
		OP_ADDK, OP_SUBK, OP_MULK, OP_MODK, OP_POWK, OP_DIVK, OP_IDIVK,
		OP_BANDK, OP_BORK, OP_BXORK,
		OP_ADD, OP_SUB, OP_MUL, OP_MOD, OP_POW, OP_DIV, OP_IDIV,
		OP_BAND, OP_BOR, OP_BXOR, OP_SHL, OP_SHR, OP_MMBIN, OP_CALL, OP_SETLIST:
		return f("%d %d %d", a, b, c)
	case OP_SETTABUP, OP_SETTABLE, OP_SETI, OP_SETFIELD, OP_SELF,
		OP_TAILCALL, OP_RETURN:
		return f("%d %d %d%s", a, b, c, isk)
	case OP_ADDI, OP_SHRI, OP_SHLI:
		return f("%d %d %d", a, b, sc)
	case OP_MMBINI:
		return f("%d %d %d %d", a, sb, c, k)
	case OP_MMBINK:
		return f("%d %d %d %d", a, b, c, k)
	case OP_JMP:
		return f("%d", GetArgsJ(i))
	case OP_EQ, OP_LT, OP_LE, OP_EQK, OP_TESTSET:
		return f("%d %d %d", a, b, k)
	case OP_EQI, OP_LTI, OP_LEI, OP_GTI, OP_GEI:
		return f("%d %d %d", a, sb, k)
	case OP_TEST:
		return f("%d %d", a, k)
	case OP_TFORCALL, OP_VARARG:
		return f("%d %d", a, c)
	case OP_RETURN0:
		return name
	case OP_EXTRAARG:
		return f("%d", ax)
	}
	return f("%d %d %d", a, b, c)
}

// LuacListing renders a prototype tree as a flat list of luac-style instruction
// strings in luac's preorder (each function followed by its nested functions),
// suitable for diffing against parsed `luac -l` output.
func LuacListing(p *Proto) []string {
	var out []string
	var walk func(*Proto)
	walk = func(pr *Proto) {
		for _, i := range pr.Code {
			out = append(out, LuacInst(i))
		}
		for _, sub := range pr.Protos {
			walk(sub)
		}
	}
	walk(p)
	return out
}

// disasmLines returns the disassembled instructions (without pc/line prefixes),
// one per slice element — convenient for table-driven test assertions.
func disasmLines(p *Proto) []string {
	out := make([]string, len(p.Code))
	for pc, i := range p.Code {
		out[pc] = DisasmInst(i)
	}
	return out
}
