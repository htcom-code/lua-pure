package luapure

import "fmt"

// This file is the AST-walking front half of the Lua 5.4 compiler. Where PUC's
// lparser.c both parses tokens and drives lcode in one pass, here the grammar
// work is already done — the `parse` package produced an AST — so codegen.go
// only ports lparser.c's *code-driving* logic: active-variable / upvalue /
// goto-label bookkeeping, block and function scoping, and the per-node emission
// that calls the lcode machinery in lcode.go. The control flow mirrors
// lparser.c function-for-function so it can be checked against the original.

// maxVars (PUC MAXVARS) and maxUpvalues (PUC MAXUPVAL) are defined in luaconf.go.

// hasMultRet reports whether an expression can yield a variable number of
// results (PUC hasmultret).
func hasMultRet(k expkind) bool { return k == VCALL || k == VVARARG }

// --- compiler-wide state (PUC LexState + Dyndata, codegen-relevant parts) ---

// varDesc is an active local-variable descriptor (PUC Vardesc).
type varDesc struct {
	kind uint8  // VarKind*
	ridx int    // register holding the variable
	pidx int    // index into Proto.LocVars (debug info)
	name string // variable name
	val  Value  // compile-time constant value (kind == VarKindCTConst)
}

// labelDesc describes a pending goto or an active label (PUC Labeldesc).
type labelDesc struct {
	name    string
	pc      int
	line    int
	nactvar int
	close   bool // jump escapes an upvalue and needs a CLOSE
}

// blockCnt tracks one lexical block (PUC BlockCnt).
type blockCnt struct {
	previous   *blockCnt
	firstlabel int
	firstgoto  int
	nactvar    int  // active locals outside the block
	upval      bool // some block variable is captured as an upvalue
	isloop     bool
	insidetbc  bool // inside the scope of a to-be-closed variable
}

// compiler holds the state shared across all nested functions of one chunk
// (PUC LexState.dyd plus the bits of LexState codegen needs).
type compiler struct {
	fs      *FuncState  // innermost function being compiled
	actvar  []varDesc   // active-variable backing store (Dyndata.actvar.arr)
	nactvar int         // logical count of active variables (Dyndata.actvar.n)
	gt      []labelDesc // pending gotos (Dyndata.gt)
	label   []labelDesc // active labels (Dyndata.label)
	source  string      // chunk name
	envName string      // name of the environment upvalue ("_ENV")

	strCache map[string]Value // per-chunk string-literal intern (PUC scanner table)
	kcache   map[constKey]int // chunk-wide constant-index cache (PUC ls->h)
}

// internConst returns a canonical string-constant Value for s, shared across all
// functions of this chunk so identical literals reuse one backing object.
func (c *compiler) internConst(s string) Value {
	if v, ok := c.strCache[s]; ok {
		return v
	}
	if c.strCache == nil {
		c.strCache = make(map[string]Value)
	}
	v := MkString(s)
	c.strCache[s] = v
	return v
}

// semError aborts compilation with a semantic error (PUC luaK_semerror).
// PUC clears ls->t.token (drops the "near <token>" suffix) and routes through
// luaX_syntaxerror, which prefixes the message with "chunkid:linenumber: ".
func (c *compiler) semError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := 0
	if c.fs != nil {
		line = c.fs.lastline
	}
	full := fmt.Sprintf("%s:%d: %s", shortSrc(c.source), line, msg)
	panic(&CompileError{Msg: full, Line: line})
}

// errorLimit reports exceeding a compiler limit, naming the enclosing function
// like PUC errorlimit: "too many <what> (limit is <n>) in <where>", where
// <where> is "main function" or "function at line <linedefined>".
func (c *compiler) errorLimit(limit int, what string) {
	where := "main function"
	if c.fs != nil && c.fs.f != nil && c.fs.f.LineDefined != 0 {
		where = fmt.Sprintf("function at line %d", c.fs.f.LineDefined)
	}
	c.semError("too many %s (limit is %d) in %s", what, limit, where)
}

// --- active-variable management ---

// reglevel converts a compiler variable level to a register index, skipping
// compile-time constants which occupy no register (PUC reglevel).
func (fs *FuncState) reglevel(nvar int) int {
	for nvar > 0 {
		nvar--
		vd := &fs.ls.actvar[fs.firstlocal+nvar]
		if vd.kind != VarKindCTConst {
			return vd.ridx + 1
		}
	}
	return 0
}

// localVarDesc returns the descriptor of active variable vidx (PUC getlocalvardesc).
func (fs *FuncState) localVarDesc(vidx int) *varDesc {
	return &fs.ls.actvar[fs.firstlocal+vidx]
}

// registerLocalVar records a local variable for debug info (PUC registerlocalvar).
func (c *compiler) registerLocalVar(fs *FuncState, name string) int {
	fs.f.LocVars = append(fs.f.LocVars, LocVar{Name: name, StartPc: fs.pc})
	fs.ndebugvars = len(fs.f.LocVars)
	return fs.ndebugvars - 1
}

// newLocalVar creates a new active local variable, returning its function-local
// index (PUC new_localvar).
func (c *compiler) newLocalVar(name string) int {
	fs := c.fs
	if c.nactvar+1-fs.firstlocal > maxVars {
		c.errorLimit(maxVars, "local variables")
	}
	// The backing store is never shrunk (PUC keeps the array and tracks a count),
	// so reuse a freed slot when one exists rather than appending.
	vd := varDesc{kind: VarKindReg, name: name}
	if c.nactvar < len(c.actvar) {
		c.actvar[c.nactvar] = vd
	} else {
		c.actvar = append(c.actvar, vd)
	}
	c.nactvar++
	return c.nactvar - 1 - fs.firstlocal
}

// initVar builds an expression referring to local variable vidx (PUC init_var).
func (c *compiler) initVar(fs *FuncState, e *expdesc, vidx int) {
	e.t, e.f = noJump, noJump
	e.k = VLOCAL
	e.vr.vidx = vidx
	e.vr.ridx = fs.localVarDesc(vidx).ridx
}

// adjustLocalVars starts the scope of the last nvars created variables, assigning
// them registers and debug records (PUC adjustlocalvars).
func (c *compiler) adjustLocalVars(nvars int) {
	fs := c.fs
	reglvl := fs.reglevel(fs.nactvar)
	for i := 0; i < nvars; i++ {
		vidx := fs.nactvar
		fs.nactvar++
		vd := fs.localVarDesc(vidx)
		vd.ridx = reglvl
		reglvl++
		vd.pidx = c.registerLocalVar(fs, vd.name)
	}
}

// removeVars closes the scope of locals above tolevel, stamping their debug
// end-pc (PUC removevars).
func (c *compiler) removeVars(fs *FuncState, tolevel int) {
	c.nactvar -= fs.nactvar - tolevel // drop from the logical count; keep the backing store
	for fs.nactvar > tolevel {
		fs.nactvar--
		vd := &c.actvar[fs.firstlocal+fs.nactvar]
		if vd.kind != VarKindCTConst {
			fs.f.LocVars[vd.pidx].EndPc = fs.pc
		}
	}
}

// checkReadonly raises an error when e refers to a read-only variable
// (PUC check_readonly).
func (c *compiler) checkReadonly(e *expdesc) {
	fs := c.fs
	var name string
	switch e.k {
	case VCONST:
		name = c.actvar[e.info].name
	case VLOCAL:
		if vd := fs.localVarDesc(e.vr.vidx); vd.kind != VarKindReg {
			name = vd.name
		}
	case VUPVAL:
		if up := fs.f.Upvalues[e.info]; up.Kind != VarKindReg {
			name = up.Name
		}
	default:
		return
	}
	if name != "" {
		c.semError("attempt to assign to const variable '%s'", name)
	}
}

// --- upvalue management ---

func (c *compiler) searchUpvalue(fs *FuncState, name string) int {
	for i := 0; i < fs.nups; i++ {
		if fs.f.Upvalues[i].Name == name {
			return i
		}
	}
	return -1
}

func (c *compiler) allocUpvalue(fs *FuncState) int {
	if fs.nups+1 > maxUpvalues {
		c.errorLimit(maxUpvalues, "upvalues")
	}
	fs.f.Upvalues = append(fs.f.Upvalues, UpvalDesc{})
	fs.nups = len(fs.f.Upvalues)
	return fs.nups - 1
}

// newUpvalue records a new upvalue of fs capturing v from the enclosing function
// (PUC newupvalue).
func (c *compiler) newUpvalue(fs *FuncState, name string, v *expdesc) int {
	idx := c.allocUpvalue(fs)
	up := &fs.f.Upvalues[idx]
	if v.k == VLOCAL {
		up.InStack = true
		up.Index = uint8(v.vr.ridx)
		up.Kind = fs.prev.localVarDesc(v.vr.vidx).kind
	} else {
		up.InStack = false
		up.Index = uint8(v.info)
		up.Kind = fs.prev.f.Upvalues[v.info].Kind
	}
	up.Name = name
	return idx
}

// searchVar looks up an active local in fs (PUC searchvar). On success it fills
// var and returns its kind; otherwise -1.
func (c *compiler) searchVar(fs *FuncState, n string, v *expdesc) expkind {
	for i := fs.nactvar - 1; i >= 0; i-- {
		vd := &c.actvar[fs.firstlocal+i]
		if vd.name == n {
			if vd.kind == VarKindCTConst {
				initExp(v, VCONST, fs.firstlocal+i)
			} else {
				c.initVar(fs, v, i)
			}
			return v.k
		}
	}
	return -1
}

// markUpval marks the block defining the variable at the given level as having a
// captured upvalue, so it emits a CLOSE later (PUC markupval).
func (c *compiler) markUpval(fs *FuncState, level int) {
	bl := fs.bl
	for bl.nactvar > level {
		bl = bl.previous
	}
	bl.upval = true
	fs.needclose = true
}

// markToBeClosed marks the current block as holding a to-be-closed variable
// (PUC marktobeclosed).
func (c *compiler) markToBeClosed(fs *FuncState) {
	bl := fs.bl
	bl.upval = true
	bl.insidetbc = true
	fs.needclose = true
}

// singleVarAux resolves name within fs, threading new upvalues through every
// intermediate function; leaves v as VVOID if name is a global (PUC singlevaraux).
func (c *compiler) singleVarAux(fs *FuncState, n string, v *expdesc, base bool) {
	if fs == nil {
		initExp(v, VVOID, 0)
		return
	}
	if k := c.searchVar(fs, n, v); k >= 0 {
		if k == VLOCAL && !base {
			c.markUpval(fs, v.vr.vidx)
		}
		return
	}
	idx := c.searchUpvalue(fs, n)
	if idx < 0 {
		c.singleVarAux(fs.prev, n, v, false)
		if v.k == VLOCAL || v.k == VUPVAL {
			idx = c.newUpvalue(fs, n, v)
		} else {
			return // global or constant: nothing to do at this level
		}
	}
	initExp(v, VUPVAL, idx)
}

// singleVar resolves a name, falling back to an _ENV index for globals
// (PUC singlevar).
func (c *compiler) singleVar(name string, v *expdesc) {
	fs := c.fs
	c.singleVarAux(fs, name, v, true)
	if v.k == VVOID { // global: _ENV[name]
		var key expdesc
		c.singleVarAux(fs, c.envName, v, true)
		fs.luaK_exp2anyregup(v)
		codestring(&key, name)
		fs.luaK_indexed(v, &key)
	}
}

// adjustAssign matches nexps expression results to nvars variables, padding with
// nils or trimming as needed (PUC adjust_assign).
func (c *compiler) adjustAssign(nvars, nexps int, e *expdesc) {
	fs := c.fs
	needed := nvars - nexps
	if hasMultRet(e.k) {
		extra := needed + 1
		if extra < 0 {
			extra = 0
		}
		fs.luaK_setreturns(e, extra)
	} else {
		if e.k != VVOID {
			fs.luaK_exp2nextreg(e)
		}
		if needed > 0 {
			fs.luaK_nil(fs.freereg, needed)
		}
	}
	if needed > 0 {
		fs.reserveRegs(needed)
	} else {
		fs.freereg += needed
	}
}

// --- goto / label management ---

func (c *compiler) newLabelEntry(list *[]labelDesc, name string, line, pc int) int {
	*list = append(*list, labelDesc{name: name, pc: pc, line: line, nactvar: c.fs.nactvar})
	return len(*list) - 1
}

func (c *compiler) newGotoEntry(name string, line, pc int) int {
	return c.newLabelEntry(&c.gt, name, line, pc)
}

// findLabel returns the active label with the given name in the current
// function, or nil (PUC findlabel).
func (c *compiler) findLabel(name string) *labelDesc {
	for i := c.fs.firstlabel; i < len(c.label); i++ {
		if c.label[i].name == name {
			return &c.label[i]
		}
	}
	return nil
}

// solveGoto resolves the pending goto at index g to label and removes it
// (PUC solvegoto).
func (c *compiler) solveGoto(g int, label *labelDesc) {
	gt := &c.gt[g]
	if gt.nactvar < label.nactvar {
		c.semError("<goto %s> at line %d jumps into the scope of local '%s'",
			gt.name, gt.line, c.fs.localVarDesc(gt.nactvar).name)
	}
	c.fs.luaK_patchlist(gt.pc, label.pc)
	c.gt = append(c.gt[:g], c.gt[g+1:]...)
}

// solveGotos resolves all pending gotos in the current block that match lb,
// returning whether any of them needs a CLOSE (PUC solvegotos).
func (c *compiler) solveGotos(lb *labelDesc) bool {
	needsclose := false
	i := c.fs.bl.firstgoto
	for i < len(c.gt) {
		if c.gt[i].name == lb.name {
			needsclose = needsclose || c.gt[i].close
			c.solveGoto(i, lb) // removes entry i
		} else {
			i++
		}
	}
	return needsclose
}

// createLabel creates a label, resolving pending gotos and emitting a CLOSE if
// any escaping goto needs one (PUC createlabel). Returns whether it emitted one.
func (c *compiler) createLabel(name string, line int, last bool) bool {
	fs := c.fs
	l := c.newLabelEntry(&c.label, name, line, fs.luaK_getlabel())
	if last {
		c.label[l].nactvar = fs.bl.nactvar
	}
	if c.solveGotos(&c.label[l]) {
		fs.codeABC(OP_CLOSE, fs.nvarstack(), 0, 0)
		return true
	}
	return false
}

// moveGotosOut re-points pending gotos to the enclosing block as a block exits
// (PUC movegotosout).
func (c *compiler) moveGotosOut(fs *FuncState, bl *blockCnt) {
	for i := bl.firstgoto; i < len(c.gt); i++ {
		gt := &c.gt[i]
		if fs.reglevel(gt.nactvar) > fs.reglevel(bl.nactvar) {
			gt.close = gt.close || bl.upval
		}
		gt.nactvar = bl.nactvar
	}
}

func (c *compiler) undefGoto(gt *labelDesc) {
	if gt.name == "break" {
		c.semError("break outside loop at line %d", gt.line)
	}
	c.semError("no visible label '%s' for <goto> at line %d", gt.name, gt.line)
}

// --- block and function scoping ---

func (c *compiler) enterBlock(fs *FuncState, bl *blockCnt, isloop bool) {
	bl.isloop = isloop
	bl.nactvar = fs.nactvar
	bl.firstlabel = len(c.label)
	bl.firstgoto = len(c.gt)
	bl.upval = false
	bl.insidetbc = fs.bl != nil && fs.bl.insidetbc
	bl.previous = fs.bl
	fs.bl = bl
}

func (c *compiler) leaveBlock(fs *FuncState) {
	bl := fs.bl
	stklevel := fs.reglevel(bl.nactvar)
	c.removeVars(fs, bl.nactvar)
	hasclose := false
	if bl.isloop {
		hasclose = c.createLabel("break", 0, false)
	}
	if !hasclose && bl.previous != nil && bl.upval {
		fs.codeABC(OP_CLOSE, stklevel, 0, 0)
	}
	fs.freereg = stklevel
	c.label = c.label[:bl.firstlabel]
	fs.bl = bl.previous
	if bl.previous != nil {
		c.moveGotosOut(fs, bl)
	} else if bl.firstgoto < len(c.gt) {
		c.undefGoto(&c.gt[bl.firstgoto])
	}
}

// addPrototype appends a new child prototype to the current function (PUC
// addprototype).
func (c *compiler) addPrototype() *Proto {
	// Small initial capacity so a nested function's first few instructions and
	// constants do not each trigger a reallocation.
	p := &Proto{
		Source:    c.source,
		Code:      make([]Instruction, 0, 16),
		LineInfo:  make([]int32, 0, 16),
		Constants: make([]Value, 0, 4),
	}
	c.fs.f.Protos = append(c.fs.f.Protos, p)
	return p
}

// codeClosure emits an OP_CLOSURE in the *parent* function referencing the just
// finished child prototype (PUC codeclosure).
func (c *compiler) codeClosure(e *expdesc) {
	fs := c.fs.prev
	initExp(e, VRELOC, fs.codeABx(OP_CLOSURE, 0, len(fs.f.Protos)-1))
	fs.luaK_exp2nextreg(e)
}

// openFunc starts compiling into proto, pushing a new FuncState (PUC open_func).
func (c *compiler) openFunc(proto *Proto) *FuncState {
	fs := newFuncState(proto)
	fs.prev = c.fs
	fs.ls = c
	fs.kres = func(info int) Value { return c.actvar[info].val }
	fs.firstlocal = c.nactvar
	fs.firstlabel = len(c.label)
	c.fs = fs
	proto.MaxStackSize = 2 // registers 0/1 are always valid
	bl := &blockCnt{}
	c.enterBlock(fs, bl, false)
	return fs
}

// closeFunc finishes the current function: final return, scope cleanup, and the
// peephole pass (PUC close_func).
func (c *compiler) closeFunc() {
	fs := c.fs
	fs.luaK_ret(fs.nvarstack(), 0)
	c.leaveBlock(fs)
	fs.luaK_finish()
	c.fs = fs.prev
}

// setVararg marks the function as vararg and emits VARARGPREP (PUC setvararg).
func (c *compiler) setVararg(fs *FuncState, nparams int) {
	fs.f.IsVararg = true
	fs.codeABC(OP_VARARGPREP, nparams, 0, 0)
}

// codestring initializes e as a string constant (PUC codestring).
func codestring(e *expdesc, s string) {
	e.t, e.f = noJump, noJump
	e.k = VKSTR
	e.strval = s
}
