package luapure

import (
	"strings"
	"testing"
)

// These tests drive the lcode machinery directly (the way the AST walker in
// codegen.go will), building expdescs by hand and asserting on the emitted
// bytecode. Expected listings are hand-traced against PUC lcode.c.

// fsWith returns a FuncState that already has n active locals occupying
// registers 0..n-1 (so freereg starts at n, like a real function body).
func fsWith(n int) *FuncState {
	fs := newFuncState(&Proto{})
	fs.nactvarReg = n
	fs.freereg = n
	if n > 0 {
		fs.f.MaxStackSize = uint8(n)
	}
	return fs
}

func localExp(ridx int) *expdesc {
	e := &expdesc{}
	initExp(e, VLOCAL, 0)
	e.vr.ridx = ridx
	return e
}

func intExp(i int64) *expdesc   { e := &expdesc{}; initExp(e, VKINT, 0); e.ival = i; return e }
func fltExp(f float64) *expdesc { e := &expdesc{}; initExp(e, VKFLT, 0); e.nval = f; return e }
func strExp(s string) *expdesc  { e := &expdesc{}; initExp(e, VKSTR, 0); e.strval = s; return e }
func nilExp() *expdesc          { e := &expdesc{}; initExp(e, VNIL, 0); return e }

// binop mimics the parser's expr/subexpr sequence for a binary operator:
// infix on the left operand, then posfix once the right operand is ready.
func (fs *FuncState) binop(opr BinOpr, e1, e2 *expdesc) {
	fs.luaK_infix(opr, e1)
	fs.luaK_posfix(opr, e1, e2, 1)
}

// got returns the emitted bytecode as normalized "NAME a b c" strings.
func got(fs *FuncState) []string {
	lines := disasmLines(fs.f)
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return lines
}

func wantCode(t *testing.T, fs *FuncState, want ...string) {
	t.Helper()
	g := got(fs)
	if len(g) != len(want) {
		t.Fatalf("got %d instructions %v, want %d %v", len(g), g, len(want), want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("instr %d: got %q, want %q", i, g[i], want[i])
		}
	}
}

func TestLoadConstants(t *testing.T) {
	t.Run("small int via LOADI", func(t *testing.T) {
		fs := fsWith(0)
		e := intExp(5)
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADI 0 5")
	})
	t.Run("integral float via LOADF", func(t *testing.T) {
		fs := fsWith(0)
		e := fltExp(2.0)
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADF 0 2")
	})
	t.Run("non-integral float via LOADK", func(t *testing.T) {
		fs := fsWith(0)
		e := fltExp(1.5)
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADK 0 0")
		if got := fs.f.Constants[0]; !got.IsFloat() || got.AsFloat() != 1.5 {
			t.Errorf("constant[0] = %v, want float 1.5", got)
		}
	})
	t.Run("string via LOADK", func(t *testing.T) {
		fs := fsWith(0)
		e := strExp("hi")
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADK 0 0")
		if got := fs.f.Constants[0]; !got.IsString() || got.Str() != "hi" {
			t.Errorf("constant[0] = %v, want string \"hi\"", got)
		}
	})
	t.Run("nil via LOADNIL", func(t *testing.T) {
		fs := fsWith(0)
		e := nilExp()
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADNIL 0 0 0")
	})
	t.Run("adjacent nils merge into one LOADNIL", func(t *testing.T) {
		fs := fsWith(0)
		fs.luaK_exp2nextreg(nilExp())
		fs.luaK_exp2nextreg(nilExp())
		wantCode(t, fs, "LOADNIL 0 1 0")
	})
}

func TestConstantFolding(t *testing.T) {
	t.Run("int add folds", func(t *testing.T) {
		fs := fsWith(0)
		e1, e2 := intExp(1), intExp(2)
		fs.binop(OPR_ADD, e1, e2)
		fs.luaK_exp2nextreg(e1)
		wantCode(t, fs, "LOADI 0 3")
	})
	t.Run("int div is float", func(t *testing.T) {
		fs := fsWith(0)
		e1, e2 := intExp(3), intExp(2)
		fs.binop(OPR_DIV, e1, e2)
		fs.luaK_exp2nextreg(e1)
		wantCode(t, fs, "LOADK 0 0")
		if c := fs.f.Constants[0]; !c.IsFloat() || c.AsFloat() != 1.5 {
			t.Errorf("constant[0] = %v, want float 1.5", c)
		}
	})
	t.Run("division by zero is not folded", func(t *testing.T) {
		fs := fsWith(0)
		e1, e2 := intExp(1), intExp(0)
		fs.binop(OPR_IDIV, e1, e2)
		// Not folded: emits an IDIV register op (operands loaded first).
		code := got(fs)
		if len(code) == 0 || !strings.HasPrefix(code[len(code)-2], "IDIV") {
			t.Errorf("expected an IDIV op (no fold), got %v", code)
		}
	})
	t.Run("bitwise on non-integral float not folded", func(t *testing.T) {
		fs := fsWith(0)
		e1, e2 := fltExp(2.5), intExp(1)
		fs.binop(OPR_BAND, e1, e2)
		code := got(fs)
		if strings.HasPrefix(code[0], "LOADI") || strings.HasPrefix(code[0], "LOADK") && len(code) == 1 {
			// folded to a single constant load — wrong
			t.Errorf("2.5 & 1 must not fold, got %v", code)
		}
	})
}

func TestArithImmediateAndK(t *testing.T) {
	t.Run("local + small int uses ADDI + MMBINI", func(t *testing.T) {
		fs := fsWith(1) // local a in reg 0
		e1, e2 := localExp(0), intExp(1)
		fs.binop(OPR_ADD, e1, e2)
		fs.luaK_exp2nextreg(e1)
		// int2sC(1) = 1 + 127 = 128; TM_ADD = 6.
		wantCode(t, fs, "ADDI 1 0 128", "MMBINI 0 128 6")
	})
	t.Run("local + large constant uses ADDK + MMBINK", func(t *testing.T) {
		fs := fsWith(1)
		e1, e2 := localExp(0), intExp(1000) // out of sC range -> K operand
		fs.binop(OPR_ADD, e1, e2)
		fs.luaK_exp2nextreg(e1)
		wantCode(t, fs, "ADDK 1 0 0", "MMBINK 0 0 6")
		if c := fs.f.Constants[0]; !c.IsInt() || c.AsInt() != 1000 {
			t.Errorf("constant[0] = %v, want int 1000", c)
		}
	})
	t.Run("local - small int folds into ADDI of the negation", func(t *testing.T) {
		fs := fsWith(1)
		e1, e2 := localExp(0), intExp(1)
		fs.binop(OPR_SUB, e1, e2)
		fs.luaK_exp2nextreg(e1)
		// finishbinexpneg: ADDI with -1 (int2sC(-1)=126); MMBINI keeps +1 (128), TM_SUB=7.
		wantCode(t, fs, "ADDI 1 0 126", "MMBINI 0 128 7")
	})
	t.Run("two locals use register ADD + MMBIN", func(t *testing.T) {
		fs := fsWith(2) // locals a,b in regs 0,1
		e1, e2 := localExp(0), localExp(1)
		fs.binop(OPR_MUL, e1, e2)
		fs.luaK_exp2nextreg(e1)
		// MUL r r; MMBIN with TM_MUL=8.
		wantCode(t, fs, "MUL 2 0 1", "MMBIN 0 1 8")
	})
}

func TestComparisons(t *testing.T) {
	t.Run("local < local emits LT + JMP", func(t *testing.T) {
		fs := fsWith(2)
		e1, e2 := localExp(0), localExp(1)
		fs.binop(OPR_LT, e1, e2)
		wantCode(t, fs, "LT 0 1 0k", "JMP -1")
	})
	t.Run("local < small int uses immediate LTI", func(t *testing.T) {
		fs := fsWith(1)
		e1, e2 := localExp(0), intExp(3)
		fs.binop(OPR_LT, e1, e2)
		wantCode(t, fs, "LTI 0 130 0k", "JMP -1") // int2sC(3) = 130
	})
	t.Run("a > b is coded as b < a", func(t *testing.T) {
		fs := fsWith(2)
		e1, e2 := localExp(0), localExp(1)
		fs.binop(OPR_GT, e1, e2)
		// swapped: r1 = reg(b)=1, r2 = reg(a)=0
		wantCode(t, fs, "LT 1 0 0k", "JMP -1")
	})
	t.Run("equality with immediate uses EQI", func(t *testing.T) {
		fs := fsWith(1)
		e1, e2 := localExp(0), intExp(5)
		fs.binop(OPR_EQ, e1, e2)
		wantCode(t, fs, "EQI 0 132 0k", "JMP -1") // int2sC(5)=132, k=1 (==)
	})
}

func TestNotAndConcat(t *testing.T) {
	t.Run("not local emits NOT", func(t *testing.T) {
		fs := fsWith(1)
		e := localExp(0)
		fs.luaK_prefix(OPR_NOT, e, 1)
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "NOT 1 0 0")
	})
	t.Run("unary minus on constant folds", func(t *testing.T) {
		fs := fsWith(0)
		e := intExp(7)
		fs.luaK_prefix(OPR_MINUS, e, 1)
		fs.luaK_exp2nextreg(e)
		wantCode(t, fs, "LOADI 0 -7")
	})
	t.Run("concat of two locals copies then CONCAT", func(t *testing.T) {
		fs := fsWith(2)
		e1, e2 := localExp(0), localExp(1)
		fs.binop(OPR_CONCAT, e1, e2)
		wantCode(t, fs, "MOVE 2 0 0", "MOVE 3 1 0", "CONCAT 2 2 0")
	})
}

func TestIndexed(t *testing.T) {
	t.Run("field access emits GETFIELD with string key", func(t *testing.T) {
		fs := fsWith(1) // table t in reg 0
		tExp := localExp(0)
		fs.luaK_dischargevars(tExp)
		key := strExp("x")
		fs.luaK_indexed(tExp, key)
		fs.luaK_exp2nextreg(tExp)
		wantCode(t, fs, "GETFIELD 1 0 0")
		if c := fs.f.Constants[0]; !c.IsString() || c.Str() != "x" {
			t.Errorf("constant[0] = %v, want string \"x\"", c)
		}
	})
	t.Run("integer index emits GETI", func(t *testing.T) {
		fs := fsWith(1)
		tExp := localExp(0)
		fs.luaK_dischargevars(tExp)
		fs.luaK_indexed(tExp, intExp(2))
		fs.luaK_exp2nextreg(tExp)
		wantCode(t, fs, "GETI 1 0 2")
	})
}

func TestShortCircuit(t *testing.T) {
	// `a and b` (two locals): TESTSET on a, JMP, then b lands in the same reg.
	fs := fsWith(2)
	e1, e2 := localExp(0), localExp(1)
	fs.luaK_infix(OPR_AND, e1)
	fs.luaK_posfix(OPR_AND, e1, e2, 1)
	fs.luaK_exp2nextreg(e1)
	code := got(fs)
	if len(code) == 0 || !strings.HasPrefix(code[0], "TESTSET") {
		t.Fatalf("expected TESTSET first for `and`, got %v", code)
	}
	hasJmp := false
	for _, c := range code {
		if strings.HasPrefix(c, "JMP") {
			hasJmp = true
		}
	}
	if !hasJmp {
		t.Errorf("expected a JMP in short-circuit code, got %v", code)
	}
}
