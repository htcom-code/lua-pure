package luapure

import (
	"math"
	"unsafe"
)

// Value is a Lua 5.4 runtime value. It uses a tagged-struct representation:
// scalars are stored inline so integer/float arithmetic does not box, and GC
// objects are held through a normal pointer (GC-safe, unlike true NaN-boxing
// which Go's precise GC cannot follow).
//
// Layout (24 bytes on 64-bit): tag(1) + pad + scalar(8) + gc(8).
type Value struct {
	tag    vtag
	scalar uint64         // boolean (0/1), int64 bits, or float64 bits
	gc     unsafe.Pointer // string/table/function/... payload, else nil
}

// vtag is the discriminating tag. Integer and float are distinct tags (like
// PUC's VNUMINT/VNUMFLT variants) so the VM can dispatch on a single switch.
type vtag uint8

const (
	tagNil vtag = iota
	tagFalse
	tagTrue
	tagInt
	tagFloat
	tagString
	tagTable
	tagFunction
	tagUserData
	tagThread
	tagLightUserData
	// tagWeakRef is an internal-only tag: the payload stored in a weak table's
	// slot in place of a strong Value, so Go's GC does not retain the referent
	// (see weak.go). It is always materialized back to a real Value by the
	// table read path (deref) and never escapes to the VM or user code.
	tagWeakRef
)

// Basic Lua types (lua.h LUA_T*), as reported by Value.Type() and `type()`.
const (
	TypeNil           = 0
	TypeBoolean       = 1
	TypeLightUserData = 2
	TypeNumber        = 3
	TypeString        = 4
	TypeTable         = 5
	TypeFunction      = 6
	TypeUserData      = 7
	TypeThread        = 8
)

// luaString is the backing object for a string Value. Interning and the full
// GC string object come later; this carries the bytes the compiler needs for
// the constant table.
type luaString struct {
	s string
}

// maxShortLen (PUC LUAI_MAXSHORTLEN, the short/long string boundary) is defined
// in luaconf.go.

// --- constructors ---

// Nil is the nil value.
var Nil = Value{tag: tagNil}

// True and False are the boolean values.
var (
	True  = Value{tag: tagTrue}
	False = Value{tag: tagFalse}
)

// Bool returns the boolean Value for b.
func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

// Int returns an integer Value (Lua 5.3+ integer subtype).
func Int(i int64) Value {
	return Value{tag: tagInt, scalar: uint64(i)}
}

// Float returns a float Value.
func Float(f float64) Value {
	return Value{tag: tagFloat, scalar: math.Float64bits(f)}
}

// MkString returns a string Value backing the given Go string.
func MkString(s string) Value {
	return Value{tag: tagString, gc: unsafe.Pointer(&luaString{s: s})}
}

// --- predicates ---

func (v Value) IsNil() bool    { return v.tag == tagNil }
func (v Value) IsBool() bool   { return v.tag == tagFalse || v.tag == tagTrue }
func (v Value) IsInt() bool    { return v.tag == tagInt }
func (v Value) IsFloat() bool  { return v.tag == tagFloat }
func (v Value) IsNumber() bool { return v.tag == tagInt || v.tag == tagFloat }
func (v Value) IsString() bool { return v.tag == tagString }

// IsFalsy reports whether v is nil or false (Lua truthiness).
func (v Value) IsFalsy() bool { return v.tag == tagNil || v.tag == tagFalse }

// --- accessors (callers must check the tag first) ---

// Bool returns the boolean payload; valid only when IsBool.
func (v Value) AsBool() bool { return v.tag == tagTrue }

// AsInt returns the integer payload; valid only when IsInt.
func (v Value) AsInt() int64 { return int64(v.scalar) }

// AsFloat returns the float payload; valid only when IsFloat.
func (v Value) AsFloat() float64 { return math.Float64frombits(v.scalar) }

// Str returns the string payload; valid only when IsString.
func (v Value) Str() string { return (*luaString)(v.gc).s }

// Type returns the basic Lua type tag (lua.h LUA_T*).
func (v Value) Type() int {
	switch v.tag {
	case tagNil:
		return TypeNil
	case tagFalse, tagTrue:
		return TypeBoolean
	case tagInt, tagFloat:
		return TypeNumber
	case tagString:
		return TypeString
	case tagTable:
		return TypeTable
	case tagFunction:
		return TypeFunction
	case tagUserData:
		return TypeUserData
	case tagThread:
		return TypeThread
	case tagLightUserData:
		return TypeLightUserData
	}
	return TypeNil
}

// RawEqual reports primitive (no-metamethod) equality, matching PUC luaV_equalobj
// for the cases representable so far. Integer and float compare by mathematical
// value (1 == 1.0); strings compare by contents.
func (a Value) RawEqual(b Value) bool {
	switch {
	case a.tag == b.tag:
		switch a.tag {
		case tagNil, tagFalse, tagTrue:
			return true
		case tagInt:
			return int64(a.scalar) == int64(b.scalar)
		case tagFloat:
			return a.AsFloat() == b.AsFloat()
		case tagString:
			return a.Str() == b.Str()
		default:
			return a.gc == b.gc
		}
	case a.IsNumber() && b.IsNumber():
		// Mixed int/float: compare as the same number when exactly representable.
		if a.tag == tagInt {
			return float64(a.AsInt()) == b.AsFloat() && int64(b.AsFloat()) == a.AsInt()
		}
		return float64(b.AsInt()) == a.AsFloat() && int64(a.AsFloat()) == b.AsInt()
	}
	return false
}
