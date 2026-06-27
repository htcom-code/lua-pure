package luapure

import "fmt"

// Single-pass recursive-descent parser — a port of PUC lparser.c that drives the
// existing lcode machinery (lcode.go) and the variable/upvalue/goto bookkeeping
// in codegen.go directly from the token stream (llex.go), instead of walking an
// AST. This is parser-rewrite phase R2/R3; compileTokens is the new entry point
// (R4 will switch Compile over and retire the AST path).
//
// The parser is a distinct type so its grammar methods coexist with codegen.go's
// AST-walk methods during the transition. Both share the (c *compiler) machinery.

type parser struct {
	c       *compiler
	ls      *lexState
	expFree []*expdesc   // freelist of reusable expdesc temporaries
	lhsFree []*lhsAssign // freelist of reusable assignment-chain nodes
	depth   int          // syntactic nesting depth (PUC L->nCcalls via enterlevel)
}

// enterLevel / leaveLevel bound syntactic nesting so deeply nested source
// (500 parens, assignments, blocks, ...) raises at parse time instead of
// recursing without limit (PUC enterlevel → luaE_incCstack, limit LUAI_MAXCCALLS).
func (p *parser) enterLevel() {
	p.depth++
	if p.depth > MaxCCalls {
		p.ls.lexError("too many C levels in function", 0)
	}
}

func (p *parser) leaveLevel() { p.depth-- }

// getExp returns a cleared expdesc from the freelist (or a fresh one). Callers
// must return it with putExp once its value has been consumed; expression
// parsing is strictly nested, so use is LIFO. This keeps the per-expression
// temporaries off the GC's hot path (they otherwise escape to the heap).
func (p *parser) getExp() *expdesc {
	if n := len(p.expFree); n > 0 {
		e := p.expFree[n-1]
		p.expFree = p.expFree[:n-1]
		*e = expdesc{}
		return e
	}
	return new(expdesc)
}

func (p *parser) putExp(e *expdesc) { p.expFree = append(p.expFree, e) }

// getLhs / putLhs pool the assignment-chain nodes the same way (used LIFO).
func (p *parser) getLhs() *lhsAssign {
	if n := len(p.lhsFree); n > 0 {
		l := p.lhsFree[n-1]
		p.lhsFree = p.lhsFree[:n-1]
		*l = lhsAssign{}
		return l
	}
	return new(lhsAssign)
}

func (p *parser) putLhs(l *lhsAssign) { p.lhsFree = append(p.lhsFree, l) }

func (p *parser) fs() *FuncState { return p.c.fs }

// next consumes the current token. It syncs the current function's lastline to
// the lexer (PUC stamps instructions from ls->lastline), so emitted line info is
// correct without the manual fixups the AST path needed.
func (p *parser) next() {
	p.ls.luaXNext()
	if p.c.fs != nil {
		p.c.fs.lastline = p.ls.lastline
	}
}

func (p *parser) tok() int { return p.ls.t.kind }

// --- error helpers (lparser.c) ---

func (p *parser) errorExpected(tok int) {
	p.ls.syntaxError(token2str(tok) + " expected")
}

func (p *parser) semError(msg string) {
	p.ls.t.kind = 0 // strip "near <token>"
	p.ls.syntaxError(msg)
}

func (p *parser) testnext(c int) bool {
	if p.tok() == c {
		p.next()
		return true
	}
	return false
}

func (p *parser) check(c int) {
	if p.tok() != c {
		p.errorExpected(c)
	}
}

func (p *parser) checknext(c int) {
	p.check(c)
	p.next()
}

func (p *parser) checkMatch(what, who, where int) {
	if !p.testnext(what) {
		if where == p.ls.line {
			p.errorExpected(what)
		} else {
			p.ls.syntaxError(fmt.Sprintf("%s expected (to close %s at line %d)",
				token2str(what), token2str(who), where))
		}
	}
}

func (p *parser) strCheckname() string {
	p.check(tkName)
	s := p.ls.t.str
	p.next()
	return s
}

func (p *parser) codename(e *expdesc) {
	codestring(e, p.strCheckname())
}

func (p *parser) blockFollow(withuntil bool) bool {
	switch p.tok() {
	case tkElse, tkElseif, tkEnd, tkEOS:
		return true
	case tkUntil:
		return withuntil
	default:
		return false
	}
}

// --- expressions ---

func (p *parser) fieldsel(v *expdesc) {
	fs := p.fs()
	var key expdesc
	fs.luaK_exp2anyregup(v)
	p.next() // skip '.' or ':'
	p.codename(&key)
	fs.luaK_indexed(v, &key)
}

func (p *parser) yindex(v *expdesc) {
	p.next() // skip '['
	p.expr(v)
	p.fs().luaK_exp2val(v)
	p.checknext(']')
}

type consControl struct {
	v       expdesc  // last list item read
	t       *expdesc // table descriptor
	nh      int      // total record elements
	na      int      // array elements stored
	tostore int      // array elements pending
}

const lfieldsPerFlush = 50

func (p *parser) recfield(cc *consControl) {
	fs := p.fs()
	reg := fs.freereg
	var key, val expdesc
	if p.tok() == tkName {
		p.codename(&key)
	} else {
		p.yindex(&key)
	}
	cc.nh++
	p.checknext('=')
	tab := *cc.t
	fs.luaK_indexed(&tab, &key)
	p.expr(&val)
	fs.luaK_storevar(&tab, &val)
	fs.freereg = reg // free registers
}

func (p *parser) closelistfield(cc *consControl) {
	fs := p.fs()
	if cc.v.k == VVOID {
		return
	}
	fs.luaK_exp2nextreg(&cc.v)
	cc.v.k = VVOID
	if cc.tostore == lfieldsPerFlush {
		fs.luaK_setlist(cc.t.info, cc.na, cc.tostore)
		cc.na += cc.tostore
		cc.tostore = 0
	}
}

func (p *parser) lastlistfield(cc *consControl) {
	fs := p.fs()
	if cc.tostore == 0 {
		return
	}
	if hasMultRet(cc.v.k) {
		fs.luaK_setmultret(&cc.v)
		fs.luaK_setlist(cc.t.info, cc.na, multRet)
		cc.na-- // do not count the last (unknown count) expression
	} else {
		if cc.v.k != VVOID {
			fs.luaK_exp2nextreg(&cc.v)
		}
		fs.luaK_setlist(cc.t.info, cc.na, cc.tostore)
	}
	cc.na += cc.tostore
}

func (p *parser) listfield(cc *consControl) {
	p.expr(&cc.v)
	cc.tostore++
}

func (p *parser) field(cc *consControl) {
	switch p.tok() {
	case tkName:
		if p.ls.luaXLookahead() != '=' {
			p.listfield(cc)
		} else {
			p.recfield(cc)
		}
	case '[':
		p.recfield(cc)
	default:
		p.listfield(cc)
	}
}

func (p *parser) constructor(t *expdesc) {
	fs := p.fs()
	line := p.ls.line
	pc := fs.codeABC(OP_NEWTABLE, 0, 0, 0)
	fs.luaK_code(0) // space for EXTRAARG
	var cc consControl
	cc.t = t
	initExp(t, VNONRELOC, fs.freereg) // table at stack top
	fs.reserveRegs(1)
	initExp(&cc.v, VVOID, 0)
	p.checknext('{')
	for {
		if p.tok() == '}' {
			break
		}
		p.closelistfield(&cc)
		p.field(&cc)
		if !(p.testnext(',') || p.testnext(';')) {
			break
		}
	}
	p.checkMatch('}', '{', line)
	p.lastlistfield(&cc)
	fs.luaK_settablesize(pc, t.info, cc.na, cc.nh)
}

func (p *parser) parlist() {
	fs := p.fs()
	nparams := 0
	isvararg := false
	if p.tok() != ')' {
		for {
			switch p.tok() {
			case tkName:
				p.c.newLocalVar(p.strCheckname())
				nparams++
			case tkDots:
				p.next()
				isvararg = true
			default:
				p.ls.syntaxError("<name> or '...' expected")
			}
			if isvararg || !p.testnext(',') {
				break
			}
		}
	}
	p.c.adjustLocalVars(nparams)
	fs.f.NumParams = uint8(fs.nactvar)
	if isvararg {
		p.c.setVararg(fs, int(fs.f.NumParams))
	}
	fs.reserveRegs(fs.nactvar)
}

func (p *parser) body(e *expdesc, ismethod bool, line int) {
	proto := p.c.addPrototype()
	proto.LineDefined = line
	newfs := p.c.openFunc(proto)
	_ = newfs
	p.checknext('(')
	if ismethod {
		p.c.newLocalVar("self")
		p.c.adjustLocalVars(1)
	}
	p.parlist()
	p.checknext(')')
	p.statlist()
	proto.LastLineDef = p.ls.line
	p.checkMatch(tkEnd, tkFunction, line)
	// CLOSURE (in the parent) and the child's final return sit on the 'end' line.
	p.c.fs.prev.lastline = p.ls.lastline
	p.c.codeClosure(e)
	p.c.fs.lastline = p.ls.lastline
	p.c.closeFunc()
}

func (p *parser) explist(v *expdesc) int {
	n := 1
	p.expr(v)
	for p.testnext(',') {
		p.fs().luaK_exp2nextreg(v)
		p.expr(v)
		n++
	}
	return n
}

func (p *parser) funcargs(f *expdesc) {
	fs := p.fs()
	var args expdesc
	line := p.ls.line
	switch p.tok() {
	case '(':
		p.next()
		if p.tok() == ')' {
			args.k = VVOID
		} else {
			p.explist(&args)
			if hasMultRet(args.k) {
				fs.luaK_setmultret(&args)
			}
		}
		p.checkMatch(')', '(', line)
	case '{':
		p.constructor(&args)
	case tkString:
		codestring(&args, p.ls.t.str)
		p.next()
	default:
		p.ls.syntaxError("function arguments expected")
	}
	base := f.info // f.k == VNONRELOC
	var nparams int
	if hasMultRet(args.k) {
		nparams = multRet
	} else {
		if args.k != VVOID {
			fs.luaK_exp2nextreg(&args)
		}
		nparams = fs.freereg - (base + 1)
	}
	initExp(f, VCALL, fs.codeABC(OP_CALL, base, nparams+1, 2))
	fs.luaK_fixline(line)
	fs.freereg = base + 1 // call leaves one result
}

func (p *parser) primaryexp(v *expdesc) {
	switch p.tok() {
	case '(':
		line := p.ls.line
		p.next()
		p.expr(v)
		p.checkMatch(')', '(', line)
		p.fs().luaK_dischargevars(v)
	case tkName:
		p.c.singleVar(p.strCheckname(), v)
	default:
		p.ls.syntaxError("unexpected symbol")
	}
}

func (p *parser) suffixedexp(v *expdesc) {
	fs := p.fs()
	p.primaryexp(v)
	for {
		switch p.tok() {
		case '.':
			p.fieldsel(v)
		case '[':
			var key expdesc
			fs.luaK_exp2anyregup(v)
			p.yindex(&key)
			fs.luaK_indexed(v, &key)
		case ':':
			var key expdesc
			p.next()
			p.codename(&key)
			fs.luaK_self(v, &key)
			p.funcargs(v)
		case '(', tkString, '{':
			fs.luaK_exp2nextreg(v)
			p.funcargs(v)
		default:
			return
		}
	}
}

func (p *parser) simpleexp(v *expdesc) {
	switch p.tok() {
	case tkFlt:
		initExp(v, VKFLT, 0)
		v.nval = p.ls.t.num.AsFloat()
	case tkInt:
		initExp(v, VKINT, 0)
		v.ival = p.ls.t.num.AsInt()
	case tkString:
		codestring(v, p.ls.t.str)
	case tkNil:
		initExp(v, VNIL, 0)
	case tkTrue:
		initExp(v, VTRUE, 0)
	case tkFalse:
		initExp(v, VFALSE, 0)
	case tkDots:
		fs := p.fs()
		if !fs.f.IsVararg {
			p.ls.syntaxError("cannot use '...' outside a vararg function")
		}
		initExp(v, VVARARG, fs.codeABC(OP_VARARG, 0, 0, 1))
	case '{':
		p.constructor(v)
		return
	case tkFunction:
		p.next()
		p.body(v, false, p.ls.line)
		return
	default:
		p.suffixedexp(v)
		return
	}
	p.next()
}

func getunopr(op int) UnOpr {
	switch op {
	case tkNot:
		return OPR_NOT
	case '-':
		return OPR_MINUS
	case '~':
		return OPR_BNOT
	case '#':
		return OPR_LEN
	default:
		return OPR_NOUNOPR
	}
}

func getbinopr(op int) BinOpr {
	switch op {
	case '+':
		return OPR_ADD
	case '-':
		return OPR_SUB
	case '*':
		return OPR_MUL
	case '%':
		return OPR_MOD
	case '^':
		return OPR_POW
	case '/':
		return OPR_DIV
	case tkIDiv:
		return OPR_IDIV
	case '&':
		return OPR_BAND
	case '|':
		return OPR_BOR
	case '~':
		return OPR_BXOR
	case tkShl:
		return OPR_SHL
	case tkShr:
		return OPR_SHR
	case tkConcat:
		return OPR_CONCAT
	case tkNe:
		return OPR_NE
	case tkEq:
		return OPR_EQ
	case '<':
		return OPR_LT
	case tkLe:
		return OPR_LE
	case '>':
		return OPR_GT
	case tkGe:
		return OPR_GE
	case tkAnd:
		return OPR_AND
	case tkOr:
		return OPR_OR
	default:
		return OPR_NOBINOPR
	}
}

// priority is the binary-operator precedence table (ORDER OPR, == PUC priority[]).
var priority = [...]struct{ left, right int }{
	OPR_ADD: {10, 10}, OPR_SUB: {10, 10},
	OPR_MUL: {11, 11}, OPR_MOD: {11, 11},
	OPR_POW: {14, 13},
	OPR_DIV: {11, 11}, OPR_IDIV: {11, 11},
	OPR_BAND: {6, 6}, OPR_BOR: {4, 4}, OPR_BXOR: {5, 5},
	OPR_SHL: {7, 7}, OPR_SHR: {7, 7},
	OPR_CONCAT: {9, 8},
	OPR_EQ: {3, 3}, OPR_LT: {3, 3}, OPR_LE: {3, 3},
	OPR_NE: {3, 3}, OPR_GT: {3, 3}, OPR_GE: {3, 3},
	OPR_AND: {2, 2}, OPR_OR: {1, 1},
}

const unaryPriority = 12

// subexpr parses an expression whose operators have priority above limit.
func (p *parser) subexpr(v *expdesc, limit int) BinOpr {
	p.enterLevel()
	fs := p.fs()
	if uop := getunopr(p.tok()); uop != OPR_NOUNOPR {
		line := p.ls.line
		p.next()
		p.subexpr(v, unaryPriority)
		fs.luaK_prefix(uop, v, line)
	} else {
		p.simpleexp(v)
	}
	op := getbinopr(p.tok())
	for op != OPR_NOBINOPR && priority[op].left > limit {
		v2 := p.getExp()
		line := p.ls.line
		p.next()
		fs.luaK_infix(op, v)
		nextop := p.subexpr(v2, priority[op].right)
		fs.luaK_posfix(op, v, v2, line)
		p.putExp(v2)
		op = nextop
	}
	p.leaveLevel()
	return op
}

func (p *parser) expr(v *expdesc) {
	p.subexpr(v, 0)
}

// --- statements ---

func (p *parser) block() {
	fs := p.fs()
	var bl blockCnt
	p.c.enterBlock(fs, &bl, false)
	p.statlist()
	p.c.leaveBlock(fs)
}

type lhsAssign struct {
	prev *lhsAssign
	v    expdesc
}

// checkConflictTok mirrors check_conflict for the token parser, using the same
// register-rescue logic.
func (p *parser) checkConflict(lh *lhsAssign, v *expdesc) {
	fs := p.fs()
	extra := fs.freereg
	conflict := false
	for ; lh != nil; lh = lh.prev {
		if vkisindexed(lh.v.k) {
			if lh.v.k == VINDEXUP {
				if v.k == VUPVAL && lh.v.ind.t == v.info {
					conflict = true
					lh.v.k = VINDEXSTR
					lh.v.ind.t = extra
				}
			} else {
				if v.k == VLOCAL && lh.v.ind.t == v.vr.ridx {
					conflict = true
					lh.v.ind.t = extra
				}
				if lh.v.k == VINDEXED && v.k == VLOCAL && lh.v.ind.idx == v.vr.ridx {
					conflict = true
					lh.v.ind.idx = extra
				}
			}
		}
	}
	if conflict {
		if v.k == VLOCAL {
			fs.codeABC(OP_MOVE, extra, v.vr.ridx, 0)
		} else {
			fs.codeABC(OP_GETUPVAL, extra, v.info, 0)
		}
		fs.reserveRegs(1)
	}
}

func (p *parser) restassign(lh *lhsAssign, nvars int) {
	fs := p.fs()
	if !vkisvar(lh.v.k) {
		p.ls.syntaxError("syntax error")
	}
	p.c.checkReadonly(&lh.v)
	if p.testnext(',') {
		nv := p.getLhs()
		nv.prev = lh
		p.suffixedexp(&nv.v)
		if !vkisindexed(nv.v.k) {
			p.checkConflict(lh, &nv.v)
		}
		p.enterLevel() // control recursion depth (PUC restassign)
		p.restassign(nv, nvars+1)
		p.leaveLevel()
		p.putLhs(nv)
	} else {
		e := p.getExp()
		p.checknext('=')
		nexps := p.explist(e)
		if nexps != nvars {
			p.c.adjustAssign(nvars, nexps, e)
		} else {
			fs.luaK_setoneret(e)
			fs.luaK_storevar(&lh.v, e)
			p.putExp(e)
			return
		}
		p.putExp(e)
	}
	// default assignment (also reached after the comma branch's recursion)
	e := p.getExp()
	initExp(e, VNONRELOC, fs.freereg-1)
	fs.luaK_storevar(&lh.v, e)
	p.putExp(e)
}

func (p *parser) cond() int {
	var v expdesc
	p.expr(&v)
	if v.k == VNIL {
		v.k = VFALSE
	}
	p.fs().luaK_goiftrue(&v)
	return v.f
}

func (p *parser) gotostat() {
	fs := p.fs()
	line := p.ls.line
	name := p.strCheckname()
	if lb := p.c.findLabel(name); lb == nil {
		p.c.newGotoEntry(name, line, fs.luaK_jump())
	} else {
		lblevel := fs.reglevel(lb.nactvar)
		if fs.nvarstack() > lblevel {
			fs.codeABC(OP_CLOSE, lblevel, 0, 0)
		}
		fs.luaK_patchlist(fs.luaK_jump(), lb.pc)
	}
}

func (p *parser) breakstat() {
	line := p.ls.line
	p.next() // skip break
	p.c.newGotoEntry("break", line, p.fs().luaK_jump())
}

func (p *parser) checkrepeated(name string) {
	if lb := p.c.findLabel(name); lb != nil {
		p.semError(fmt.Sprintf("label '%s' already defined on line %d", name, lb.line))
	}
}

func (p *parser) labelstat(name string, line int) {
	p.checknext(tkDbColon)
	for p.tok() == ';' || p.tok() == tkDbColon {
		p.statement()
	}
	p.checkrepeated(name)
	p.c.createLabel(name, line, p.blockFollow(false))
}

func (p *parser) whilestat(line int) {
	fs := p.fs()
	p.next() // skip WHILE
	whileinit := fs.luaK_getlabel()
	condexit := p.cond()
	var bl blockCnt
	p.c.enterBlock(fs, &bl, true)
	p.checknext(tkDo)
	p.block()
	fs.luaK_patchlist(fs.luaK_jump(), whileinit)
	p.checkMatch(tkEnd, tkWhile, line)
	p.c.leaveBlock(fs)
	fs.luaK_patchtohere(condexit)
}

func (p *parser) repeatstat(line int) {
	fs := p.fs()
	repeatInit := fs.luaK_getlabel()
	var bl1, bl2 blockCnt
	p.c.enterBlock(fs, &bl1, true) // loop block
	p.c.enterBlock(fs, &bl2, false)
	p.next() // skip REPEAT
	p.statlist()
	p.checkMatch(tkUntil, tkRepeat, line)
	condexit := p.cond()
	p.c.leaveBlock(fs) // finish scope
	if bl2.upval {
		exit := fs.luaK_jump()
		fs.luaK_patchtohere(condexit)
		fs.codeABC(OP_CLOSE, fs.reglevel(bl2.nactvar), 0, 0)
		condexit = fs.luaK_jump()
		fs.luaK_patchtohere(exit)
	}
	fs.luaK_patchlist(condexit, repeatInit)
	p.c.leaveBlock(fs) // finish loop
}

func (p *parser) exp1() {
	var e expdesc
	p.expr(&e)
	p.fs().luaK_exp2nextreg(&e)
}

func (p *parser) forbody(base, line, nvars int, isgen bool) {
	fs := p.fs()
	prepOp, loopOp := OP_FORPREP, OP_FORLOOP
	if isgen {
		prepOp, loopOp = OP_TFORPREP, OP_TFORLOOP
	}
	p.checknext(tkDo)
	prep := fs.codeABx(prepOp, base, 0)
	var bl blockCnt
	p.c.enterBlock(fs, &bl, false)
	p.c.adjustLocalVars(nvars)
	fs.reserveRegs(nvars)
	p.block()
	p.c.leaveBlock(fs)
	fs.fixForJump(prep, fs.luaK_getlabel(), false)
	if isgen {
		fs.codeABC(OP_TFORCALL, base, 0, nvars)
		fs.luaK_fixline(line)
	}
	endfor := fs.codeABx(loopOp, base, 0)
	fs.fixForJump(endfor, prep+1, true)
	fs.luaK_fixline(line)
}

// fixForJump patches a FORPREP/FORLOOP-style jump offset (PUC fixforjump).
func (fs *FuncState) fixForJump(pc, dest int, back bool) {
	offset := dest - (pc + 1)
	if back {
		offset = -offset
	}
	if offset > MaxArgBx {
		fs.syntaxError("control structure too long")
	}
	SetArgBx(&fs.f.Code[pc], offset)
}

func (p *parser) fornum(varname string, line int) {
	fs := p.fs()
	base := fs.freereg
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar(varname)
	p.checknext('=')
	p.exp1() // initial value
	p.checknext(',')
	p.exp1() // limit
	if p.testnext(',') {
		p.exp1() // optional step
	} else {
		fs.luaK_int(fs.freereg, 1)
		fs.reserveRegs(1)
	}
	p.c.adjustLocalVars(3)
	p.forbody(base, line, 1, false)
}

func (p *parser) forlist(indexname string) {
	fs := p.fs()
	var e expdesc
	nvars := 5
	base := fs.freereg
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar("(for state)")
	p.c.newLocalVar(indexname)
	for p.testnext(',') {
		p.c.newLocalVar(p.strCheckname())
		nvars++
	}
	p.checknext(tkIn)
	line := p.ls.line
	p.c.adjustAssign(4, p.explist(&e), &e)
	p.c.adjustLocalVars(4)
	p.c.markToBeClosed(fs)
	fs.checkStack(3)
	p.forbody(base, line, nvars-4, true)
}

func (p *parser) forstat(line int) {
	fs := p.fs()
	var bl blockCnt
	p.c.enterBlock(fs, &bl, true)
	p.next() // skip 'for'
	varname := p.strCheckname()
	switch p.tok() {
	case '=':
		p.fornum(varname, line)
	case ',', tkIn:
		p.forlist(varname)
	default:
		p.ls.syntaxError("'=' or 'in' expected")
	}
	p.checkMatch(tkEnd, tkFor, line)
	p.c.leaveBlock(fs)
}

func (p *parser) testThenBlock(escape *int) {
	fs := p.fs()
	var v expdesc
	var bl blockCnt
	p.next() // skip IF or ELSEIF
	p.expr(&v)
	p.checknext(tkThen)
	var jf int
	if p.tok() == tkBreak {
		line := p.ls.line
		fs.luaK_goiffalse(&v)
		p.next() // skip break
		p.c.enterBlock(fs, &bl, false)
		p.c.newGotoEntry("break", line, v.t)
		for p.testnext(';') {
		}
		if p.blockFollow(false) {
			p.c.leaveBlock(fs)
			return
		}
		jf = fs.luaK_jump()
	} else {
		fs.luaK_goiftrue(&v)
		p.c.enterBlock(fs, &bl, false)
		jf = v.f
	}
	p.statlist()
	p.c.leaveBlock(fs)
	if p.tok() == tkElse || p.tok() == tkElseif {
		fs.luaK_concat(escape, fs.luaK_jump())
	}
	fs.luaK_patchtohere(jf)
}

func (p *parser) ifstat(line int) {
	fs := p.fs()
	escape := noJump
	p.testThenBlock(&escape)
	for p.tok() == tkElseif {
		p.testThenBlock(&escape)
	}
	if p.testnext(tkElse) {
		p.block()
	}
	p.checkMatch(tkEnd, tkIf, line)
	fs.luaK_patchtohere(escape)
}

func (p *parser) localfunc() {
	fs := p.fs()
	fvar := fs.nactvar
	p.c.newLocalVar(p.strCheckname())
	p.c.adjustLocalVars(1)
	var b expdesc
	p.body(&b, false, p.ls.line)
	// the local is only visible after the body (debug startpc)
	fs.f.LocVars[fs.localVarDesc(fvar).pidx].StartPc = fs.pc
}

func (p *parser) getlocalattribute() uint8 {
	if p.testnext('<') {
		attr := p.strCheckname()
		p.checknext('>')
		switch attr {
		case "const":
			return VarKindConst
		case "close":
			return VarKindToClose
		default:
			p.semError(fmt.Sprintf("unknown attribute '%s'", attr))
		}
	}
	return VarKindReg
}

func (p *parser) checktoclose(level int) {
	if level != -1 {
		fs := p.fs()
		p.c.markToBeClosed(fs)
		fs.codeABC(OP_TBC, fs.reglevel(level), 0, 0)
	}
}

func (p *parser) localstat() {
	fs := p.fs()
	toclose := -1
	var lastVidx int
	nvars := 0
	for {
		vidx := p.c.newLocalVar(p.strCheckname())
		kind := p.getlocalattribute()
		fs.localVarDesc(vidx).kind = kind
		if kind == VarKindToClose {
			if toclose != -1 {
				p.semError("multiple to-be-closed variables in local list")
			}
			toclose = fs.nactvar + nvars
		}
		lastVidx = vidx
		nvars++
		if !p.testnext(',') {
			break
		}
	}
	var e expdesc
	var nexps int
	if p.testnext('=') {
		nexps = p.explist(&e)
	} else {
		e.k = VVOID
		nexps = 0
	}
	lastVar := fs.localVarDesc(lastVidx)
	if val, ok := fs.luaK_exp2const(&e); nvars == nexps && lastVar.kind == VarKindConst && ok {
		lastVar.kind = VarKindCTConst
		lastVar.val = val
		p.c.adjustLocalVars(nvars - 1)
		fs.nactvar++ // count it
	} else {
		p.c.adjustAssign(nvars, nexps, &e)
		p.c.adjustLocalVars(nvars)
	}
	p.checktoclose(toclose)
}

func (p *parser) funcname(v *expdesc) bool {
	ismethod := false
	p.c.singleVar(p.strCheckname(), v)
	for p.tok() == '.' {
		p.fieldsel(v)
	}
	if p.tok() == ':' {
		ismethod = true
		p.fieldsel(v)
	}
	return ismethod
}

func (p *parser) funcstat(line int) {
	p.next() // skip FUNCTION
	var v, b expdesc
	ismethod := p.funcname(&v)
	p.body(&b, ismethod, line)
	p.c.checkReadonly(&v)
	p.fs().luaK_storevar(&v, &b)
	p.fs().luaK_fixline(line)
}

func (p *parser) exprstat() {
	fs := p.fs()
	v := p.getLhs()
	p.suffixedexp(&v.v)
	if p.tok() == '=' || p.tok() == ',' {
		v.prev = nil
		p.restassign(v, 1)
	} else {
		if v.v.k != VCALL {
			p.ls.syntaxError("syntax error")
		}
		SetArgC(&fs.f.Code[v.v.info], 1) // call statement, no results
	}
	p.putLhs(v)
}

func (p *parser) retstat() {
	fs := p.fs()
	var e expdesc
	first := fs.nvarstack()
	var nret int
	if p.blockFollow(true) || p.tok() == ';' {
		nret = 0
	} else {
		nret = p.explist(&e)
		if hasMultRet(e.k) {
			fs.luaK_setmultret(&e)
			if e.k == VCALL && nret == 1 && !fs.bl.insidetbc {
				SetOpCode(&fs.f.Code[e.info], OP_TAILCALL)
			}
			nret = multRet
		} else {
			if nret == 1 {
				first = fs.luaK_exp2anyreg(&e)
			} else {
				fs.luaK_exp2nextreg(&e)
			}
		}
	}
	fs.luaK_ret(first, nret)
	p.testnext(';')
}

func (p *parser) statement() {
	p.enterLevel()
	defer p.leaveLevel()
	line := p.ls.line
	switch p.tok() {
	case ';':
		p.next()
	case tkIf:
		p.ifstat(line)
	case tkWhile:
		p.whilestat(line)
	case tkDo:
		p.next()
		p.block()
		p.checkMatch(tkEnd, tkDo, line)
	case tkFor:
		p.forstat(line)
	case tkRepeat:
		p.repeatstat(line)
	case tkFunction:
		p.funcstat(line)
	case tkLocal:
		p.next()
		if p.testnext(tkFunction) {
			p.localfunc()
		} else {
			p.localstat()
		}
	case tkDbColon:
		p.next()
		p.labelstat(p.strCheckname(), line)
	case tkReturn:
		p.next()
		p.retstat()
	case tkBreak:
		p.breakstat()
	case tkGoto:
		p.next()
		p.gotostat()
	default:
		p.exprstat()
	}
	fs := p.fs()
	fs.freereg = fs.nvarstack()
}

func (p *parser) statlist() {
	for !p.blockFollow(true) {
		if p.tok() == tkReturn {
			p.statement()
			return
		}
		p.statement()
	}
}

func (p *parser) mainfunc(proto *Proto) {
	p.c.openFunc(proto)
	// PUC emits the main chunk's VARARGPREP before reading the first token, when
	// ls->lastline is still its initial value (1). Sync lastline from the lexer
	// so that instruction is stamped with line 1, matching luac exactly.
	p.fs().lastline = p.ls.lastline
	p.c.setVararg(p.fs(), 0)
	env := p.c.allocUpvalue(p.fs())
	p.fs().f.Upvalues[env] = UpvalDesc{Name: p.c.envName, InStack: true, Index: 0, Kind: VarKindReg}
	p.next() // read first token
	p.statlist()
	p.check(tkEOS)
	p.c.closeFunc()
}

// compileTokens compiles a complete source string (the common path / tests).
func compileTokens(src, name string) (*Proto, error) {
	return compileZIO(newStringZIO(src), name, len(src))
}

// compileZIO is the single front-end entry: it lexes/parses straight from a ZIO
// (PUC lua_load over a Zio), so every chunk source — a string, a file, or a
// load() reader — shares one path and is never buffered whole. sizeHint
// pre-sizes the main chunk's vectors. A reader that raises mid-parse is caught
// and returned as the load failure (a *luaError, whose value the caller
// forwards); a compile-limit/syntax error returns a *CompileError.
func compileZIO(z *ZIO, name string, sizeHint int) (proto *Proto, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *CompileError:
				err = e
			case *luaError: // a reader function raised
				err = e
			default:
				panic(r)
			}
		}
	}()
	c := &compiler{source: name, envName: "_ENV"}
	main := &Proto{Source: name}
	// Preallocate the main chunk's growing vectors from a source-size estimate
	// so the bytecode/lineinfo/constant/locvar/proto slices avoid repeated
	// reallocation (most code of a large flat chunk lives here, so its LocVars
	// and child Protos also grow from nil through many doublings). Rough
	// bytes-per-item ratios; over-estimating only wastes a little headroom,
	// while under-estimating still beats starting from nil.
	main.Code = make([]Instruction, 0, sizeHint/12+8)
	main.LineInfo = make([]int32, 0, sizeHint/12+8)
	main.Constants = make([]Value, 0, sizeHint/60+4)
	main.LocVars = make([]LocVar, 0, sizeHint/110+4)
	main.Protos = make([]*Proto, 0, sizeHint/400+2)
	p := &parser{c: c, ls: newLexStateZIO(z, name)}
	p.mainfunc(main)
	return main, nil
}
