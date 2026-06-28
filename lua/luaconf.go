package luapure

// luaconf.go consolidates the configuration knobs that PUC Lua keeps in
// luaconf.h (plus a few tunable limits PUC #defines in .c files). PUC bakes
// these in at compile time — one value for the whole library, never per
// lua_State — so the faithful Go analog is package-level configuration.
//
// The Tunable vars below default to stock PUC values, reproducing PUC behavior
// out of the box. An embedder may override them, but the contract mirrors PUC's
// compile-time nature: set them ONCE at startup, before creating any State.
// Mutating a Tunable while a State is executing races with that State and is
// not supported. There is no runtime validation (PUC has none either); a
// nonsensical value (e.g. a zero or negative limit) yields undefined behavior.
//
// Three of these are read while a State runs and can additionally be overridden
// per State at construction — see the NewState options WithMaxStack,
// WithMaxCCalls, and WithMaxTableArraySize. Each State snapshots the package
// globals at NewState; an option then overrides its own copy. The rest stay
// process-wide because they are read by the stateless compiler, which has no
// State to carry per-instance config.

// --- Tunable limits (settable; defaults match stock PUC luaconf.h) ---

var (
	// MaxStack bounds the value stack (PUC LUAI_MAXSTACK), so unbounded
	// recursion raises "stack overflow" instead of exhausting memory.
	MaxStack = 1000000

	// ErrorStackReserve is slack above MaxStack (PUC ERRORSTACKSIZE) kept free
	// so that, after a "stack overflow" is raised, the message handler and any
	// to-be-closed variables still have room to run.
	ErrorStackReserve = 200

	// MaxCCalls bounds nested Go-level calls (metamethods, pcall, hooks, native
	// callbacks), matching PUC's LUAI_MAXCCALLS. Exceeding it raises a catchable
	// error rather than letting Go recursion overflow the real stack and abort.
	MaxCCalls = 200

	// LoadBufferSize is the disk read-block size for streaming file loads (PUC
	// getF reads BUFSIZ at a time); a file is fed to the compiler one block at a
	// time so a large source is lexed incrementally and never held whole.
	LoadBufferSize = 8192

	// IDSize is the length to which chunk source names are truncated in
	// messages (PUC LUA_IDSIZE), via luaO_chunkid / shortSrc.
	IDSize = 60

	// MaxTagLoop limits __index/__newindex metamethod chains (lvm.c MAXTAGLOOP)
	// before raising "'__index' chain too long; possible loop".
	MaxTagLoop = 2000

	// MaxTableArraySize, when > 0, caps how far a table's array part may grow
	// before extending it raises a catchable "not enough memory" error. Go
	// cannot turn a real allocation failure into a recoverable error (OOM is a
	// fatal runtime throw), so a program that fills a table without bound (Lua's
	// heavy.lua toomanyidx: `for i=1,math.huge do a[i]=i end`) would crash the
	// host process instead of erroring. This ceiling stands in for PUC's
	// malloc-failure path. 0 (the default) preserves unlimited growth.
	MaxTableArraySize int

	// MaxLexElement, when > 0, caps the length of a single lexical element (an
	// identifier, string, or number token). PUC's save raises "lexical element
	// too long" when the token buffer would grow past MAX_SIZE; on a 64-bit
	// build that is effectively unreachable, so an unbounded token (heavy.lua's
	// hugeid, a reader feeding one endless identifier) OOMs the host instead.
	// This ceiling raises the catchable error first. 0 (the default) keeps the
	// unbounded behavior.
	MaxLexElement int
)

// --- Structural limits (DO NOT CHANGE) ---
//
// These are luaconf-flavored limits collected here for a single source of
// truth, but unlike the Tunables above they are bytecode/layout invariants:
// changing them corrupts compiled chunks or overruns fixed-size structures, so
// they stay const.

const (
	// maxCaptures is the pattern-matching capture limit (PUC LUA_MAXCAPTURES);
	// it sizes the fixed capture array in lstrlib_pattern.go.
	maxCaptures = 32

	// maxVars is the per-function local-variable limit (PUC MAXVARS).
	maxVars = 200

	// maxUpvalues is the per-function upvalue limit (PUC MAXUPVAL); it must fit
	// in the 8-bit upvalue index of the instruction format.
	maxUpvalues = 255

	// maxRegisters is the per-function register ceiling (PUC MAXREGS); it must
	// fit in 8 bits.
	maxRegisters = 255

	// maxShortLen is PUC LUAI_MAXSHORTLEN: strings up to this length are treated
	// as "short" (interned for identity) and can key GETFIELD/SETFIELD; longer
	// strings are "long". The boundary is part of the bytecode contract.
	maxShortLen = 40
)
