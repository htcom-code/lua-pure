package luapure

// Proto is a compiled Lua 5.4 function prototype (PUC lobject.h Proto): the
// bytecode plus the constants, nested prototypes, upvalue descriptors and debug
// information a closure is instantiated from.
type Proto struct {
	NumParams    uint8 // number of fixed (named) parameters
	IsVararg     bool
	MaxStackSize uint8 // registers needed by this function

	Code      []Instruction // opcodes
	Constants []Value       // constant table (k)
	Protos    []*Proto      // functions defined inside this one (p)
	Upvalues  []UpvalDesc   // upvalue descriptors

	// Debug information.
	Source      string   // chunk name
	LineDefined int      // 0 for the main chunk
	LastLineDef int      // last line of the definition
	LineInfo    []int32  // source line per instruction (1:1 with Code)
	LocVars     []LocVar // local-variable debug records
}

// UpvalDesc describes one upvalue of a Proto (PUC lobject.h Upvaldesc).
type UpvalDesc struct {
	Name    string // upvalue name (debug)
	InStack bool   // captured from the enclosing function's registers (vs its upvalues)
	Index   uint8  // register index (InStack) or outer upvalue index
	Kind    uint8  // variable kind: regular / <const> / <close> / compile-time const
}

// Upvalue kinds (PUC lparser.h VDKREG / RDKCONST / RDKTOCLOSE / RDKCTC).
const (
	VarKindReg     uint8 = iota // regular variable
	VarKindConst                // <const>
	VarKindToClose              // <close> (to-be-closed)
	VarKindCTConst              // compile-time constant
)

// LocVar is a local-variable debug record (PUC lobject.h LocVar): the register
// holding it is implied by declaration order; StartPc..EndPc is its live range.
type LocVar struct {
	Name    string
	StartPc int // first pc where the variable is active
	EndPc   int // first pc where it is dead
}

// LineAt returns the source line for instruction pc. A proto with no line info
// (a stripped chunk) reports -1, matching PUC luaG_getfuncline, so error
// messages and tracebacks read ":-1:" rather than ":0:".
func (p *Proto) LineAt(pc int) int {
	if len(p.LineInfo) == 0 {
		return -1
	}
	if pc < 0 || pc >= len(p.LineInfo) {
		return 0
	}
	return int(p.LineInfo[pc])
}

// AddConstant appends v to the constant table and returns its index. (Dedup is
// the compiler's job; this is the raw append.)
func (p *Proto) AddConstant(v Value) int {
	p.Constants = append(p.Constants, v)
	return len(p.Constants) - 1
}
