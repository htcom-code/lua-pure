package luapure

// Closures and upvalues (PUC lfunc.c / lobject.h union Closure).
//
// PUC distinguishes LClosure (Lua function + UpVal pointers), CClosure (C
// function + boxed upvalues) and LUA_VLCF (a bare C function). This port folds
// all three into one Closure struct discriminated by `kind`, since the VM only
// ever needs to ask "Lua or native?" and read the relevant fields.

type funcKind uint8

const (
	funcLua funcKind = iota // proto + upvals
	funcGo                  // gofn + goUpvals
)

// GoFunc is a native (C-style) function: it reads its arguments from and pushes
// its results onto L's stack, returning the number of results, exactly like a
// lua_CFunction.
type GoFunc func(L *LState) int

// Closure is a callable value (Value.tag == tagFunction).
type Closure struct {
	kind funcKind

	// funcLua
	proto  *Proto
	upvals []*Upvalue

	// funcGo
	gofn     GoFunc
	goUpvals []Value
	name     string // debug name for native functions
}

func newLuaClosure(p *Proto) *Closure {
	return &Closure{kind: funcLua, proto: p, upvals: make([]*Upvalue, len(p.Upvalues))}
}

func newGoClosure(fn GoFunc, name string, nups int) *Closure {
	c := &Closure{kind: funcGo, gofn: fn, name: name}
	if nups > 0 {
		c.goUpvals = make([]Value, nups)
	}
	return c
}

func (c *Closure) isLua() bool { return c.kind == funcLua }

// Upvalue is a shared variable reference (PUC UpVal). While "open" it aliases a
// live stack slot (L.stack[idx]); once "closed" it owns the value in `v`.
// Because the stack is addressed by index rather than by pointer, stack growth
// never invalidates an open upvalue — no pointer fix-up is needed.
type Upvalue struct {
	l   *LState
	idx int   // stack index when open; -1 when closed
	v   Value // value when closed
}

func (u *Upvalue) isOpen() bool { return u.idx >= 0 }
func (u *Upvalue) level() int   { return u.idx }

func (u *Upvalue) get() Value {
	if u.idx >= 0 {
		return u.l.stack[u.idx]
	}
	return u.v
}

func (u *Upvalue) set(x Value) {
	if u.idx >= 0 {
		u.l.stack[u.idx] = x
		return
	}
	u.v = x
}

// close detaches the upvalue from the stack, copying the current value into its
// own storage (luaF_closeupval moves the slot into UpVal->u.value).
func (u *Upvalue) close() {
	if u.idx >= 0 {
		u.v = u.l.stack[u.idx]
		u.idx = -1
	}
}
