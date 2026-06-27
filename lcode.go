package luapure

import (
	"fmt"
	"math"
)

// This file ports PUC-Lua 5.4.8's code generator (lcode.c / lcode.h): the
// register allocator, constant table, expression-descriptor (expdesc) discharge
// machinery, jump-list patching, and operator code generation. It is the layer
// the AST-walking driver (codegen.go) calls to turn parsed expressions and
// statements into 5.4 bytecode.
//
// The port follows lcode.c closely; the deliberate departures, all forced by
// the host language or the surrounding design, are:
//
//   - Single-pass vs AST walk. PUC's lcode is driven by the single-pass parser
//     and reads the current source line from the lexer (ls->lastline). Here the
//     driver sets FuncState.lastline before emitting, so line info still tracks
//     the source faithfully without a lexer.
//   - Line info. PUC compresses line info into relative bytes plus an absolute
//     side table. This Proto stores one absolute line per instruction
//     (LineInfo, 1:1 with Code), so savelineinfo/removelastlineinfo/fixline are
//     simple append/truncate/overwrite.
//   - Constant dedup. PUC reuses the lexer's scanner table — one table for the
//     whole chunk — to cache constant indices. Here a Go map on the shared
//     compiler state keyed by the typed value does the same job (see addk),
//     including the cross-function staleness that lets a value used in both a
//     parent and a child appear twice in a constant table. Because the key
//     carries the value's tag, integers and floats never collide (so PUC's
//     nil-as-table and float-key tricks are unnecessary).
//   - Errors. PUC longjmps on over-limit programs; here syntaxError panics with
//     a *CompileError that the top-level compile entry recovers.

// noJump marks the end of a patch list (PUC NO_JUMP). It is invalid both as an
// absolute address and as a self-link.
const noJump = -1

// maxRegisters is the per-function register ceiling (PUC MAXREGS); it must fit
// in 8 bits.
const maxRegisters = 255

// maxIndexRK is the largest constant index usable as an inline K operand
// (PUC MAXINDEXRK == MAXARG_B).
const maxIndexRK = MaxArgB

// maxShortStr is the byte length under which a string constant is "short"
// (PUC LUAI_MAXSHORTLEN); only short strings can key GETFIELD/SETFIELD.
const maxShortStr = 40

// CompileError is raised when a program exceeds a hard compiler limit (too many
// registers, control structure too long, …). The top-level compile recovers it.
type CompileError struct {
	Msg  string
	Line int
}

func (e *CompileError) Error() string { return e.Msg }

// --- tag-method codes (ltm.h TMS, ORDER TM) ---
//
// Only the arithmetic/bitwise events are used by the compiler (stamped into the
// MMBIN family so the VM can find the right metamethod); the full enum is
// defined here for the VM to reuse.
const (
	TM_INDEX = iota
	TM_NEWINDEX
	TM_GC
	TM_MODE
	TM_LEN
	TM_EQ
	TM_ADD
	TM_SUB
	TM_MUL
	TM_MOD
	TM_POW
	TM_DIV
	TM_IDIV
	TM_BAND
	TM_BOR
	TM_BXOR
	TM_SHL
	TM_SHR
	TM_UNM
	TM_BNOT
	TM_LT
	TM_LE
	TM_CONCAT
	TM_CALL
	TM_CLOSE
	TM_N
)

// --- operator codes (lcode.h BinOpr / UnOpr, ORDER OPR) ---

// BinOpr enumerates binary operators in the order lcode.c expects (ORDER OPR);
// the layout lets binopr2op/binopr2TM convert to opcodes/tag methods by offset.
type BinOpr int

const (
	OPR_ADD BinOpr = iota
	OPR_SUB
	OPR_MUL
	OPR_MOD
	OPR_POW
	OPR_DIV
	OPR_IDIV
	OPR_BAND
	OPR_BOR
	OPR_BXOR
	OPR_SHL
	OPR_SHR
	OPR_CONCAT
	OPR_EQ
	OPR_LT
	OPR_LE
	OPR_NE
	OPR_GT
	OPR_GE
	OPR_AND
	OPR_OR
	OPR_NOBINOPR
)

// foldbinop reports whether opr is arithmetic or bitwise (PUC foldbinop): only
// those operators are constant-foldable.
func foldbinop(op BinOpr) bool { return op <= OPR_SHR }

// UnOpr enumerates unary operators (lcode.h UnOpr).
type UnOpr int

const (
	OPR_MINUS UnOpr = iota
	OPR_BNOT
	OPR_NOT
	OPR_LEN
	OPR_NOUNOPR
)

// --- expression descriptors (lparser.h expkind / expdesc) ---

// expkind classifies an expdesc (PUC expkind). Code generation for a value can
// be delayed; the kind says where the (possibly not-yet-emitted) value lives.
type expkind int

const (
	VVOID     expkind = iota // empty expression (no value)
	VNIL                     // constant nil
	VTRUE                    // constant true
	VFALSE                   // constant false
	VK                       // constant in 'k'; info = index
	VKFLT                    // float constant; nval
	VKINT                    // integer constant; ival
	VKSTR                    // string constant; strval
	VNONRELOC                // value in a fixed register; info = register
	VLOCAL                   // local variable; vr.ridx register, vr.vidx actvar slot
	VUPVAL                   // upvalue; info = upvalue index
	VCONST                   // compile-time <const>; info = actvar slot
	VINDEXED                 // t[k]: ind.t table register, ind.idx key register
	VINDEXUP                 // upvalue[k]: ind.t table upvalue, ind.idx key K index
	VINDEXI                  // t[i]: ind.t table register, ind.idx integer key
	VINDEXSTR                // t.field: ind.t table register, ind.idx key K index
	VJMP                     // test/comparison; info = pc of the jump
	VRELOC                   // result can go in any register; info = instruction pc
	VCALL                    // function call; info = instruction pc
	VVARARG                  // vararg; info = instruction pc
)

// vkisvar / vkisindexed mirror the PUC range macros.
func vkisvar(k expkind) bool     { return VLOCAL <= k && k <= VINDEXSTR }
func vkisindexed(k expkind) bool { return VINDEXED <= k && k <= VINDEXSTR }

// expdesc describes a potentially-delayed variable/expression (PUC expdesc). The
// C union is flattened into separate fields here; only the field(s) named for
// the current kind are meaningful.
type expdesc struct {
	k      expkind
	ival   int64   // VKINT
	nval   float64 // VKFLT
	strval string  // VKSTR
	info   int     // generic (register, upvalue index, constant index, pc, …)
	ind    struct {
		idx int // key: R index or K index
		t   int // table: register or upvalue index
	}
	vr struct {
		ridx int // register holding the local
		vidx int // local's slot in the active-variable array
	}
	t int // patch list: exits when expression is true
	f int // patch list: exits when expression is false
}

// hasJumps reports whether e carries a pending conditional jump list
// (PUC hasjumps): true/false exit lists differ.
func hasJumps(e *expdesc) bool { return e.t != e.f }

// initExp resets e to kind k with the given primary info (PUC init_exp), clearing
// its jump lists.
func initExp(e *expdesc, k expkind, info int) {
	e.t = noJump
	e.f = noJump
	e.k = k
	e.info = info
}

// --- code-generation state (lparser.h FuncState, codegen-relevant fields) ---

// FuncState holds the state needed to generate code for one function. The fields
// here are those the lcode machinery touches; codegen.go adds the parser-side
// bookkeeping (blocks, active variables, upvalue resolution) it needs.
type FuncState struct {
	f          *Proto               // current function header
	prev       *FuncState           // enclosing function
	pc         int                  // next position to code (== len(f.Code))
	lasttarget int                  // pc of the last jump target (basic-block guard)
	freereg    int                  // first free register
	nactvarReg int                  // unit-test register level (used only when ls == nil)
	lastline   int                  // source line stamped on emitted instructions
	needclose  bool                 // function must close upvalues on return
	kcache     map[constKey]int     // constant-dedup cache (lcode-test fallback when ls == nil; see kcacheMap)
	kres       func(info int) Value // resolves a VCONST's compile-time value (set by codegen)

	// Parser-side bookkeeping, populated by the AST walker (codegen.go).
	ls         *compiler // shared compilation state
	bl         *blockCnt // chain of active blocks
	nactvar    int       // number of active local variables
	nups       int       // number of upvalues
	firstlocal int       // index of this function's first local in dyd.actvar
	firstlabel int       // index of this function's first label in dyd.label
	ndebugvars int       // number of entries in f.LocVars
}

// newFuncState creates a FuncState emitting into proto. lasttarget starts at 0
// (PUC open_func), so previousInstruction reports "none" before any code exists.
func newFuncState(proto *Proto) *FuncState {
	// kcache is lazily allocated by kcacheMap (and only when no shared compiler
	// state is attached — the lcode unit-test path); normal compilation dedups
	// through the chunk-wide cache on the compiler instead.
	return &FuncState{f: proto}
}

// nvarstack returns the register level above the active locals — the boundary
// between locals and temporaries (PUC luaY_nvarstack). In codegen mode it is
// computed from the active-variable array (skipping compile-time constants);
// the lcode unit tests, which have no compiler attached, use nactvarReg.
func (fs *FuncState) nvarstack() int {
	if fs.ls != nil {
		return fs.reglevel(fs.nactvar)
	}
	return fs.nactvarReg
}

// syntaxError aborts compilation with a hard-limit error (PUC luaX_syntaxerror).
func (fs *FuncState) syntaxError(msg string) {
	panic(&CompileError{Msg: msg, Line: fs.lastline})
}

// --- constant table ---

// constKey identifies a constant for dedup. The tag separates integers from
// floats with the same bit pattern, so they never share a slot.
type constKey struct {
	tag vtag
	n   uint64
	s   string
}

func constKeyOf(v Value) constKey {
	switch v.tag {
	case tagString:
		return constKey{tag: tagString, s: v.Str()}
	case tagInt:
		return constKey{tag: tagInt, n: v.scalar}
	case tagFloat:
		return constKey{tag: tagFloat, n: v.scalar}
	default:
		return constKey{tag: v.tag}
	}
}

// addk adds v to the constant table, reusing an existing slot when possible
// (PUC addk). Returns the constant's index.
//
// The dedup cache is a single table shared across every function of the chunk
// (PUC's scanner table ls->h), not per-function. A cached index may therefore
// belong to a sibling or nested function, so we reuse it only after confirming
// it is a live slot in THIS function holding the same value; otherwise we fall
// through and append a fresh constant, overwriting the cache. This faithfully
// reproduces luac, whose constant tables contain apparent duplicates of a value
// used in both a parent and a child function — and whose higher constant
// indices in turn force the non-K instruction forms (GETTABLE vs GETFIELD, EQ
// vs EQK) that any byte-identical port must match.
func (fs *FuncState) addk(v Value) int {
	key := constKeyOf(v)
	cache := fs.kcacheMap()
	if idx, ok := cache[key]; ok {
		if idx < len(fs.f.Constants) && constKeyOf(fs.f.Constants[idx]) == key {
			return idx
		}
	}
	// PUC addk grows the constant vector with limit MAXARG_Ax; exceeding it
	// raises "too many constants" (heavy.lua toomanyconst).
	if len(fs.f.Constants) >= MaxArgAx {
		fs.syntaxError(fmt.Sprintf("too many constants (limit is %d)", MaxArgAx))
	}
	idx := fs.f.AddConstant(v)
	cache[key] = idx
	return idx
}

// kcacheMap returns the constant-index dedup cache. In normal compilation it is
// the chunk-wide cache on the shared compiler state (PUC ls->h); the lcode unit
// tests run with no compiler attached and fall back to a per-FuncState map.
func (fs *FuncState) kcacheMap() map[constKey]int {
	if fs.ls != nil {
		if fs.ls.kcache == nil {
			fs.ls.kcache = map[constKey]int{}
		}
		return fs.ls.kcache
	}
	if fs.kcache == nil {
		fs.kcache = map[constKey]int{}
	}
	return fs.kcache
}

func (fs *FuncState) stringK(s string) int {
	// Intern the literal across the whole chunk: identical literals (in any
	// function) share one backing object — matching PUC's scanner string table —
	// so cross-function "%p" identity holds and MkString runs once per distinct
	// literal, not per use.
	if fs.ls != nil {
		return fs.addk(fs.ls.internConst(s))
	}
	return fs.addk(MkString(s))
}
func (fs *FuncState) intK(n int64) int      { return fs.addk(Int(n)) }
func (fs *FuncState) numberK(n float64) int { return fs.addk(Float(n)) }
func (fs *FuncState) boolF() int            { return fs.addk(False) }
func (fs *FuncState) boolT() int            { return fs.addk(True) }
func (fs *FuncState) nilK() int             { return fs.addk(Nil) }

// --- numeral helpers ---

// nvalue returns the float value of a number Value (PUC nvalue), used for the
// division-by-zero fold check.
func nvalue(v Value) float64 {
	if v.IsInt() {
		return float64(v.AsInt())
	}
	return v.AsFloat()
}

// tonumeral returns e's numeric value when it is a (jump-free) numeric constant
// (PUC tonumeral).
func tonumeral(e *expdesc) (Value, bool) {
	if hasJumps(e) {
		return Value{}, false
	}
	switch e.k {
	case VKINT:
		return Int(e.ival), true
	case VKFLT:
		return Float(e.nval), true
	default:
		return Value{}, false
	}
}

// luaK_exp2const fills the value of a constant expression (PUC luaK_exp2const),
// returning false when e is not a constant.
func (fs *FuncState) luaK_exp2const(e *expdesc) (Value, bool) {
	if hasJumps(e) {
		return Value{}, false
	}
	switch e.k {
	case VFALSE:
		return False, true
	case VTRUE:
		return True, true
	case VNIL:
		return Nil, true
	case VKSTR:
		return MkString(e.strval), true
	case VCONST:
		return fs.kres(e.info), true
	default:
		return tonumeral(e)
	}
}

// const2exp loads a constant Value into e (PUC const2exp).
func const2exp(v Value, e *expdesc) {
	switch v.tag {
	case tagInt:
		e.k = VKINT
		e.ival = v.AsInt()
	case tagFloat:
		e.k = VKFLT
		e.nval = v.AsFloat()
	case tagFalse:
		e.k = VFALSE
	case tagTrue:
		e.k = VTRUE
	case tagNil:
		e.k = VNIL
	case tagString:
		e.k = VKSTR
		e.strval = v.Str()
	default:
		panic("const2exp: not a constant value")
	}
}

// --- instruction emission ---

// previousInstruction returns the instruction before the current pc, or nil when
// a jump target may sit between them (PUC previousinstruction) — nil disables
// the peephole optimizations that read it.
func (fs *FuncState) previousInstruction() *Instruction {
	if fs.pc > 0 && fs.pc > fs.lasttarget {
		return &fs.f.Code[fs.pc-1]
	}
	return nil
}

// luaK_code emits instruction i, recording its source line (PUC luaK_code).
func (fs *FuncState) luaK_code(i Instruction) int {
	// PUC luaK_code caps the code vector at MAX_INT (2^31) opcodes. We omit that
	// check on this per-instruction hot path: 2^31 opcodes is ~8.5 GB of Code
	// alone, so a Go allocation failure (fatal OOM) is hit long before the limit
	// — the check could never actually fire, only cost. (Constants/lex elements
	// keep their caps, which are reachable and configurable.)
	fs.f.Code = append(fs.f.Code, i)
	fs.f.LineInfo = append(fs.f.LineInfo, int32(fs.lastline))
	fs.pc = len(fs.f.Code)
	return fs.pc - 1
}

func (fs *FuncState) codeABCk(o OpCode, a, b, c, k int) int {
	return fs.luaK_code(CreateABCk(o, a, b, c, k))
}

func (fs *FuncState) codeABC(o OpCode, a, b, c int) int {
	return fs.codeABCk(o, a, b, c, 0)
}

func (fs *FuncState) codeABx(o OpCode, a, bx int) int {
	return fs.luaK_code(CreateABx(o, a, bx))
}

func (fs *FuncState) codeAsBx(o OpCode, a, sbx int) int {
	return fs.luaK_code(CreateABx(o, a, sbx+OffsetsBx))
}

func (fs *FuncState) codesJ(o OpCode, sj, k int) int {
	return fs.luaK_code(CreatesJ(o, sj, k))
}

func (fs *FuncState) codeextraarg(a int) int {
	return fs.luaK_code(CreateAx(OP_EXTRAARG, a))
}

// codek emits a constant load, falling back to LOADKX+EXTRAARG for indices that
// do not fit in Bx (PUC luaK_codek).
func (fs *FuncState) codek(reg, k int) int {
	if k <= MaxArgBx {
		return fs.codeABx(OP_LOADK, reg, k)
	}
	p := fs.codeABx(OP_LOADKX, reg, 0)
	fs.codeextraarg(k)
	return p
}

// --- line info ---

func (fs *FuncState) removelastlineinfo() {
	fs.f.LineInfo = fs.f.LineInfo[:len(fs.f.LineInfo)-1]
}

// removelastinstruction drops the last emitted instruction and its line info
// (PUC removelastinstruction).
func (fs *FuncState) removelastinstruction() {
	fs.removelastlineinfo()
	fs.f.Code = fs.f.Code[:fs.pc-1]
	fs.pc--
}

// luaK_fixline restamps the last instruction's source line (PUC luaK_fixline).
func (fs *FuncState) luaK_fixline(line int) {
	fs.f.LineInfo[fs.pc-1] = int32(line)
}

// --- register management ---

// checkStack ensures n more registers fit, tracking maxstacksize
// (PUC luaK_checkstack).
func (fs *FuncState) checkStack(n int) {
	newstack := fs.freereg + n
	if newstack > int(fs.f.MaxStackSize) {
		if newstack >= maxRegisters {
			fs.syntaxError("function or expression needs too many registers")
		}
		fs.f.MaxStackSize = uint8(newstack)
	}
}

// reserveRegs reserves n registers (PUC luaK_reserveregs).
func (fs *FuncState) reserveRegs(n int) {
	fs.checkStack(n)
	fs.freereg += n
}

// freeReg frees register reg when it is a temporary (PUC freereg).
func (fs *FuncState) freeReg(reg int) {
	if reg >= fs.nvarstack() {
		fs.freereg--
	}
}

func (fs *FuncState) freeRegs(r1, r2 int) {
	if r1 > r2 {
		fs.freeReg(r1)
		fs.freeReg(r2)
	} else {
		fs.freeReg(r2)
		fs.freeReg(r1)
	}
}

// freeExp frees the register used by e, if any (PUC freeexp).
func (fs *FuncState) freeExp(e *expdesc) {
	if e.k == VNONRELOC {
		fs.freeReg(e.info)
	}
}

func (fs *FuncState) freeExps(e1, e2 *expdesc) {
	r1, r2 := -1, -1
	if e1.k == VNONRELOC {
		r1 = e1.info
	}
	if e2.k == VNONRELOC {
		r2 = e2.info
	}
	fs.freeRegs(r1, r2)
}

// --- constant loads ---

// fitsBx reports whether i fits in an sBx operand (PUC fitsBx).
func fitsBx(i int64) bool {
	return -int64(OffsetsBx) <= i && i <= int64(MaxArgBx-OffsetsBx)
}

// fitsC reports whether i fits in an sC operand (PUC fitsC).
func fitsC(i int64) bool {
	return uint64(i)+uint64(OffsetsC) <= uint64(MaxArgC)
}

// luaK_int loads integer i into reg, using LOADI when it fits (PUC luaK_int).
func (fs *FuncState) luaK_int(reg int, i int64) {
	if fitsBx(i) {
		fs.codeAsBx(OP_LOADI, reg, int(i))
	} else {
		fs.codek(reg, fs.intK(i))
	}
}

// luaK_float loads float f into reg, using LOADF for small integral values
// (PUC luaK_float).
func (fs *FuncState) luaK_float(reg int, f float64) {
	if fi, ok := fltToIntEq(f); ok && fitsBx(fi) {
		fs.codeAsBx(OP_LOADF, reg, int(fi))
	} else {
		fs.codek(reg, fs.numberK(f))
	}
}

// luaK_nil sets registers from..from+n-1 to nil, merging with a preceding
// LOADNIL when the ranges connect (PUC luaK_nil).
func (fs *FuncState) luaK_nil(from, n int) {
	l := from + n - 1
	if prev := fs.previousInstruction(); prev != nil && GetOpCode(*prev) == OP_LOADNIL {
		pfrom := GetArgA(*prev)
		pl := pfrom + GetArgB(*prev)
		if (pfrom <= from && from <= pl+1) || (from <= pfrom && pfrom <= l+1) {
			if pfrom < from {
				from = pfrom
			}
			if pl > l {
				l = pl
			}
			SetArgA(prev, from)
			SetArgB(prev, l-from)
			return
		}
	}
	fs.codeABC(OP_LOADNIL, from, n-1, 0)
}

// --- jumps ---

// getJump follows a jump list (PUC getjump): returns the next pc, or noJump.
func (fs *FuncState) getJump(pc int) int {
	offset := GetArgsJ(fs.f.Code[pc])
	if offset == noJump {
		return noJump
	}
	return (pc + 1) + offset
}

// fixJump points the jump at pc to dest (PUC fixjump).
func (fs *FuncState) fixJump(pc, dest int) {
	jmp := &fs.f.Code[pc]
	offset := dest - (pc + 1)
	if !(-OffsetsJ <= offset && offset <= MaxArgJ-OffsetsJ) {
		fs.syntaxError("control structure too long")
	}
	SetArgsJ(jmp, offset)
}

// luaK_concat appends jump list l2 onto *l1 (PUC luaK_concat).
func (fs *FuncState) luaK_concat(l1 *int, l2 int) {
	if l2 == noJump {
		return
	}
	if *l1 == noJump {
		*l1 = l2
		return
	}
	list := *l1
	for {
		next := fs.getJump(list)
		if next == noJump {
			break
		}
		list = next
	}
	fs.fixJump(list, l2)
}

// luaK_jump emits an unconditional jump whose target is fixed later
// (PUC luaK_jump).
func (fs *FuncState) luaK_jump() int { return fs.codesJ(OP_JMP, noJump, 0) }

// luaK_ret emits a return of nret values starting at register first
// (PUC luaK_ret).
func (fs *FuncState) luaK_ret(first, nret int) {
	var op OpCode
	switch nret {
	case 0:
		op = OP_RETURN0
	case 1:
		op = OP_RETURN1
	default:
		op = OP_RETURN
	}
	fs.codeABC(op, first, nret+1, 0)
}

// condjump emits a test/comparison opcode followed by a jump (PUC condjump).
func (fs *FuncState) condjump(op OpCode, a, b, c, k int) int {
	fs.codeABCk(op, a, b, c, k)
	return fs.luaK_jump()
}

// luaK_getlabel marks the current pc as a jump target (PUC luaK_getlabel).
func (fs *FuncState) luaK_getlabel() int {
	fs.lasttarget = fs.pc
	return fs.pc
}

// getjumpcontrol returns the test controlling the jump at pc, or the jump itself
// when unconditional (PUC getjumpcontrol).
func (fs *FuncState) getjumpcontrol(pc int) *Instruction {
	pi := &fs.f.Code[pc]
	if pc >= 1 && fs.f.Code[pc-1].opCode().IsTest() {
		return &fs.f.Code[pc-1]
	}
	return pi
}

// opCode is a small helper so getjumpcontrol can ask IsTest of an instruction.
func (i Instruction) opCode() OpCode { return GetOpCode(i) }

// patchtestreg points a TESTSET's destination at reg, or downgrades it to a
// plain TEST when reg is unusable (PUC patchtestreg). Returns false when the
// controlled instruction is not a TESTSET.
func (fs *FuncState) patchtestreg(node, reg int) bool {
	i := fs.getjumpcontrol(node)
	if GetOpCode(*i) != OP_TESTSET {
		return false
	}
	if reg != NoReg && reg != GetArgB(*i) {
		SetArgA(i, reg)
	} else {
		*i = CreateABCk(OP_TEST, GetArgB(*i), 0, 0, GetArgk(*i))
	}
	return true
}

// removevalues downgrades every TESTSET in list to a value-less TEST
// (PUC removevalues).
func (fs *FuncState) removevalues(list int) {
	for ; list != noJump; list = fs.getJump(list) {
		fs.patchtestreg(list, NoReg)
	}
}

// patchlistaux patches a jump list: value-producing tests jump to vtarget with
// their value in reg, the rest jump to dtarget (PUC patchlistaux).
func (fs *FuncState) patchlistaux(list, vtarget, reg, dtarget int) {
	for list != noJump {
		next := fs.getJump(list)
		if fs.patchtestreg(list, reg) {
			fs.fixJump(list, vtarget)
		} else {
			fs.fixJump(list, dtarget)
		}
		list = next
	}
}

// luaK_patchlist points all jumps in list at target (PUC luaK_patchlist).
func (fs *FuncState) luaK_patchlist(list, target int) {
	fs.patchlistaux(list, target, NoReg, target)
}

// luaK_patchtohere points all jumps in list at the current pc
// (PUC luaK_patchtohere).
func (fs *FuncState) luaK_patchtohere(list int) {
	hr := fs.luaK_getlabel()
	fs.luaK_patchlist(list, hr)
}

// --- multi-result and variable discharge ---

// luaK_setreturns fixes a multi-result expression to yield nresults
// (PUC luaK_setreturns).
func (fs *FuncState) luaK_setreturns(e *expdesc, nresults int) {
	pc := &fs.f.Code[e.info]
	if e.k == VCALL {
		SetArgC(pc, nresults+1)
	} else { // VVARARG
		SetArgC(pc, nresults+1)
		SetArgA(pc, fs.freereg)
		fs.reserveRegs(1)
	}
}

// luaK_setmultret fixes e to yield all its results (PUC luaK_setmultret).
func (fs *FuncState) luaK_setmultret(e *expdesc) { fs.luaK_setreturns(e, multiRet) }

// multiRet is LUA_MULTRET (lua.h): "all results / all arguments".
const multiRet = -1

// str2K converts a VKSTR into a VK pointing at the constant table (PUC str2K).
func (fs *FuncState) str2K(e *expdesc) {
	e.info = fs.stringK(e.strval)
	e.k = VK
}

// luaK_setoneret fixes a multi-result expression to a single result
// (PUC luaK_setoneret).
func (fs *FuncState) luaK_setoneret(e *expdesc) {
	if e.k == VCALL {
		e.k = VNONRELOC
		e.info = GetArgA(fs.f.Code[e.info])
	} else if e.k == VVARARG {
		SetArgC(&fs.f.Code[e.info], 2)
		e.k = VRELOC
	}
}

// luaK_dischargevars ensures e is not a variable reference, emitting the load
// that fetches its value where needed (PUC luaK_dischargevars).
func (fs *FuncState) luaK_dischargevars(e *expdesc) {
	switch e.k {
	case VCONST:
		const2exp(fs.kres(e.info), e)
	case VLOCAL:
		e.info = e.vr.ridx
		e.k = VNONRELOC
	case VUPVAL:
		e.info = fs.codeABC(OP_GETUPVAL, 0, e.info, 0)
		e.k = VRELOC
	case VINDEXUP:
		e.info = fs.codeABC(OP_GETTABUP, 0, e.ind.t, e.ind.idx)
		e.k = VRELOC
	case VINDEXI:
		fs.freeReg(e.ind.t)
		e.info = fs.codeABC(OP_GETI, 0, e.ind.t, e.ind.idx)
		e.k = VRELOC
	case VINDEXSTR:
		fs.freeReg(e.ind.t)
		e.info = fs.codeABC(OP_GETFIELD, 0, e.ind.t, e.ind.idx)
		e.k = VRELOC
	case VINDEXED:
		fs.freeRegs(e.ind.t, e.ind.idx)
		e.info = fs.codeABC(OP_GETTABLE, 0, e.ind.t, e.ind.idx)
		e.k = VRELOC
	case VVARARG, VCALL:
		fs.luaK_setoneret(e)
	}
}

// discharge2reg puts e's value into register reg, making it VNONRELOC
// (PUC discharge2reg).
func (fs *FuncState) discharge2reg(e *expdesc, reg int) {
	fs.luaK_dischargevars(e)
	switch e.k {
	case VNIL:
		fs.luaK_nil(reg, 1)
	case VFALSE:
		fs.codeABC(OP_LOADFALSE, reg, 0, 0)
	case VTRUE:
		fs.codeABC(OP_LOADTRUE, reg, 0, 0)
	case VKSTR:
		fs.str2K(e)
		fs.codek(reg, e.info)
	case VK:
		fs.codek(reg, e.info)
	case VKFLT:
		fs.luaK_float(reg, e.nval)
	case VKINT:
		fs.luaK_int(reg, e.ival)
	case VRELOC:
		SetArgA(&fs.f.Code[e.info], reg)
	case VNONRELOC:
		if reg != e.info {
			fs.codeABC(OP_MOVE, reg, e.info, 0)
		}
	case VJMP:
		return // nothing to do
	}
	e.info = reg
	e.k = VNONRELOC
}

// discharge2anyreg ensures e occupies some register (PUC discharge2anyreg).
func (fs *FuncState) discharge2anyreg(e *expdesc) {
	if e.k != VNONRELOC {
		fs.reserveRegs(1)
		fs.discharge2reg(e, fs.freereg-1)
	}
}

func (fs *FuncState) code_loadbool(a int, op OpCode) int {
	fs.luaK_getlabel() // may be a jump target
	return fs.codeABC(op, a, 0, 0)
}

// need_value reports whether list has a jump that does not produce a value
// (PUC need_value).
func (fs *FuncState) need_value(list int) bool {
	for ; list != noJump; list = fs.getJump(list) {
		if GetOpCode(*fs.getjumpcontrol(list)) != OP_TESTSET {
			return true
		}
	}
	return false
}

// exp2reg lands e's full result (including its jump lists) in register reg
// (PUC exp2reg).
func (fs *FuncState) exp2reg(e *expdesc, reg int) {
	fs.discharge2reg(e, reg)
	if e.k == VJMP {
		fs.luaK_concat(&e.t, e.info) // put this jump in the true list
	}
	if hasJumps(e) {
		pf := noJump // position of an eventual LOADFALSE
		pt := noJump // position of an eventual LOADTRUE
		if fs.need_value(e.t) || fs.need_value(e.f) {
			fj := noJump
			if e.k != VJMP {
				fj = fs.luaK_jump()
			}
			pf = fs.code_loadbool(reg, OP_LFALSESKIP) // skip next instruction
			pt = fs.code_loadbool(reg, OP_LOADTRUE)
			fs.luaK_patchtohere(fj)
		}
		final := fs.luaK_getlabel()
		fs.patchlistaux(e.f, final, reg, pf)
		fs.patchlistaux(e.t, final, reg, pt)
	}
	e.f, e.t = noJump, noJump
	e.info = reg
	e.k = VNONRELOC
}

// luaK_exp2nextreg lands e's result in the next free register
// (PUC luaK_exp2nextreg).
func (fs *FuncState) luaK_exp2nextreg(e *expdesc) {
	fs.luaK_dischargevars(e)
	fs.freeExp(e)
	fs.reserveRegs(1)
	fs.exp2reg(e, fs.freereg-1)
}

// luaK_exp2anyreg lands e's result in some register and returns it
// (PUC luaK_exp2anyreg).
func (fs *FuncState) luaK_exp2anyreg(e *expdesc) int {
	fs.luaK_dischargevars(e)
	if e.k == VNONRELOC {
		if !hasJumps(e) {
			return e.info
		}
		if e.info >= fs.nvarstack() { // not a local register
			fs.exp2reg(e, e.info)
			return e.info
		}
	}
	fs.luaK_exp2nextreg(e)
	return e.info
}

// luaK_exp2anyregup keeps an upvalue as-is, else lands it in a register
// (PUC luaK_exp2anyregup).
func (fs *FuncState) luaK_exp2anyregup(e *expdesc) {
	if e.k != VUPVAL || hasJumps(e) {
		fs.luaK_exp2anyreg(e)
	}
}

// luaK_exp2val lands e in a register if it has jumps, else discharges its
// variable reference (PUC luaK_exp2val).
func (fs *FuncState) luaK_exp2val(e *expdesc) {
	if e.k == VJMP || hasJumps(e) {
		fs.luaK_exp2anyreg(e)
	} else {
		fs.luaK_dischargevars(e)
	}
}

// exp2K tries to make e a K expression whose index fits an inline operand
// (PUC luaK_exp2K). Returns true on success.
func (fs *FuncState) exp2K(e *expdesc) bool {
	if hasJumps(e) {
		return false
	}
	var info int
	switch e.k {
	case VTRUE:
		info = fs.boolT()
	case VFALSE:
		info = fs.boolF()
	case VNIL:
		info = fs.nilK()
	case VKINT:
		info = fs.intK(e.ival)
	case VKFLT:
		info = fs.numberK(e.nval)
	case VKSTR:
		info = fs.stringK(e.strval)
	case VK:
		info = e.info
	default:
		return false
	}
	if info <= maxIndexRK {
		e.k = VK
		e.info = info
		return true
	}
	return false
}

// exp2RK ensures e is a valid R/K operand (PUC exp2RK); returns true iff K.
func (fs *FuncState) exp2RK(e *expdesc) bool {
	if fs.exp2K(e) {
		return true
	}
	fs.luaK_exp2anyreg(e)
	return false
}

// codeABRK emits an opcode whose final operand is an R/K from ec, setting the k
// flag accordingly (PUC codeABRK).
func (fs *FuncState) codeABRK(o OpCode, a, b int, ec *expdesc) {
	k := 0
	if fs.exp2RK(ec) {
		k = 1
	}
	fs.codeABCk(o, a, b, ec.info, k)
}

// luaK_storevar stores the value of ex into the variable var (PUC luaK_storevar).
func (fs *FuncState) luaK_storevar(varE, ex *expdesc) {
	switch varE.k {
	case VLOCAL:
		fs.freeExp(ex)
		fs.exp2reg(ex, varE.vr.ridx)
		return
	case VUPVAL:
		e := fs.luaK_exp2anyreg(ex)
		fs.codeABC(OP_SETUPVAL, e, varE.info, 0)
	case VINDEXUP:
		fs.codeABRK(OP_SETTABUP, varE.ind.t, varE.ind.idx, ex)
	case VINDEXI:
		fs.codeABRK(OP_SETI, varE.ind.t, varE.ind.idx, ex)
	case VINDEXSTR:
		fs.codeABRK(OP_SETFIELD, varE.ind.t, varE.ind.idx, ex)
	case VINDEXED:
		fs.codeABRK(OP_SETTABLE, varE.ind.t, varE.ind.idx, ex)
	default:
		panic("luaK_storevar: invalid var kind")
	}
	fs.freeExp(ex)
}

// luaK_self emits SELF, turning e into "e:key(e," (PUC luaK_self).
func (fs *FuncState) luaK_self(e, key *expdesc) {
	fs.luaK_exp2anyreg(e)
	ereg := e.info
	fs.freeExp(e)
	e.info = fs.freereg
	e.k = VNONRELOC
	fs.reserveRegs(2) // function and self produced by SELF
	fs.codeABRK(OP_SELF, e.info, ereg, key)
	fs.freeExp(key)
}

// --- conditional code ---

// negatecondition flips the sense of a comparison expression (PUC negatecondition).
func (fs *FuncState) negatecondition(e *expdesc) {
	pc := fs.getjumpcontrol(e.info)
	SetArgk(pc, GetArgk(*pc)^1)
}

// jumponcond emits a jump taken when e equals cond, optimizing `not e`
// (PUC jumponcond).
func (fs *FuncState) jumponcond(e *expdesc, cond int) int {
	if e.k == VRELOC {
		ie := fs.f.Code[e.info]
		if GetOpCode(ie) == OP_NOT {
			fs.removelastinstruction()
			notCond := 1
			if cond != 0 {
				notCond = 0
			}
			return fs.condjump(OP_TEST, GetArgB(ie), 0, 0, notCond)
		}
	}
	fs.discharge2anyreg(e)
	fs.freeExp(e)
	return fs.condjump(OP_TESTSET, NoReg, e.info, 0, cond)
}

// luaK_goiftrue codes "go through if e is true, jump otherwise"
// (PUC luaK_goiftrue).
func (fs *FuncState) luaK_goiftrue(e *expdesc) {
	fs.luaK_dischargevars(e)
	var pc int
	switch e.k {
	case VJMP:
		fs.negatecondition(e)
		pc = e.info
	case VK, VKFLT, VKINT, VKSTR, VTRUE:
		pc = noJump // always true
	default:
		pc = fs.jumponcond(e, 0)
	}
	fs.luaK_concat(&e.f, pc)
	fs.luaK_patchtohere(e.t)
	e.t = noJump
}

// luaK_goiffalse codes "go through if e is false, jump otherwise"
// (PUC luaK_goiffalse).
func (fs *FuncState) luaK_goiffalse(e *expdesc) {
	fs.luaK_dischargevars(e)
	var pc int
	switch e.k {
	case VJMP:
		pc = e.info
	case VNIL, VFALSE:
		pc = noJump // always false
	default:
		pc = fs.jumponcond(e, 1)
	}
	fs.luaK_concat(&e.t, pc)
	fs.luaK_patchtohere(e.f)
	e.f = noJump
}

// codenot codes "not e", folding constants (PUC codenot).
func (fs *FuncState) codenot(e *expdesc) {
	switch e.k {
	case VNIL, VFALSE:
		e.k = VTRUE
	case VK, VKFLT, VKINT, VKSTR, VTRUE:
		e.k = VFALSE
	case VJMP:
		fs.negatecondition(e)
	case VRELOC, VNONRELOC:
		fs.discharge2anyreg(e)
		fs.freeExp(e)
		e.info = fs.codeABC(OP_NOT, 0, e.info, 0)
		e.k = VRELOC
	default:
		panic("codenot: unexpected kind")
	}
	e.t, e.f = e.f, e.t
	fs.removevalues(e.f)
	fs.removevalues(e.t)
}

// --- indexing ---

// isKstr reports whether e is a short literal string constant usable as a
// field key (PUC isKstr).
func (fs *FuncState) isKstr(e *expdesc) bool {
	if e.k != VK || hasJumps(e) || e.info > MaxArgB {
		return false
	}
	v := fs.f.Constants[e.info]
	return v.IsString() && len(v.Str()) <= maxShortStr
}

// isKint reports whether e is a literal integer (PUC isKint).
func isKint(e *expdesc) bool { return e.k == VKINT && !hasJumps(e) }

// isCint reports whether e is a literal integer fitting register C (PUC isCint).
func isCint(e *expdesc) bool { return isKint(e) && uint64(e.ival) <= uint64(MaxArgC) }

// isSCint reports whether e is a literal integer fitting sC (PUC isSCint).
func isSCint(e *expdesc) bool { return isKint(e) && fitsC(e.ival) }

// isSCnumber reports whether e is a number fitting an sC operand, returning the
// encoded value and whether the original was a float (PUC isSCnumber).
func isSCnumber(e *expdesc) (im int, isfloat bool, ok bool) {
	var i int64
	switch {
	case e.k == VKINT:
		i = e.ival
	case e.k == VKFLT:
		fi, conv := fltToIntEq(e.nval)
		if !conv {
			return 0, false, false
		}
		i = fi
		isfloat = true
	default:
		return 0, false, false
	}
	if !hasJumps(e) && fitsC(i) {
		return Int2sC(int(i)), isfloat, true
	}
	// PUC isSCnumber writes *isfloat before the range test, so an integer-valued
	// float that does not fit the immediate still reports isfloat — codeeq/
	// codeorder then carry it into operand C of the EQK/EQ/LT comparison.
	return 0, isfloat, false
}

// luaK_indexed builds the expression t[k] (PUC luaK_indexed). t must already
// have its value in a register or upvalue.
func (fs *FuncState) luaK_indexed(t, k *expdesc) {
	if k.k == VKSTR {
		fs.str2K(k)
	}
	if t.k == VUPVAL && !fs.isKstr(k) {
		fs.luaK_exp2anyreg(t) // upvalue can only be indexed by a Kstr
	}
	if t.k == VUPVAL {
		t.ind.t = t.info
		t.ind.idx = k.info
		t.k = VINDEXUP
		return
	}
	if t.k == VLOCAL {
		t.ind.t = t.vr.ridx
	} else {
		t.ind.t = t.info
	}
	switch {
	case fs.isKstr(k):
		t.ind.idx = k.info
		t.k = VINDEXSTR
	case isCint(k):
		t.ind.idx = int(k.ival)
		t.k = VINDEXI
	default:
		t.ind.idx = fs.luaK_exp2anyreg(k)
		t.k = VINDEXED
	}
}

// --- constant folding ---

// validop reports whether folding op on the given constants is safe: bitwise
// ops need integer-convertible operands; division/modulo need a nonzero divisor
// (PUC validop).
func validop(op int, v1, v2 Value) bool {
	switch op {
	case OpBAnd, OpBOr, OpBXor, OpShl, OpShr, OpBNot:
		_, ok1 := toIntegerNS(v1)
		_, ok2 := toIntegerNS(v2)
		return ok1 && ok2
	case OpDiv, OpIDiv, OpMod:
		return nvalue(v2) != 0
	default:
		return true
	}
}

// constfolding folds a numeric operation into e1 when both operands are numeric
// constants and the operation is safe (PUC constfolding). It refuses NaN and 0.0
// float results to avoid -0.0 surprises.
func (fs *FuncState) constfolding(op int, e1, e2 *expdesc) bool {
	v1, ok1 := tonumeral(e1)
	v2, ok2 := tonumeral(e2)
	if !ok1 || !ok2 || !validop(op, v1, v2) {
		return false
	}
	res, ok := rawArith(op, v1, v2)
	if !ok {
		return false
	}
	if res.IsInt() {
		e1.k = VKINT
		e1.ival = res.AsInt()
	} else {
		n := res.AsFloat()
		if math.IsNaN(n) || n == 0 {
			return false
		}
		e1.k = VKFLT
		e1.nval = n
	}
	return true
}

// --- operator-to-opcode mappings ---

// binopr2op converts a BinOpr to an OpCode by offset from a base pair
// (PUC binopr2op).
func binopr2op(opr, baser BinOpr, base OpCode) OpCode {
	return OpCode(int(opr) - int(baser) + int(base))
}

// unopr2op converts a UnOpr to its OpCode (PUC unopr2op).
func unopr2op(opr UnOpr) OpCode {
	return OpCode(int(opr) - int(OPR_MINUS) + int(OP_UNM))
}

// binopr2TM converts a BinOpr to its tag-method code (PUC binopr2TM).
func binopr2TM(opr BinOpr) int { return int(opr) - int(OPR_ADD) + TM_ADD }

// --- binary/unary value code ---

// codeunexpval emits a value-producing unary op (PUC codeunexpval).
func (fs *FuncState) codeunexpval(op OpCode, e *expdesc, line int) {
	r := fs.luaK_exp2anyreg(e)
	fs.freeExp(e)
	e.info = fs.codeABC(op, 0, r, 0)
	e.k = VRELOC
	fs.luaK_fixline(line)
}

// finishbinexpval emits a binary op plus its MMBIN metamethod follow-up
// (PUC finishbinexpval).
func (fs *FuncState) finishbinexpval(e1, e2 *expdesc, op OpCode, v2, flip, line int, mmop OpCode, event int) {
	v1 := fs.luaK_exp2anyreg(e1)
	pc := fs.codeABCk(op, 0, v1, v2, 0)
	fs.freeExps(e1, e2)
	e1.info = pc
	e1.k = VRELOC
	fs.luaK_fixline(line)
	fs.codeABCk(mmop, v1, v2, event, flip)
	fs.luaK_fixline(line)
}

// codebinexpval emits a binary op over two registers (PUC codebinexpval).
func (fs *FuncState) codebinexpval(opr BinOpr, e1, e2 *expdesc, line int) {
	op := binopr2op(opr, OPR_ADD, OP_ADD)
	v2 := fs.luaK_exp2anyreg(e2)
	fs.finishbinexpval(e1, e2, op, v2, 0, line, OP_MMBIN, binopr2TM(opr))
}

// codebini emits a binary op with an immediate operand (PUC codebini).
func (fs *FuncState) codebini(op OpCode, e1, e2 *expdesc, flip, line, event int) {
	v2 := Int2sC(int(e2.ival))
	fs.finishbinexpval(e1, e2, op, v2, flip, line, OP_MMBINI, event)
}

// codebinK emits a binary op with a K operand (PUC codebinK).
func (fs *FuncState) codebinK(opr BinOpr, e1, e2 *expdesc, flip, line int) {
	event := binopr2TM(opr)
	v2 := e2.info
	op := binopr2op(opr, OPR_ADD, OP_ADDK)
	fs.finishbinexpval(e1, e2, op, v2, flip, line, OP_MMBINK, event)
}

// finishbinexpneg codes a binary op against the negation of an integer constant
// (PUC finishbinexpneg). Returns false when e2 is not a suitable constant.
func (fs *FuncState) finishbinexpneg(e1, e2 *expdesc, op OpCode, line, event int) bool {
	if !isKint(e2) {
		return false
	}
	i2 := e2.ival
	if !(fitsC(i2) && fitsC(-i2)) {
		return false
	}
	v2 := int(i2)
	fs.finishbinexpval(e1, e2, op, Int2sC(-v2), 0, line, OP_MMBINI, event)
	SetArgB(&fs.f.Code[fs.pc-1], Int2sC(v2)) // metamethod keeps the original operand
	return true
}

func swapexps(e1, e2 *expdesc) { *e1, *e2 = *e2, *e1 }

// codebinNoK emits a binary op with no constant operand (PUC codebinNoK).
func (fs *FuncState) codebinNoK(opr BinOpr, e1, e2 *expdesc, flip, line int) {
	if flip != 0 {
		swapexps(e1, e2) // restore original order
	}
	fs.codebinexpval(opr, e1, e2, line)
}

// codearith codes an arithmetic op, using a K-operand variant when e2 is a
// suitable constant (PUC codearith).
func (fs *FuncState) codearith(opr BinOpr, e1, e2 *expdesc, flip, line int) {
	if _, ok := tonumeral(e2); ok && fs.exp2K(e2) {
		fs.codebinK(opr, e1, e2, flip, line)
	} else {
		fs.codebinNoK(opr, e1, e2, flip, line)
	}
}

// codecommutative codes a commutative op, swapping operands to expose an
// immediate/K operand (PUC codecommutative).
func (fs *FuncState) codecommutative(op BinOpr, e1, e2 *expdesc, line int) {
	flip := 0
	if _, ok := tonumeral(e1); ok {
		swapexps(e1, e2)
		flip = 1
	}
	if op == OPR_ADD && isSCint(e2) {
		fs.codebini(OP_ADDI, e1, e2, flip, line, TM_ADD)
	} else {
		fs.codearith(op, e1, e2, flip, line)
	}
}

// codebitwise codes a bitwise op, preferring an integer-constant 2nd operand
// (PUC codebitwise).
func (fs *FuncState) codebitwise(opr BinOpr, e1, e2 *expdesc, line int) {
	flip := 0
	if e1.k == VKINT {
		swapexps(e1, e2)
		flip = 1
	}
	if e2.k == VKINT && fs.exp2K(e2) {
		fs.codebinK(opr, e1, e2, flip, line)
	} else {
		fs.codebinNoK(opr, e1, e2, flip, line)
	}
}

// codeorder codes an order comparison (<, <=), using immediates when possible
// (PUC codeorder).
func (fs *FuncState) codeorder(opr BinOpr, e1, e2 *expdesc) {
	var r1, r2 int
	var op OpCode
	// isfloat (operand C) is sticky across both probes: PUC's isSCnumber sets it
	// for any float operand and never clears it, so a float that fails the
	// immediate test still marks the comparison as float-typed.
	im, isfloat, ok := isSCnumber(e2)
	if ok {
		r1 = fs.luaK_exp2anyreg(e1)
		r2 = im
		op = binopr2op(opr, OPR_LT, OP_LTI)
	} else if im1, fl1, ok1 := isSCnumber(e1); ok1 {
		// (A < B) becomes (B > A); (A <= B) becomes (B >= A)
		r1 = fs.luaK_exp2anyreg(e2)
		r2 = im1
		isfloat = isfloat || fl1
		op = binopr2op(opr, OPR_LT, OP_GTI)
	} else {
		isfloat = isfloat || fl1
		r1 = fs.luaK_exp2anyreg(e1)
		r2 = fs.luaK_exp2anyreg(e2)
		op = binopr2op(opr, OPR_LT, OP_LT)
	}
	fs.freeExps(e1, e2)
	cf := 0 // operand C carries the "was a float" flag
	if isfloat {
		cf = 1
	}
	e1.info = fs.condjump(op, r1, r2, cf, 1)
	e1.k = VJMP
}

// codeeq codes an equality comparison (==, ~=) (PUC codeeq). e1 was put as RK by
// luaK_infix. Operand C carries the "was a float" flag; k selects ==/~=.
func (fs *FuncState) codeeq(opr BinOpr, e1, e2 *expdesc) {
	if e1.k != VNONRELOC {
		swapexps(e1, e2)
	}
	r1 := fs.luaK_exp2anyreg(e1)
	var op OpCode
	var r2 int
	// isfloat (operand C) records that the constant operand was a float, even
	// when it is too large for the EQI immediate and falls through to EQK
	// (PUC isSCnumber's side effect persists past its false return).
	im, isfloat, ok := isSCnumber(e2)
	if ok {
		op = OP_EQI
		r2 = im
	} else if fs.exp2RK(e2) {
		op = OP_EQK
		r2 = e2.info
	} else {
		op = OP_EQ
		r2 = fs.luaK_exp2anyreg(e2)
	}
	fs.freeExps(e1, e2)
	eq := 0
	if opr == OPR_EQ {
		eq = 1
	}
	cf := 0
	if isfloat {
		cf = 1
	}
	e1.info = fs.condjump(op, r1, r2, cf, eq)
	e1.k = VJMP
}

// luaK_prefix applies a prefix operator to e (PUC luaK_prefix).
func (fs *FuncState) luaK_prefix(opr UnOpr, e *expdesc, line int) {
	var ef expdesc // fake 2nd operand for folding
	initExp(&ef, VKINT, 0)
	fs.luaK_dischargevars(e)
	switch opr {
	case OPR_MINUS, OPR_BNOT:
		if fs.constfolding(int(opr)+OpUnm, e, &ef) {
			break
		}
		fs.codeunexpval(unopr2op(opr), e, line)
	case OPR_LEN:
		fs.codeunexpval(unopr2op(opr), e, line)
	case OPR_NOT:
		fs.codenot(e)
	default:
		panic("luaK_prefix: bad operator")
	}
}

// luaK_infix processes the 1st operand of a binary op before the 2nd is read
// (PUC luaK_infix).
func (fs *FuncState) luaK_infix(op BinOpr, v *expdesc) {
	fs.luaK_dischargevars(v)
	switch op {
	case OPR_AND:
		fs.luaK_goiftrue(v)
	case OPR_OR:
		fs.luaK_goiffalse(v)
	case OPR_CONCAT:
		fs.luaK_exp2nextreg(v) // operand must be on the stack
	case OPR_ADD, OPR_SUB, OPR_MUL, OPR_DIV, OPR_IDIV, OPR_MOD, OPR_POW,
		OPR_BAND, OPR_BOR, OPR_BXOR, OPR_SHL, OPR_SHR:
		if _, ok := tonumeral(v); !ok {
			fs.luaK_exp2anyreg(v)
		}
	case OPR_EQ, OPR_NE:
		if _, ok := tonumeral(v); !ok {
			fs.exp2RK(v)
		}
	case OPR_LT, OPR_LE, OPR_GT, OPR_GE:
		if _, _, ok := isSCnumber(v); !ok {
			fs.luaK_exp2anyreg(v)
		}
	default:
		panic("luaK_infix: bad operator")
	}
}

// codeconcat merges adjacent CONCATs for right-associative concatenation
// (PUC codeconcat).
func (fs *FuncState) codeconcat(e1, e2 *expdesc, line int) {
	if ie2 := fs.previousInstruction(); ie2 != nil && GetOpCode(*ie2) == OP_CONCAT {
		n := GetArgB(*ie2)
		fs.freeExp(e2)
		SetArgA(ie2, e1.info)
		SetArgB(ie2, n+1)
	} else {
		fs.codeABC(OP_CONCAT, e1.info, 2, 0)
		fs.freeExp(e2)
		fs.luaK_fixline(line)
	}
}

// luaK_posfix finalizes a binary operation after the 2nd operand is read
// (PUC luaK_posfix).
func (fs *FuncState) luaK_posfix(opr BinOpr, e1, e2 *expdesc, line int) {
	fs.luaK_dischargevars(e2)
	if foldbinop(opr) && fs.constfolding(int(opr)+OpAdd, e1, e2) {
		return
	}
	switch opr {
	case OPR_AND:
		fs.luaK_concat(&e2.f, e1.f)
		*e1 = *e2
	case OPR_OR:
		fs.luaK_concat(&e2.t, e1.t)
		*e1 = *e2
	case OPR_CONCAT:
		fs.luaK_exp2nextreg(e2)
		fs.codeconcat(e1, e2, line)
	case OPR_ADD, OPR_MUL:
		fs.codecommutative(opr, e1, e2, line)
	case OPR_SUB:
		if fs.finishbinexpneg(e1, e2, OP_ADDI, line, TM_SUB) {
			break
		}
		fs.codearith(opr, e1, e2, 0, line)
	case OPR_DIV, OPR_IDIV, OPR_MOD, OPR_POW:
		fs.codearith(opr, e1, e2, 0, line)
	case OPR_BAND, OPR_BOR, OPR_BXOR:
		fs.codebitwise(opr, e1, e2, line)
	case OPR_SHL:
		if isSCint(e1) {
			swapexps(e1, e2)
			fs.codebini(OP_SHLI, e1, e2, 1, line, TM_SHL) // I << r2
		} else if fs.finishbinexpneg(e1, e2, OP_SHRI, line, TM_SHL) {
			// coded as (r1 >> -I)
		} else {
			fs.codebinexpval(opr, e1, e2, line)
		}
	case OPR_SHR:
		if isSCint(e2) {
			fs.codebini(OP_SHRI, e1, e2, 0, line, TM_SHR) // r1 >> I
		} else {
			fs.codebinexpval(opr, e1, e2, line)
		}
	case OPR_EQ, OPR_NE:
		fs.codeeq(opr, e1, e2)
	case OPR_GT, OPR_GE:
		// (a > b) == (b < a); (a >= b) == (b <= a)
		swapexps(e1, e2)
		opr = BinOpr(int(opr)-int(OPR_GT)) + OPR_LT
		fs.codeorder(opr, e1, e2)
	case OPR_LT, OPR_LE:
		fs.codeorder(opr, e1, e2)
	default:
		panic("luaK_posfix: bad operator")
	}
}

// --- table construction ---

// luaK_settablesize back-patches a NEWTABLE with its array/hash sizes
// (PUC luaK_settablesize).
func (fs *FuncState) luaK_settablesize(pc, ra, asize, hsize int) {
	rb := 0
	if hsize != 0 {
		rb = ceillog2(uint(hsize)) + 1
	}
	extra := asize / (MaxArgC + 1)
	rc := asize % (MaxArgC + 1)
	k := 0
	if extra > 0 {
		k = 1
	}
	fs.f.Code[pc] = CreateABCk(OP_NEWTABLE, ra, rb, rc, k)
	fs.f.Code[pc+1] = CreateAx(OP_EXTRAARG, extra)
}

// luaK_setlist emits a SETLIST flushing tostore values into the table at base
// (PUC luaK_setlist).
func (fs *FuncState) luaK_setlist(base, nelems, tostore int) {
	if tostore == multiRet {
		tostore = 0
	}
	if nelems <= MaxArgC {
		fs.codeABC(OP_SETLIST, base, tostore, nelems)
	} else {
		extra := nelems / (MaxArgC + 1)
		nelems %= (MaxArgC + 1)
		fs.codeABCk(OP_SETLIST, base, tostore, nelems, 1)
		fs.codeextraarg(extra)
	}
	fs.freereg = base + 1 // free the list-value registers
}

// ceillog2 returns ceil(log2(x)) (PUC luaO_ceillog2), for hash-size encoding.
func ceillog2(x uint) int {
	l := 0
	x--
	for x >= 256 {
		l += 8
		x >>= 8
	}
	for x != 0 {
		l++
		x >>= 1
	}
	return l
}

// --- final pass ---

// finaltarget follows a chain of jumps-to-jumps to its end (PUC finaltarget).
func finaltarget(code []Instruction, i int) int {
	for count := 0; count < 100; count++ {
		pc := code[i]
		if GetOpCode(pc) != OP_JMP {
			break
		}
		i += GetArgsJ(pc) + 1
	}
	return i
}

// luaK_finish does the final peephole pass: collapses jump chains and marks
// returns/tailcalls that must close upvalues or are vararg (PUC luaK_finish).
func (fs *FuncState) luaK_finish() {
	p := fs.f
	for i := 0; i < fs.pc; i++ {
		pc := &p.Code[i]
		switch GetOpCode(*pc) {
		case OP_RETURN0, OP_RETURN1:
			if !(fs.needclose || p.IsVararg) {
				break
			}
			SetOpCode(pc, OP_RETURN)
			fallthrough
		case OP_RETURN, OP_TAILCALL:
			if fs.needclose {
				SetArgk(pc, 1)
			}
			if p.IsVararg {
				SetArgC(pc, int(p.NumParams)+1)
			}
		case OP_JMP:
			target := finaltarget(p.Code, i)
			fs.fixJump(i, target)
		}
	}
}
