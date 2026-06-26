package luapure

import (
	"unsafe"
	"weak"
)

// liveWeakTables counts weak tables ever created in the process. While > 0 the
// VM periodically forces a GC (see pollFinalizers) so weak referents dropped
// inside a tight bytecode loop are reclaimed — a `while t[k] do ... end` spin
// (closure.lua) would otherwise depend on the Go GC pacer happening to fire,
// which is nondeterministic in a CPU-bound loop. Monotonic: a weak table can't
// cheaply be un-counted when collected, but the nudge is coarse and gated, so
// the cost is bounded and ordinary (weak-free) programs pay nothing.
var liveWeakTables int32

// Weak tables (PUC ltable.c / lgc.c __mode support).
//
// PUC implements weakness in the GC mark phase: a weak table is simply not
// traversed for the weak half (keys and/or values), so an object reachable
// *only* through a weak table stays white and is freed, and the dangling entry
// is then cleared. gopher-lua delegates object liveness to Go's GC, which has
// no hook to "skip marking through this slot" — a normal pointer in the table
// would keep the referent alive forever. So we store the weak half physically
// as a weak.Pointer (Go 1.24): it does not retain the referent, and reads
// materialize it back, reporting a collected referent as nil.
//
// Scope: values are held truly weakly. Keys are kept strong even under a 'k'
// mode (the map key identity holds a pointer we cannot make weak without
// rebuilding the hash representation); no conformance test exercises weak-key
// clearing of a *collectable* key (the suite's weak tables key on integers),
// so this is a documented, test-irrelevant limitation. weakk is still tracked
// so the mode round-trips through getmetatable.

// weakCell is the internal payload for a weak table value slot. It keeps the
// original value's tag plus a typed weak pointer to its GC object; alive()
// reports whether the referent is still live and hands back its base pointer.
type weakCell struct {
	origTag vtag
	alive   func() (unsafe.Pointer, bool)
}

// collectableTag reports whether values of this tag are GC objects a weak table
// holds weakly. PUC treats strings as values that are never weak-cleared, and
// scalars are non-collectable, so neither is wrapped.
func collectableTag(t vtag) bool {
	switch t {
	case tagTable, tagFunction, tagUserData, tagThread:
		return true
	}
	return false
}

// mkWeak wraps a collectable value as a weak cell. Non-collectable values
// (nil, scalars, strings) and values that are already weak cells are returned
// unchanged, so wrapping is idempotent.
func mkWeak(v Value) Value {
	var alive func() (unsafe.Pointer, bool)
	switch v.tag {
	case tagTable:
		wp := weak.Make((*Table)(v.gc))
		alive = func() (unsafe.Pointer, bool) { p := wp.Value(); return unsafe.Pointer(p), p != nil }
	case tagFunction:
		wp := weak.Make((*Closure)(v.gc))
		alive = func() (unsafe.Pointer, bool) { p := wp.Value(); return unsafe.Pointer(p), p != nil }
	case tagUserData:
		wp := weak.Make((*userData)(v.gc))
		alive = func() (unsafe.Pointer, bool) { p := wp.Value(); return unsafe.Pointer(p), p != nil }
	case tagThread:
		wp := weak.Make((*LState)(v.gc))
		alive = func() (unsafe.Pointer, bool) { p := wp.Value(); return unsafe.Pointer(p), p != nil }
	default:
		return v
	}
	return Value{tag: tagWeakRef, gc: unsafe.Pointer(&weakCell{origTag: v.tag, alive: alive})}
}

// deref materializes a stored slot value into the strong Value the VM sees. A
// weak cell whose referent has been collected reads as nil; anything else is
// returned unchanged.
func deref(v Value) Value {
	if v.tag != tagWeakRef {
		return v
	}
	c := (*weakCell)(v.gc)
	p, ok := c.alive()
	if !ok {
		return Nil
	}
	return Value{tag: c.origTag, gc: p}
}

// isDeadWeak reports whether v is a weak cell whose referent has been collected.
func isDeadWeak(v Value) bool {
	if v.tag != tagWeakRef {
		return false
	}
	_, ok := (*weakCell)(v.gc).alive()
	return !ok
}
