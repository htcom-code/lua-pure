// Package luapure is a ground-up implementation of the Lua 5.4 virtual machine
// (instruction set, compiler, and interpreter), distinct from the package's
// historical 5.1-derived engine. This file ports PUC-Lua 5.4.8's lopcodes.h
// and lopcodes.c verbatim: the 32-bit instruction encoding, the opcode set,
// and the per-opcode mode table.
//
// Instruction formats (bit 0 is least significant):
//
//	iABC   C(8) | B(8) | k(1) | A(8) | Op(7)
//	iABx        Bx(17)        | A(8) | Op(7)
//	iAsBx      sBx(17,signed) | A(8) | Op(7)
//	iAx              Ax(25)          | Op(7)
//	isJ          sJ(25,signed)       | Op(7)
package luapure

// Instruction is a single 32-bit Lua 5.4 VM instruction.
type Instruction uint32

// OpCode identifies a Lua 5.4 virtual-machine instruction.
type OpCode int

// OpMode is one of the five basic instruction formats.
type OpMode int

const (
	IABC OpMode = iota
	IABx
	IAsBx
	IAx
	IsJ
)

// Field sizes (bits) — lopcodes.h SIZE_*.
const (
	SizeC  = 8
	SizeB  = 8
	SizeBx = SizeC + SizeB + 1 // 17
	SizeA  = 8
	SizeAx = SizeBx + SizeA // 25
	SizeJ  = SizeBx + SizeA // 25 (sJ)
	SizeOp = 7
)

// Field positions (bit offsets) — lopcodes.h POS_*.
const (
	PosOp = 0
	PosA  = PosOp + SizeOp // 7
	PosK  = PosA + SizeA   // 15
	PosB  = PosK + 1       // 16
	PosC  = PosB + SizeB   // 24
	PosBx = PosK           // 15
	PosAx = PosA           // 7
	PosJ  = PosA           // 7 (sJ)
)

// Argument limits — lopcodes.h MAXARG_* / OFFSET_*.
const (
	MaxArgBx  = (1 << SizeBx) - 1 // 131071
	OffsetsBx = MaxArgBx >> 1     // 65535
	MaxArgAx  = (1 << SizeAx) - 1 // 33554431
	MaxArgJ   = (1 << SizeJ) - 1  // 33554431
	OffsetsJ  = MaxArgJ >> 1      // 16777215
	MaxArgA   = (1 << SizeA) - 1  // 255
	MaxArgB   = (1 << SizeB) - 1  // 255
	MaxArgC   = (1 << SizeC) - 1  // 255
	OffsetsC  = MaxArgC >> 1      // 127
)

// NoReg is an invalid register that still fits in 8 bits (lopcodes.h NO_REG).
const NoReg = MaxArgA

// LFieldsPerFlush is the number of list items accumulated before a SETLIST.
const LFieldsPerFlush = 50

// int2sC / sC2int convert between a signed-C value and its excess-K encoding.
func Int2sC(i int) int { return i + OffsetsC }
func SC2int(i int) int { return i - OffsetsC }

func mask1(n, p uint) Instruction { return ((^Instruction(0)) >> (32 - n)) << p }

func getArg(i Instruction, pos, size uint) int {
	return int((i >> pos) & mask1(size, 0))
}

func setArg(i *Instruction, v int, pos, size uint) {
	*i = (*i &^ mask1(size, pos)) | ((Instruction(v) << pos) & mask1(size, pos))
}

// --- opcode field ---

func GetOpCode(i Instruction) OpCode { return OpCode(getArg(i, PosOp, SizeOp)) }
func SetOpCode(i *Instruction, o OpCode) {
	setArg(i, int(o), PosOp, SizeOp)
}

// --- A ---

func GetArgA(i Instruction) int     { return getArg(i, PosA, SizeA) }
func SetArgA(i *Instruction, v int) { setArg(i, v, PosA, SizeA) }

// --- B (and signed sB) ---

func GetArgB(i Instruction) int     { return getArg(i, PosB, SizeB) }
func GetArgsB(i Instruction) int    { return SC2int(GetArgB(i)) }
func SetArgB(i *Instruction, v int) { setArg(i, v, PosB, SizeB) }

// --- C (and signed sC) ---

func GetArgC(i Instruction) int     { return getArg(i, PosC, SizeC) }
func GetArgsC(i Instruction) int    { return SC2int(GetArgC(i)) }
func SetArgC(i *Instruction, v int) { setArg(i, v, PosC, SizeC) }

// --- k flag ---

func GetArgk(i Instruction) int { return getArg(i, PosK, 1) }
func Testk(i Instruction) bool  { return i&(1<<PosK) != 0 }
func SetArgk(i *Instruction, v int) {
	setArg(i, v, PosK, 1)
}

// --- Bx / sBx ---

func GetArgBx(i Instruction) int     { return getArg(i, PosBx, SizeBx) }
func SetArgBx(i *Instruction, v int) { setArg(i, v, PosBx, SizeBx) }
func GetArgsBx(i Instruction) int    { return getArg(i, PosBx, SizeBx) - OffsetsBx }
func SetArgsBx(i *Instruction, v int) {
	SetArgBx(i, v+OffsetsBx)
}

// --- Ax ---

func GetArgAx(i Instruction) int     { return getArg(i, PosAx, SizeAx) }
func SetArgAx(i *Instruction, v int) { setArg(i, v, PosAx, SizeAx) }

// --- sJ ---

func GetArgsJ(i Instruction) int { return getArg(i, PosJ, SizeJ) - OffsetsJ }
func SetArgsJ(i *Instruction, v int) {
	setArg(i, v+OffsetsJ, PosJ, SizeJ)
}

// --- instruction creators (lopcodes.h CREATE_*) ---

func CreateABCk(o OpCode, a, b, c, k int) Instruction {
	return (Instruction(o) << PosOp) |
		(Instruction(a) << PosA) |
		(Instruction(b) << PosB) |
		(Instruction(c) << PosC) |
		(Instruction(k) << PosK)
}

func CreateABx(o OpCode, a, bx int) Instruction {
	return (Instruction(o) << PosOp) |
		(Instruction(a) << PosA) |
		(Instruction(bx) << PosBx)
}

func CreateAx(o OpCode, a int) Instruction {
	return (Instruction(o) << PosOp) | (Instruction(a) << PosAx)
}

func CreatesJ(o OpCode, j, k int) Instruction {
	return (Instruction(o) << PosOp) |
		(Instruction(j+OffsetsJ) << PosJ) |
		(Instruction(k) << PosK)
}

// The opcode set, in PUC order (lopcodes.h). "Grep ORDER OP" if changed.
const (
	OP_MOVE OpCode = iota
	OP_LOADI
	OP_LOADF
	OP_LOADK
	OP_LOADKX
	OP_LOADFALSE
	OP_LFALSESKIP
	OP_LOADTRUE
	OP_LOADNIL
	OP_GETUPVAL
	OP_SETUPVAL

	OP_GETTABUP
	OP_GETTABLE
	OP_GETI
	OP_GETFIELD

	OP_SETTABUP
	OP_SETTABLE
	OP_SETI
	OP_SETFIELD

	OP_NEWTABLE

	OP_SELF

	OP_ADDI

	OP_ADDK
	OP_SUBK
	OP_MULK
	OP_MODK
	OP_POWK
	OP_DIVK
	OP_IDIVK

	OP_BANDK
	OP_BORK
	OP_BXORK

	OP_SHRI
	OP_SHLI

	OP_ADD
	OP_SUB
	OP_MUL
	OP_MOD
	OP_POW
	OP_DIV
	OP_IDIV

	OP_BAND
	OP_BOR
	OP_BXOR
	OP_SHL
	OP_SHR

	OP_MMBIN
	OP_MMBINI
	OP_MMBINK

	OP_UNM
	OP_BNOT
	OP_NOT
	OP_LEN

	OP_CONCAT

	OP_CLOSE
	OP_TBC
	OP_JMP
	OP_EQ
	OP_LT
	OP_LE

	OP_EQK
	OP_EQI
	OP_LTI
	OP_LEI
	OP_GTI
	OP_GEI

	OP_TEST
	OP_TESTSET

	OP_CALL
	OP_TAILCALL

	OP_RETURN
	OP_RETURN0
	OP_RETURN1

	OP_FORLOOP
	OP_FORPREP

	OP_TFORPREP
	OP_TFORCALL
	OP_TFORLOOP

	OP_SETLIST

	OP_CLOSURE

	OP_VARARG

	OP_VARARGPREP

	OP_EXTRAARG
)

// NumOpCodes is the count of opcodes (lopcodes.h NUM_OPCODES).
const NumOpCodes = int(OP_EXTRAARG) + 1

// opNames maps each opcode to its name (for disassembly), in opcode order.
var opNames = [NumOpCodes]string{
	"MOVE", "LOADI", "LOADF", "LOADK", "LOADKX", "LOADFALSE", "LFALSESKIP",
	"LOADTRUE", "LOADNIL", "GETUPVAL", "SETUPVAL",
	"GETTABUP", "GETTABLE", "GETI", "GETFIELD",
	"SETTABUP", "SETTABLE", "SETI", "SETFIELD",
	"NEWTABLE", "SELF", "ADDI",
	"ADDK", "SUBK", "MULK", "MODK", "POWK", "DIVK", "IDIVK",
	"BANDK", "BORK", "BXORK", "SHRI", "SHLI",
	"ADD", "SUB", "MUL", "MOD", "POW", "DIV", "IDIV",
	"BAND", "BOR", "BXOR", "SHL", "SHR",
	"MMBIN", "MMBINI", "MMBINK",
	"UNM", "BNOT", "NOT", "LEN", "CONCAT",
	"CLOSE", "TBC", "JMP", "EQ", "LT", "LE",
	"EQK", "EQI", "LTI", "LEI", "GTI", "GEI",
	"TEST", "TESTSET", "CALL", "TAILCALL",
	"RETURN", "RETURN0", "RETURN1",
	"FORLOOP", "FORPREP", "TFORPREP", "TFORCALL", "TFORLOOP",
	"SETLIST", "CLOSURE", "VARARG", "VARARGPREP", "EXTRAARG",
}

// Name returns the opcode's mnemonic, or "?" if out of range.
func (o OpCode) Name() string {
	if o < 0 || int(o) >= NumOpCodes {
		return "?"
	}
	return opNames[o]
}

// opmode packs the per-opcode property bits exactly as lopcodes.h's opmode():
//
//	bit 0-2: op mode (iABC/...); bit 3: sets register A; bit 4: is a test;
//	bit 5: uses L->top (B==0); bit 6: sets L->top (C==0); bit 7: is an MM op.
func opmode(mm, ot, it, t, a int, m OpMode) byte {
	return byte((mm << 7) | (ot << 6) | (it << 5) | (t << 4) | (a << 3) | int(m))
}

// opModes is the property table, ported verbatim from lopcodes.c luaP_opmodes.
var opModes = [NumOpCodes]byte{
	/*           MM OT IT T  A  mode */
	OP_MOVE:       opmode(0, 0, 0, 0, 1, IABC),
	OP_LOADI:      opmode(0, 0, 0, 0, 1, IAsBx),
	OP_LOADF:      opmode(0, 0, 0, 0, 1, IAsBx),
	OP_LOADK:      opmode(0, 0, 0, 0, 1, IABx),
	OP_LOADKX:     opmode(0, 0, 0, 0, 1, IABx),
	OP_LOADFALSE:  opmode(0, 0, 0, 0, 1, IABC),
	OP_LFALSESKIP: opmode(0, 0, 0, 0, 1, IABC),
	OP_LOADTRUE:   opmode(0, 0, 0, 0, 1, IABC),
	OP_LOADNIL:    opmode(0, 0, 0, 0, 1, IABC),
	OP_GETUPVAL:   opmode(0, 0, 0, 0, 1, IABC),
	OP_SETUPVAL:   opmode(0, 0, 0, 0, 0, IABC),
	OP_GETTABUP:   opmode(0, 0, 0, 0, 1, IABC),
	OP_GETTABLE:   opmode(0, 0, 0, 0, 1, IABC),
	OP_GETI:       opmode(0, 0, 0, 0, 1, IABC),
	OP_GETFIELD:   opmode(0, 0, 0, 0, 1, IABC),
	OP_SETTABUP:   opmode(0, 0, 0, 0, 0, IABC),
	OP_SETTABLE:   opmode(0, 0, 0, 0, 0, IABC),
	OP_SETI:       opmode(0, 0, 0, 0, 0, IABC),
	OP_SETFIELD:   opmode(0, 0, 0, 0, 0, IABC),
	OP_NEWTABLE:   opmode(0, 0, 0, 0, 1, IABC),
	OP_SELF:       opmode(0, 0, 0, 0, 1, IABC),
	OP_ADDI:       opmode(0, 0, 0, 0, 1, IABC),
	OP_ADDK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_SUBK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_MULK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_MODK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_POWK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_DIVK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_IDIVK:      opmode(0, 0, 0, 0, 1, IABC),
	OP_BANDK:      opmode(0, 0, 0, 0, 1, IABC),
	OP_BORK:       opmode(0, 0, 0, 0, 1, IABC),
	OP_BXORK:      opmode(0, 0, 0, 0, 1, IABC),
	OP_SHRI:       opmode(0, 0, 0, 0, 1, IABC),
	OP_SHLI:       opmode(0, 0, 0, 0, 1, IABC),
	OP_ADD:        opmode(0, 0, 0, 0, 1, IABC),
	OP_SUB:        opmode(0, 0, 0, 0, 1, IABC),
	OP_MUL:        opmode(0, 0, 0, 0, 1, IABC),
	OP_MOD:        opmode(0, 0, 0, 0, 1, IABC),
	OP_POW:        opmode(0, 0, 0, 0, 1, IABC),
	OP_DIV:        opmode(0, 0, 0, 0, 1, IABC),
	OP_IDIV:       opmode(0, 0, 0, 0, 1, IABC),
	OP_BAND:       opmode(0, 0, 0, 0, 1, IABC),
	OP_BOR:        opmode(0, 0, 0, 0, 1, IABC),
	OP_BXOR:       opmode(0, 0, 0, 0, 1, IABC),
	OP_SHL:        opmode(0, 0, 0, 0, 1, IABC),
	OP_SHR:        opmode(0, 0, 0, 0, 1, IABC),
	OP_MMBIN:      opmode(1, 0, 0, 0, 0, IABC),
	OP_MMBINI:     opmode(1, 0, 0, 0, 0, IABC),
	OP_MMBINK:     opmode(1, 0, 0, 0, 0, IABC),
	OP_UNM:        opmode(0, 0, 0, 0, 1, IABC),
	OP_BNOT:       opmode(0, 0, 0, 0, 1, IABC),
	OP_NOT:        opmode(0, 0, 0, 0, 1, IABC),
	OP_LEN:        opmode(0, 0, 0, 0, 1, IABC),
	OP_CONCAT:     opmode(0, 0, 0, 0, 1, IABC),
	OP_CLOSE:      opmode(0, 0, 0, 0, 0, IABC),
	OP_TBC:        opmode(0, 0, 0, 0, 0, IABC),
	OP_JMP:        opmode(0, 0, 0, 0, 0, IsJ),
	OP_EQ:         opmode(0, 0, 0, 1, 0, IABC),
	OP_LT:         opmode(0, 0, 0, 1, 0, IABC),
	OP_LE:         opmode(0, 0, 0, 1, 0, IABC),
	OP_EQK:        opmode(0, 0, 0, 1, 0, IABC),
	OP_EQI:        opmode(0, 0, 0, 1, 0, IABC),
	OP_LTI:        opmode(0, 0, 0, 1, 0, IABC),
	OP_LEI:        opmode(0, 0, 0, 1, 0, IABC),
	OP_GTI:        opmode(0, 0, 0, 1, 0, IABC),
	OP_GEI:        opmode(0, 0, 0, 1, 0, IABC),
	OP_TEST:       opmode(0, 0, 0, 1, 0, IABC),
	OP_TESTSET:    opmode(0, 0, 0, 1, 1, IABC),
	OP_CALL:       opmode(0, 1, 1, 0, 1, IABC),
	OP_TAILCALL:   opmode(0, 1, 1, 0, 1, IABC),
	OP_RETURN:     opmode(0, 0, 1, 0, 0, IABC),
	OP_RETURN0:    opmode(0, 0, 0, 0, 0, IABC),
	OP_RETURN1:    opmode(0, 0, 0, 0, 0, IABC),
	OP_FORLOOP:    opmode(0, 0, 0, 0, 1, IABx),
	OP_FORPREP:    opmode(0, 0, 0, 0, 1, IABx),
	OP_TFORPREP:   opmode(0, 0, 0, 0, 0, IABx),
	OP_TFORCALL:   opmode(0, 0, 0, 0, 0, IABC),
	OP_TFORLOOP:   opmode(0, 0, 0, 0, 1, IABx),
	OP_SETLIST:    opmode(0, 0, 1, 0, 0, IABC),
	OP_CLOSURE:    opmode(0, 0, 0, 0, 1, IABx),
	OP_VARARG:     opmode(0, 1, 0, 0, 1, IABC),
	OP_VARARGPREP: opmode(0, 0, 1, 0, 1, IABC),
	OP_EXTRAARG:   opmode(0, 0, 0, 0, 0, IAx),
}

// Mode returns the instruction format for opcode o.
func (o OpCode) Mode() OpMode { return OpMode(opModes[o] & 7) }

// SetsA reports whether the opcode assigns register A.
func (o OpCode) SetsA() bool { return opModes[o]&(1<<3) != 0 }

// IsTest reports whether the opcode is a test (next instruction is a jump).
func (o OpCode) IsTest() bool { return opModes[o]&(1<<4) != 0 }

// UsesTopIn reports whether the opcode reads L->top set by the previous
// instruction (relevant when B == 0).
func (o OpCode) UsesTopIn() bool { return opModes[o]&(1<<5) != 0 }

// SetsTopOut reports whether the opcode sets L->top for the next instruction
// (relevant when C == 0).
func (o OpCode) SetsTopOut() bool { return opModes[o]&(1<<6) != 0 }

// IsMM reports whether the opcode invokes a metamethod (MMBIN family).
func (o OpCode) IsMM() bool { return opModes[o]&(1<<7) != 0 }
