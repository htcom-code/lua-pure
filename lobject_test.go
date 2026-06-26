package luapure

import (
	"testing"
	"unsafe"
)

func TestValueSize(t *testing.T) {
	// The tagged-struct decision targets 24 bytes on 64-bit (tag+pad, scalar,
	// gc pointer). Guard against accidental bloat.
	if sz := unsafe.Sizeof(Value{}); sz != 24 {
		t.Errorf("sizeof(Value) = %d, want 24", sz)
	}
}

func TestScalarConstructors(t *testing.T) {
	if !Nil.IsNil() || Nil.Type() != TypeNil {
		t.Error("Nil")
	}
	if !True.AsBool() || True.AsBool() != true || !True.IsBool() || True.Type() != TypeBoolean {
		t.Error("True")
	}
	if False.AsBool() || !False.IsBool() {
		t.Error("False")
	}
	if Bool(true) != True || Bool(false) != False {
		t.Error("Bool()")
	}

	for _, i := range []int64{0, 1, -1, 1 << 62, -(1 << 62)} {
		v := Int(i)
		if !v.IsInt() || !v.IsNumber() || v.AsInt() != i || v.Type() != TypeNumber {
			t.Errorf("Int(%d): isInt=%v got=%d", i, v.IsInt(), v.AsInt())
		}
	}
	for _, f := range []float64{0, 1.5, -2.25, 1e300, -1e-300} {
		v := Float(f)
		if !v.IsFloat() || !v.IsNumber() || v.AsFloat() != f || v.Type() != TypeNumber {
			t.Errorf("Float(%g): isFloat=%v got=%g", f, v.IsFloat(), v.AsFloat())
		}
	}
}

func TestStringValue(t *testing.T) {
	v := MkString("hello")
	if !v.IsString() || v.Type() != TypeString {
		t.Error("string predicates")
	}
	if v.Str() != "hello" {
		t.Errorf("Str() = %q", v.Str())
	}
	if MkString("").Str() != "" {
		t.Error("empty string")
	}
}

func TestFalsy(t *testing.T) {
	if !Nil.IsFalsy() || !False.IsFalsy() {
		t.Error("nil/false should be falsy")
	}
	if True.IsFalsy() || Int(0).IsFalsy() || Float(0).IsFalsy() || MkString("").IsFalsy() {
		t.Error("true/0/0.0/'' must be truthy in Lua")
	}
}

func TestRawEqual(t *testing.T) {
	cases := []struct {
		a, b Value
		want bool
	}{
		{Nil, Nil, true},
		{True, True, true},
		{True, False, false},
		{Int(5), Int(5), true},
		{Int(5), Int(6), false},
		{Float(1.5), Float(1.5), true},
		{Int(2), Float(2.0), true},  // mixed int/float, equal value
		{Int(2), Float(2.5), false}, // different value
		{Float(2.0), Int(2), true},  // symmetric
		{MkString("a"), MkString("a"), true},
		{MkString("a"), MkString("b"), false},
		{Int(0), Nil, false},
		{Int(0), False, false},
	}
	for _, c := range cases {
		if got := c.a.RawEqual(c.b); got != c.want {
			t.Errorf("RawEqual(%+v, %+v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	// A huge integer not exactly representable as the compared float must differ.
	big := int64(1)<<53 + 1
	if Int(big).RawEqual(Float(float64(big))) {
		t.Errorf("Int(%d) should not equal its lossy float", big)
	}
}

func TestProtoBasics(t *testing.T) {
	p := &Proto{Source: "@x.lua", NumParams: 2}
	p.Code = []Instruction{CreateABCk(OP_MOVE, 0, 1, 0, 0), CreateABx(OP_LOADK, 1, 0)}
	p.LineInfo = []int32{10, 11}

	if got := p.AddConstant(MkString("k0")); got != 0 {
		t.Errorf("AddConstant idx = %d, want 0", got)
	}
	if got := p.AddConstant(Int(42)); got != 1 {
		t.Errorf("AddConstant idx = %d, want 1", got)
	}
	if len(p.Constants) != 2 || p.Constants[0].Str() != "k0" || p.Constants[1].AsInt() != 42 {
		t.Error("constant table")
	}
	if p.LineAt(0) != 10 || p.LineAt(1) != 11 {
		t.Errorf("LineAt: %d %d", p.LineAt(0), p.LineAt(1))
	}
	if p.LineAt(-1) != 0 || p.LineAt(99) != 0 {
		t.Error("LineAt out of range should be 0")
	}
}
