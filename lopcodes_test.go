package luapure

import "testing"

func TestNumOpCodesAndNames(t *testing.T) {
	if NumOpCodes != 83 {
		t.Fatalf("NumOpCodes = %d, want 83 (PUC 5.4.8)", NumOpCodes)
	}
	for op := 0; op < NumOpCodes; op++ {
		if opNames[op] == "" {
			t.Errorf("opcode %d has empty name", op)
		}
		// A zero opmode byte is valid (e.g. SETUPVAL: a=0, mode=iABC), so we
		// can't detect missing entries that way; the index-keyed opModes literal
		// covers all opcodes and TestOpModeProperties spot-checks the values.
	}
	// Last opcode name sanity (ORDER OP).
	if OP_EXTRAARG.Name() != "EXTRAARG" {
		t.Errorf("OP_EXTRAARG.Name() = %q", OP_EXTRAARG.Name())
	}
}

func TestFieldConstants(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"SizeBx", SizeBx, 17}, {"SizeAx", SizeAx, 25}, {"SizeJ", SizeJ, 25},
		{"PosA", PosA, 7}, {"PosK", PosK, 15}, {"PosB", PosB, 16}, {"PosC", PosC, 24},
		{"MaxArgBx", MaxArgBx, 131071}, {"OffsetsBx", OffsetsBx, 65535},
		{"MaxArgA", MaxArgA, 255}, {"MaxArgC", MaxArgC, 255}, {"OffsetsC", OffsetsC, 127},
		{"OffsetsJ", OffsetsJ, 16777215},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestABCkRoundTrip(t *testing.T) {
	// Every field at its maximum, k set — verify independence (no cross-field
	// corruption) and that the opcode survives.
	i := CreateABCk(OP_GETFIELD, MaxArgA, MaxArgB, MaxArgC, 1)
	if op := GetOpCode(i); op != OP_GETFIELD {
		t.Errorf("opcode = %v, want OP_GETFIELD", op)
	}
	if GetArgA(i) != MaxArgA || GetArgB(i) != MaxArgB || GetArgC(i) != MaxArgC {
		t.Errorf("A/B/C = %d/%d/%d", GetArgA(i), GetArgB(i), GetArgC(i))
	}
	if !Testk(i) || GetArgk(i) != 1 {
		t.Errorf("k not set: Testk=%v GetArgk=%d", Testk(i), GetArgk(i))
	}

	// k clear.
	i = CreateABCk(OP_ADD, 1, 2, 3, 0)
	if Testk(i) {
		t.Errorf("k should be clear")
	}
	if GetArgA(i) != 1 || GetArgB(i) != 2 || GetArgC(i) != 3 {
		t.Errorf("A/B/C = %d/%d/%d, want 1/2/3", GetArgA(i), GetArgB(i), GetArgC(i))
	}
}

func TestSettersIndependent(t *testing.T) {
	var i Instruction
	SetOpCode(&i, OP_NEWTABLE)
	SetArgA(&i, 200)
	SetArgB(&i, 100)
	SetArgC(&i, 50)
	SetArgk(&i, 1)
	if GetOpCode(i) != OP_NEWTABLE {
		t.Errorf("opcode corrupted: %v", GetOpCode(i))
	}
	if GetArgA(i) != 200 || GetArgB(i) != 100 || GetArgC(i) != 50 || GetArgk(i) != 1 {
		t.Errorf("fields: A=%d B=%d C=%d k=%d", GetArgA(i), GetArgB(i), GetArgC(i), GetArgk(i))
	}
	// Rewriting one field must not disturb the others.
	SetArgB(&i, 0)
	if GetArgA(i) != 200 || GetArgC(i) != 50 || GetArgk(i) != 1 || GetOpCode(i) != OP_NEWTABLE {
		t.Errorf("SetArgB disturbed other fields: A=%d C=%d k=%d op=%v",
			GetArgA(i), GetArgC(i), GetArgk(i), GetOpCode(i))
	}
}

func TestBxAndsBxRoundTrip(t *testing.T) {
	i := CreateABx(OP_LOADK, 5, MaxArgBx)
	if GetOpCode(i) != OP_LOADK || GetArgA(i) != 5 || GetArgBx(i) != MaxArgBx {
		t.Errorf("ABx: op=%v A=%d Bx=%d", GetOpCode(i), GetArgA(i), GetArgBx(i))
	}
	for _, v := range []int{-OffsetsBx, -1, 0, 1, OffsetsBx} {
		var x Instruction
		SetOpCode(&x, OP_LOADI)
		SetArgA(&x, 3)
		SetArgsBx(&x, v)
		if got := GetArgsBx(x); got != v {
			t.Errorf("sBx round-trip: set %d got %d", v, got)
		}
		if GetArgA(x) != 3 {
			t.Errorf("sBx clobbered A: %d", GetArgA(x))
		}
	}
}

func TestSCRoundTrip(t *testing.T) {
	// ADDI uses sC in C; verify excess-K encoding round-trips, negatives included.
	for _, v := range []int{-OffsetsC, -1, 0, 1, OffsetsC} {
		i := CreateABCk(OP_ADDI, 1, 2, Int2sC(v), 0)
		if got := GetArgsC(i); got != v {
			t.Errorf("sC round-trip: set %d got %d", v, got)
		}
	}
}

func TestSJRoundTrip(t *testing.T) {
	for _, v := range []int{-OffsetsJ, -1000, -1, 0, 1, 1000, OffsetsJ} {
		i := CreatesJ(OP_JMP, v, 0)
		if GetOpCode(i) != OP_JMP {
			t.Errorf("sJ opcode = %v", GetOpCode(i))
		}
		if got := GetArgsJ(i); got != v {
			t.Errorf("sJ round-trip: set %d got %d", v, got)
		}
	}
}

func TestAxRoundTrip(t *testing.T) {
	i := CreateAx(OP_EXTRAARG, MaxArgAx)
	if GetOpCode(i) != OP_EXTRAARG || GetArgAx(i) != MaxArgAx {
		t.Errorf("Ax: op=%v Ax=%d", GetOpCode(i), GetArgAx(i))
	}
}

func TestOpModeProperties(t *testing.T) {
	// Spot-check the ported luaP_opmodes against lopcodes.c.
	check := func(op OpCode, mode OpMode, a, test, mm bool) {
		if op.Mode() != mode {
			t.Errorf("%s mode = %v, want %v", op.Name(), op.Mode(), mode)
		}
		if op.SetsA() != a {
			t.Errorf("%s SetsA = %v, want %v", op.Name(), op.SetsA(), a)
		}
		if op.IsTest() != test {
			t.Errorf("%s IsTest = %v, want %v", op.Name(), op.IsTest(), test)
		}
		if op.IsMM() != mm {
			t.Errorf("%s IsMM = %v, want %v", op.Name(), op.IsMM(), mm)
		}
	}
	check(OP_MOVE, IABC, true, false, false)
	check(OP_LOADI, IAsBx, true, false, false)
	check(OP_LOADK, IABx, true, false, false)
	check(OP_SETUPVAL, IABC, false, false, false)
	check(OP_MMBIN, IABC, false, false, true)
	check(OP_MMBINK, IABC, false, false, true)
	check(OP_JMP, IsJ, false, false, false)
	check(OP_EQ, IABC, false, true, false)
	check(OP_TESTSET, IABC, true, true, false)
	check(OP_FORLOOP, IABx, true, false, false)
	check(OP_TFORCALL, IABC, false, false, false)
	check(OP_EXTRAARG, IAx, false, false, false)

	// CALL: sets A, uses top-in and sets top-out.
	if !OP_CALL.UsesTopIn() || !OP_CALL.SetsTopOut() || !OP_CALL.SetsA() {
		t.Errorf("OP_CALL flags wrong: in=%v out=%v a=%v",
			OP_CALL.UsesTopIn(), OP_CALL.SetsTopOut(), OP_CALL.SetsA())
	}
	// RETURN: uses top-in only.
	if !OP_RETURN.UsesTopIn() || OP_RETURN.SetsTopOut() {
		t.Errorf("OP_RETURN flags wrong: in=%v out=%v", OP_RETURN.UsesTopIn(), OP_RETURN.SetsTopOut())
	}
}
