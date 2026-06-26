package luapure

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// Binary chunk (de)serialization for string.dump / load(binary chunk).
//
// This is gopher-lua's OWN format, keyed to this engine's Proto layout. It is
// deliberately NOT PUC-Lua bytecode: the luapure opcodes match PUC's instruction
// set, but the proto/value model is Go, so a portable bytecode file is neither
// possible nor a goal (the C standard calls dumped bytecode implementation
// defined). The contract honoured is the observable one:
// load(string.dump(f))(args) == f(args).
//
// The format is versioned and not portable across engine versions — the magic
// plus version byte reject any blob this exact format did not produce. Upvalue
// *values* are not serialized (PUC does not either); a reloaded function binds
// its upvalues afresh, with _ENV rebound by the loader like any loaded chunk.
//
// SECURITY: undump reconstructs a Proto from untrusted bytes. It validates
// aggressively — bounded slice lengths, bounded recursion depth, opcode
// validity, and in-range nested-closure indices — and never panics on malformed
// input, returning an error instead. Fully sandboxing hostile bytecode is not a
// goal; embedders that must not load binary chunks should reject them first.
// The binary chunk begins with PUC Lua 5.4's exact header (signature, version,
// format, LUAC_DATA, type sizes, LUAC_INT, LUAC_NUM) so the conformance suite's
// header/size checks pass and any non-5.4 blob is rejected. The function body
// that follows is still gopher-lua's own format keyed to this engine's Proto
// layout (a portable bytecode file is neither possible nor a goal); the contract
// honoured is load(string.dump(f))(args) == f(args).
const (
	luaSignature = "\x1bLua"             // LUA_SIGNATURE
	luacVersion  = 0x54                  // 5.4
	luacFormat   = 0                     // LUAC_FORMAT
	luacData     = "\x19\x93\r\n\x1a\n"  // LUAC_DATA (catches transfer corruption)
	luacInt      = 0x5678                // LUAC_INT (endianness/size check)
	luacNum      = 370.5                 // LUAC_NUM (float format check)
	dumpInstSize = 4                     // sizeof(Instruction)
	dumpIntSize  = 8                     // sizeof(lua_Integer)
	dumpNumSize  = 8                     // sizeof(lua_Number)

	dumpMaxItems = 1 << 26 // ceiling on any length-prefixed slice/string
	dumpMaxDepth = 200     // ceiling on nested-prototype recursion
)

// Constant type tags (constants are only nil/bool/number/string by construction).
const (
	dumpConstNil = iota
	dumpConstFalse
	dumpConstTrue
	dumpConstFloat
	dumpConstInt
	dumpConstString
)

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

func (d *dumpWriter) i(v int) { d.u64(uint64(int64(v))) }

func (d *dumpWriter) str(s string) {
	d.u32(uint32(len(s)))
	d.raw(s)
}

func (d *dumpWriter) constant(k Value) {
	switch {
	case k.IsNil():
		d.u8(dumpConstNil)
	case k.IsBool():
		if k.AsBool() {
			d.u8(dumpConstTrue)
		} else {
			d.u8(dumpConstFalse)
		}
	case k.IsInt():
		d.u8(dumpConstInt)
		d.u64(uint64(k.AsInt()))
	case k.IsFloat():
		d.u8(dumpConstFloat)
		d.u64(math.Float64bits(k.AsFloat()))
	case k.IsString():
		d.u8(dumpConstString)
		d.str(k.Str())
	default:
		d.err = errBadBinaryChunk
	}
}

func (d *dumpWriter) function(p *Proto, strip bool, parentSource string) {
	// Source dedup (PUC dumpFunction): a nested proto that shares its parent's
	// source writes an empty string and inherits it on load, so the source text
	// is stored once for the whole chunk rather than per proto.
	if strip || p.Source == parentSource {
		d.str("")
	} else {
		d.str(p.Source)
	}
	d.i(p.LineDefined)
	d.i(p.LastLineDef)
	d.u8(p.NumParams)
	if p.IsVararg {
		d.u8(1)
	} else {
		d.u8(0)
	}
	d.u8(p.MaxStackSize)

	d.u32(uint32(len(p.Code)))
	for _, c := range p.Code {
		d.u32(uint32(c))
	}

	d.u32(uint32(len(p.Constants)))
	for _, k := range p.Constants {
		d.constant(k)
	}

	d.u32(uint32(len(p.Protos)))
	for _, np := range p.Protos {
		d.function(np, strip, p.Source)
	}

	// Upvalue descriptors are always needed (the VM binds nested closures from
	// them); only the names are debug info, blanked when stripping.
	d.u32(uint32(len(p.Upvalues)))
	for _, uv := range p.Upvalues {
		if strip {
			d.str("")
		} else {
			d.str(uv.Name)
		}
		if uv.InStack {
			d.u8(1)
		} else {
			d.u8(0)
		}
		d.u8(uv.Index)
		d.u8(uv.Kind)
	}

	// Line/local debug info is optional: a stripped dump omits it (a single 0
	// byte), so the reloaded function reports no line info.
	if strip {
		d.u8(0)
		return
	}
	d.u8(1)
	d.u32(uint32(len(p.LineInfo)))
	for _, x := range p.LineInfo {
		d.u32(uint32(x))
	}
	d.u32(uint32(len(p.LocVars)))
	for _, l := range p.LocVars {
		d.str(l.Name)
		d.i(l.StartPc)
		d.i(l.EndPc)
	}
}

// dumpProto serializes a function prototype into a self-describing binary chunk.
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
	d.u64(uint64(luacInt))            // LUAC_INT, 8-byte little-endian
	d.u64(math.Float64bits(luacNum))  // LUAC_NUM, 8-byte little-endian double
	d.function(p, strip, "") // top level has no parent source to inherit
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
func (u *undumpReader) i() int      { return int(int64(u.u64())) }

// count reads a length prefix and rejects values beyond dumpMaxItems so a
// hostile blob cannot drive a huge allocation.
func (u *undumpReader) count() int {
	n := u.u32()
	if u.err != nil {
		return 0
	}
	if n > dumpMaxItems {
		u.err = errBadBinaryChunk
		return 0
	}
	return int(n)
}

func (u *undumpReader) str() string {
	n := u.count()
	if u.err != nil || n == 0 {
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(u.r, b); err != nil {
		u.err = errTruncated
		return ""
	}
	return string(b)
}

func (u *undumpReader) constant() Value {
	switch u.u8() {
	case dumpConstNil:
		return Nil
	case dumpConstFalse:
		return False
	case dumpConstTrue:
		return True
	case dumpConstFloat:
		return Float(math.Float64frombits(u.u64()))
	case dumpConstInt:
		return Int(int64(u.u64()))
	case dumpConstString:
		return MkString(u.str())
	default:
		if u.err == nil {
			u.err = errBadBinaryChunk
		}
		return Nil
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
	// Source dedup: an empty source means "same as the parent" (PUC inherits the
	// enclosing proto's source). A genuinely empty source — a stripped top-level
	// dump — leaves it NULL in PUC; we render that as "=?" so shortSrc yields "?"
	// at every observation point. Children inherit the raw (possibly empty) src.
	src := u.str()
	if src == "" {
		src = parentSource
	}
	if src == "" {
		p.Source = "=?"
	} else {
		p.Source = src
	}
	p.LineDefined = u.i()
	p.LastLineDef = u.i()
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

	p.Protos = make([]*Proto, u.count())
	for i := range p.Protos {
		p.Protos[i] = u.function(src)
	}

	p.Upvalues = make([]UpvalDesc, u.count())
	for i := range p.Upvalues {
		p.Upvalues[i] = UpvalDesc{
			Name:    u.str(),
			InStack: u.u8() != 0,
			Index:   u.u8(),
			Kind:    u.u8(),
		}
	}

	if u.u8() != 0 {
		p.LineInfo = make([]int32, u.count())
		for i := range p.LineInfo {
			p.LineInfo[i] = int32(u.u32())
		}
		p.LocVars = make([]LocVar, u.count())
		for i := range p.LocVars {
			p.LocVars[i] = LocVar{Name: u.str(), StartPc: u.i(), EndPc: u.i()}
		}
	}

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

// undump reconstructs a function prototype from a binary chunk produced by
// dumpProto. It returns an error (never panics) on any malformed input.
func undump(r io.Reader) (*Proto, error) {
	u := &undumpReader{r: r}
	u.checkHeader()
	if u.err != nil {
		return nil, u.err
	}
	p := u.function("")
	if u.err != nil {
		return nil, u.err
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
