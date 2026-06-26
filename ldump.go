package luapure

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// Binary chunk (de)serialization for string.dump / load(binary chunk).
//
// This is a faithful Go port of PUC-Lua 5.4's ldump.c / lundump.c: the wire
// format is byte-identical to what the reference luac 5.4.8 produces on a
// little-endian 64-bit platform (sizeof Instruction=4, lua_Integer=8,
// lua_Number=8). A chunk dumped here loads in reference Lua, and a chunk
// produced by luac 5.4.8 loads here. The whole format — varint sizes, the
// size+1 string convention, PUC constant type tags, and the compressed
// lineinfo/abslineinfo debug layout — mirrors the C sources field for field.
//
// luapure's internal Proto keeps absolute source lines (LineInfo, 1:1 with
// Code). PUC instead stores signed-byte deltas (lineinfo) plus periodic
// absolute reference points (abslineinfo). dump re-runs PUC's savelineinfo
// compression over our absolute lines, and undump decompresses back to
// absolute lines; because the codegen emits PUC-identical instructions, the
// recompressed bytes match luac's exactly. Upvalue *values* are not
// serialized (PUC does not either); a reloaded function binds its upvalues
// afresh, with _ENV rebound by the loader like any loaded chunk.
//
// SECURITY: undump reconstructs a Proto from untrusted bytes. It validates
// aggressively — bounded slice lengths, bounded recursion depth, opcode
// validity, and in-range nested-closure indices — and never panics on
// malformed input, returning an error instead. Fully sandboxing hostile
// bytecode is not a goal; embedders that must not load binary chunks should
// reject them first.
const (
	luaSignature = "\x1bLua"            // LUA_SIGNATURE
	luacVersion  = 0x54                 // LUAC_VERSION (5.4)
	luacFormat   = 0                    // LUAC_FORMAT (official format)
	luacData     = "\x19\x93\r\n\x1a\n" // LUAC_DATA (catches transfer corruption)
	luacInt      = 0x5678               // LUAC_INT (endianness/size check)
	luacNum      = 370.5                // LUAC_NUM (float format check)
	dumpInstSize = 4                    // sizeof(Instruction)
	dumpIntSize  = 8                    // sizeof(lua_Integer)
	dumpNumSize  = 8                    // sizeof(lua_Number)

	dumpMaxItems = 1 << 26 // ceiling on any length-prefixed slice/string
	dumpMaxDepth = 200     // ceiling on nested-prototype recursion

	// Line-info compression (PUC lcode.c / ldebug.h).
	limLineDiff = 0x80  // LIMLINEDIFF: relative delta must fit a signed byte
	maxIwthAbs  = 128   // MAXIWTHABS: instructions between absolute references
	absLineInfo = -0x80 // ABSLINEINFO: marker that an absolute entry applies
)

// Constant type tags — PUC ttypetag values (makevariant), so a dumped
// constant table is byte-identical to luac's.
const (
	tagVNil    = 0x00 // LUA_VNIL
	tagVFalse  = 0x01 // LUA_VFALSE
	tagVTrue   = 0x11 // LUA_VTRUE
	tagVNumInt = 0x03 // LUA_VNUMINT
	tagVNumFlt = 0x13 // LUA_VNUMFLT
	tagVShrStr = 0x04 // LUA_VSHRSTR
	tagVLngStr = 0x14 // LUA_VLNGSTR
)

// maxShortLen (LUAI_MAXSHORTLEN, the short/long string boundary) is defined in
// lobject.go and reused here to choose the LUA_VSHRSTR vs LUA_VLNGSTR tag.

var (
	errBadBinaryChunk = errors.New("malformed binary chunk")
	errTruncated      = errors.New("truncated binary chunk")
)

// --- dump ----------------------------------------------------------------------

// dumpWriter serializes with a sticky error: once a Write fails every later
// operation is a no-op and the error is reported by dumpProto.
type dumpWriter struct {
	w   io.Writer
	buf [8]byte
	err error
}

func (d *dumpWriter) raw(s string) {
	if d.err != nil {
		return
	}
	_, d.err = io.WriteString(d.w, s)
}

func (d *dumpWriter) u8(b byte) {
	if d.err != nil {
		return
	}
	d.buf[0] = b
	_, d.err = d.w.Write(d.buf[:1])
}

func (d *dumpWriter) u32(v uint32) {
	if d.err != nil {
		return
	}
	binary.LittleEndian.PutUint32(d.buf[:4], v)
	_, d.err = d.w.Write(d.buf[:4])
}

func (d *dumpWriter) u64(v uint64) {
	if d.err != nil {
		return
	}
	binary.LittleEndian.PutUint64(d.buf[:8], v)
	_, d.err = d.w.Write(d.buf[:8])
}

// size writes a value as PUC dumpSize does: 7-bit groups, most significant
// first, with the high bit set on the final (least significant) byte.
func (d *dumpWriter) size(x uint64) {
	if d.err != nil {
		return
	}
	var b [10]byte // ceil(64/7) = 10 groups is the worst case
	n := 0
	for {
		n++
		b[len(b)-n] = byte(x) & 0x7f
		x >>= 7
		if x == 0 {
			break
		}
	}
	b[len(b)-1] |= 0x80 // mark last byte
	_, d.err = d.w.Write(b[len(b)-n:])
}

// vInt mirrors PUC dumpInt: a (non-negative) int written through dumpSize.
func (d *dumpWriter) vInt(v int) { d.size(uint64(v)) }

// str writes a present string with the PUC size+1 convention.
func (d *dumpWriter) str(s string) {
	d.size(uint64(len(s)) + 1)
	d.raw(s)
}

// nullStr writes PUC's NULL string (size 0): on load it means "inherit the
// parent prototype's source".
func (d *dumpWriter) nullStr() { d.size(0) }

func (d *dumpWriter) constant(k Value) {
	switch {
	case k.IsNil():
		d.u8(tagVNil)
	case k.IsBool():
		if k.AsBool() {
			d.u8(tagVTrue)
		} else {
			d.u8(tagVFalse)
		}
	case k.IsInt():
		d.u8(tagVNumInt)
		d.u64(uint64(k.AsInt())) // dumpInteger: 8-byte little-endian
	case k.IsFloat():
		d.u8(tagVNumFlt)
		d.u64(math.Float64bits(k.AsFloat())) // dumpNumber: 8-byte LE double
	case k.IsString():
		s := k.Str()
		if len(s) <= maxShortLen {
			d.u8(tagVShrStr)
		} else {
			d.u8(tagVLngStr)
		}
		d.str(s)
	default:
		d.err = errBadBinaryChunk
	}
}

// compressLines reconstructs PUC's lineinfo (signed-byte deltas) and
// abslineinfo (absolute reference points) from luapure's absolute LineInfo, by
// replaying lcode.c's savelineinfo over the instruction sequence. Because the
// state it threads (previousline, iwthabs) is fully determined by the line
// sequence and codegen emits luac-identical instructions, the bytes produced
// here match luac's exactly.
type absEntry struct{ pc, line int }

func compressLines(lines []int32, lineDefined int) (lineinfo []byte, abs []absEntry) {
	lineinfo = make([]byte, len(lines))
	previousline := lineDefined
	iwthabs := 0
	for pc, l := range lines {
		line := int(l)
		linedif := line - previousline
		takeAbs := false
		if linedif >= limLineDiff || linedif <= -limLineDiff {
			takeAbs = true // delta does not fit a signed byte
		} else {
			if iwthabs >= maxIwthAbs {
				takeAbs = true // too many instructions since last absolute ref
			}
			iwthabs++ // PUC's iwthabs++ runs whenever the delta fits
		}
		if takeAbs {
			abs = append(abs, absEntry{pc: pc, line: line})
			linedif = absLineInfo
			iwthabs = 1
		}
		lineinfo[pc] = byte(int8(linedif))
		previousline = line
	}
	return lineinfo, abs
}

func (d *dumpWriter) debug(p *Proto, strip bool) {
	if strip {
		d.vInt(0) // lineinfo
		d.vInt(0) // abslineinfo
		d.vInt(0) // locvars
		d.vInt(0) // upvalue names
		return
	}

	lineinfo, abs := compressLines(p.LineInfo, p.LineDefined)
	d.vInt(len(lineinfo))
	for _, b := range lineinfo {
		d.u8(b)
	}

	d.vInt(len(abs))
	for _, a := range abs {
		d.vInt(a.pc)
		d.vInt(a.line)
	}

	d.vInt(len(p.LocVars))
	for _, l := range p.LocVars {
		d.str(l.Name)
		d.vInt(l.StartPc)
		d.vInt(l.EndPc)
	}

	d.vInt(len(p.Upvalues))
	for _, uv := range p.Upvalues {
		d.str(uv.Name)
	}
}

func (d *dumpWriter) function(p *Proto, strip bool, parentSource string) {
	// Source dedup (PUC dumpFunction): a nested proto that shares its parent's
	// source, or any proto when stripping, writes NULL and inherits the source
	// on load, so the source text is stored once for the whole chunk.
	if strip || p.Source == parentSource {
		d.nullStr()
	} else {
		d.str(p.Source)
	}
	d.vInt(p.LineDefined)
	d.vInt(p.LastLineDef)
	d.u8(p.NumParams)
	if p.IsVararg {
		d.u8(1)
	} else {
		d.u8(0)
	}
	d.u8(p.MaxStackSize)

	// Code.
	d.vInt(len(p.Code))
	for _, c := range p.Code {
		d.u32(uint32(c))
	}

	// Constants.
	d.vInt(len(p.Constants))
	for _, k := range p.Constants {
		d.constant(k)
	}

	// Upvalue descriptors (instack/idx/kind only; names live in debug info).
	d.vInt(len(p.Upvalues))
	for _, uv := range p.Upvalues {
		if uv.InStack {
			d.u8(1)
		} else {
			d.u8(0)
		}
		d.u8(uv.Index)
		d.u8(uv.Kind)
	}

	// Nested prototypes.
	d.vInt(len(p.Protos))
	for _, np := range p.Protos {
		d.function(np, strip, p.Source)
	}

	d.debug(p, strip)
}

// dumpProto serializes a function prototype into a PUC 5.4 precompiled chunk.
// When strip is true, debug info (source, line positions, local and upvalue
// names) is omitted, yielding a smaller blob that reports no line info.
func dumpProto(w io.Writer, p *Proto, strip bool) error {
	d := &dumpWriter{w: w}
	d.raw(luaSignature)
	d.u8(luacVersion)
	d.u8(luacFormat)
	d.raw(luacData)
	d.u8(dumpInstSize)
	d.u8(dumpIntSize)
	d.u8(dumpNumSize)
	d.u64(uint64(luacInt))           // LUAC_INT, 8-byte little-endian
	d.u64(math.Float64bits(luacNum)) // LUAC_NUM, 8-byte little-endian double
	d.u8(byte(len(p.Upvalues)))      // closure's nupvalues (PUC luaU_dump)
	d.function(p, strip, "")         // top level has no parent source to inherit
	return d.err
}

// isBinaryChunk reports whether b looks like a binary chunk. Like PUC f_parser,
// only the first byte (the escape) is examined, so a chunk truncated mid-header
// is still routed to undump and reported as "truncated" rather than parsed.
func isBinaryChunk(b string) bool {
	return len(b) >= 1 && b[0] == luaSignature[0]
}

// --- undump --------------------------------------------------------------------

// undumpReader deserializes with a sticky error: a short read, an out-of-bounds
// length, or excessive nesting sets err, after which every read is a no-op and
// undump returns the error rather than panicking.
type undumpReader struct {
	r     io.Reader
	buf   [8]byte
	err   error
	depth int
}

func (u *undumpReader) read(n int) []byte {
	if u.err != nil {
		return u.buf[:n]
	}
	if _, err := io.ReadFull(u.r, u.buf[:n]); err != nil {
		u.err = errTruncated // ran out of bytes (PUC reports "truncated")
	}
	return u.buf[:n]
}

// fail marks the chunk malformed, but never overwrites a truncation error (a
// short read should keep reporting "truncated").
func (u *undumpReader) fail() {
	if u.err == nil {
		u.err = errBadBinaryChunk
	}
}

func (u *undumpReader) u8() byte    { return u.read(1)[0] }
func (u *undumpReader) u32() uint32 { return binary.LittleEndian.Uint32(u.read(4)) }
func (u *undumpReader) u64() uint64 { return binary.LittleEndian.Uint64(u.read(8)) }

// size reads a PUC varint (loadUnsigned), rejecting overflow.
func (u *undumpReader) size() uint64 {
	const limit = ^uint64(0) >> 7
	var x uint64
	for {
		b := u.u8()
		if u.err != nil {
			return 0
		}
		if x >= limit {
			u.err = errBadBinaryChunk // integer overflow
			return 0
		}
		x = (x << 7) | uint64(b&0x7f)
		if b&0x80 != 0 {
			break
		}
	}
	return x
}

// count reads a length prefix and rejects values beyond dumpMaxItems so a
// hostile blob cannot drive a huge allocation.
func (u *undumpReader) count() int {
	n := u.size()
	if u.err != nil {
		return 0
	}
	if n > dumpMaxItems {
		u.err = errBadBinaryChunk
		return 0
	}
	return int(n)
}

// vInt reads a PUC dumpInt (a non-negative int through the varint encoding).
func (u *undumpReader) vInt() int {
	n := u.size()
	if u.err != nil {
		return 0
	}
	if n > uint64(maxInt32) {
		u.err = errBadBinaryChunk
		return 0
	}
	return int(n)
}

const maxInt32 = 1<<31 - 1

// strN reads a PUC string. A stored size of 0 means NULL (the caller treats
// that as "inherit"); otherwise the real length is size-1.
func (u *undumpReader) strN() (string, bool) {
	size := u.size()
	if u.err != nil || size == 0 {
		return "", true // NULL
	}
	n := size - 1
	if n == 0 {
		return "", false
	}
	if n > dumpMaxItems {
		u.err = errBadBinaryChunk
		return "", false
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(u.r, b); err != nil {
		u.err = errTruncated
		return "", false
	}
	return string(b), false
}

// str reads a string where NULL and empty are equivalent (locvar/upvalue names).
func (u *undumpReader) str() string {
	s, _ := u.strN()
	return s
}

func (u *undumpReader) constant() Value {
	switch u.u8() {
	case tagVNil:
		return Nil
	case tagVFalse:
		return False
	case tagVTrue:
		return True
	case tagVNumFlt:
		return Float(math.Float64frombits(u.u64()))
	case tagVNumInt:
		return Int(int64(u.u64()))
	case tagVShrStr, tagVLngStr:
		return MkString(u.str())
	default:
		if u.err == nil {
			u.err = errBadBinaryChunk
		}
		return Nil
	}
}

// decompressLines rebuilds luapure's absolute LineInfo from PUC's lineinfo
// (signed-byte deltas, with ABSLINEINFO markers) and abslineinfo entries,
// reversing compressLines / lcode.c savelineinfo.
func decompressLines(lineinfo []byte, abs []absEntry, lineDefined int) ([]int32, bool) {
	out := make([]int32, len(lineinfo))
	previousline := lineDefined
	absIdx := 0
	for pc := range lineinfo {
		if int8(lineinfo[pc]) == absLineInfo {
			if absIdx >= len(abs) || abs[absIdx].pc != pc {
				return nil, false // marker without a matching absolute entry
			}
			previousline = abs[absIdx].line
			absIdx++
		} else {
			previousline += int(int8(lineinfo[pc]))
		}
		out[pc] = int32(previousline)
	}
	if absIdx != len(abs) {
		return nil, false // stray absolute entries
	}
	return out, true
}

func (u *undumpReader) debug(p *Proto) {
	// lineinfo: signed-byte deltas, one per instruction.
	nline := u.count()
	lineinfo := make([]byte, nline)
	for i := range lineinfo {
		lineinfo[i] = u.u8()
	}

	// abslineinfo: absolute reference points.
	nabs := u.count()
	abs := make([]absEntry, nabs)
	for i := range abs {
		abs[i] = absEntry{pc: u.vInt(), line: u.vInt()}
	}

	// locvars.
	p.LocVars = make([]LocVar, u.count())
	for i := range p.LocVars {
		p.LocVars[i] = LocVar{Name: u.str(), StartPc: u.vInt(), EndPc: u.vInt()}
	}

	// upvalue names: PUC writes either 0 or exactly sizeupvalues of them.
	nup := u.count()
	if nup != 0 {
		if nup != len(p.Upvalues) {
			u.fail()
			return
		}
		for i := range p.Upvalues {
			p.Upvalues[i].Name = u.str()
		}
	}

	if u.err != nil {
		return
	}
	if nline > 0 {
		lines, ok := decompressLines(lineinfo, abs, p.LineDefined)
		if !ok {
			u.fail()
			return
		}
		p.LineInfo = lines
	}
}

func (u *undumpReader) function(parentSource string) *Proto {
	if u.err != nil {
		return nil
	}
	u.depth++
	if u.depth > dumpMaxDepth {
		u.err = errBadBinaryChunk
		return nil
	}
	defer func() { u.depth-- }()

	p := &Proto{}

	// Source dedup: a NULL source means "same as the parent" (PUC inherits the
	// enclosing proto's source). A genuinely absent source — a stripped
	// top-level dump — is rendered as "=?" so shortSrc yields "?" at every
	// observation point. Children inherit the raw (possibly empty) source.
	src, isNull := u.strN()
	if isNull {
		src = parentSource
	}
	if src == "" {
		p.Source = "=?"
	} else {
		p.Source = src
	}

	p.LineDefined = u.vInt()
	p.LastLineDef = u.vInt()
	p.NumParams = u.u8()
	p.IsVararg = u.u8() != 0
	p.MaxStackSize = u.u8()

	p.Code = make([]Instruction, u.count())
	for i := range p.Code {
		p.Code[i] = Instruction(u.u32())
	}

	p.Constants = make([]Value, u.count())
	for i := range p.Constants {
		p.Constants[i] = u.constant()
	}

	p.Upvalues = make([]UpvalDesc, u.count())
	for i := range p.Upvalues {
		p.Upvalues[i] = UpvalDesc{
			InStack: u.u8() != 0,
			Index:   u.u8(),
			Kind:    u.u8(),
		}
	}

	p.Protos = make([]*Proto, u.count())
	for i := range p.Protos {
		// PUC inherits source from this proto (raw src, before "=?" fallback).
		childParent := src
		p.Protos[i] = u.function(childParent)
	}

	u.debug(p)

	if u.err != nil {
		return nil
	}
	if err := validateProto(p); err != nil {
		u.err = err
		return nil
	}
	return p
}

// validateProto rejects bytecode that would index out of range: every
// instruction must carry a known opcode, and every OP_CLOSURE must point at an
// existing nested prototype.
func validateProto(p *Proto) error {
	for _, c := range p.Code {
		op := GetOpCode(c)
		if int(op) < 0 || int(op) >= NumOpCodes {
			return errBadBinaryChunk
		}
		if op == OP_CLOSURE {
			bx := GetArgBx(c)
			if bx < 0 || bx >= len(p.Protos) {
				return errBadBinaryChunk
			}
		}
	}
	return nil
}

// undump reconstructs a function prototype from a PUC 5.4 binary chunk. It
// returns an error (never panics) on any malformed input.
func undump(r io.Reader) (*Proto, error) {
	u := &undumpReader{r: r}
	u.checkHeader()
	if u.err != nil {
		return nil, u.err
	}
	nups := int(u.u8()) // closure nupvalues (validated against the proto below)
	if u.err != nil {
		return nil, u.err
	}
	p := u.function("")
	if u.err != nil {
		return nil, u.err
	}
	if nups != len(p.Upvalues) {
		return nil, errBadBinaryChunk
	}
	return p, nil
}

// checkHeader validates the PUC 5.4 binary-chunk header field by field. A
// corrupted field marks the chunk malformed; a short read marks it truncated.
func (u *undumpReader) checkHeader() {
	if string(u.read(len(luaSignature))) != luaSignature {
		u.fail()
		return
	}
	if u.u8() != luacVersion {
		u.fail()
		return
	}
	if u.u8() != luacFormat {
		u.fail()
		return
	}
	if string(u.read(len(luacData))) != luacData {
		u.fail()
		return
	}
	if u.u8() != dumpInstSize || u.u8() != dumpIntSize || u.u8() != dumpNumSize {
		u.fail()
		return
	}
	if u.u64() != uint64(luacInt) {
		u.fail()
		return
	}
	if math.Float64frombits(u.u64()) != luacNum {
		u.fail()
		return
	}
}
