package luapure

// Buffered input stream — a port of PUC-Lua 5.4.8's lzio.c / lzio.h (ZIO).
//
// PUC funnels every chunk source through a ZIO: load(string) (getS), loadfile
// (getF), and load(reader function) all hand lua_load a reader, and the lexer
// pulls bytes from the ZIO via zgetc, calling luaZ_fill (the reader) when the
// buffer empties. This file is that single abstraction, so the front end has
// one input path regardless of where the source comes from and never has to
// hold the whole chunk in memory (an unbounded reader is lexed incrementally).

// ZIO is a byte stream backed by an optional refill reader (PUC struct Zio).
// reader returns the next piece and ok=false at end of input; a nil/empty
// piece also ends the stream, matching PUC's treatment of a NULL/zero-size
// reader result as EOZ.
type ZIO struct {
	reader func() (string, bool)
	buf    string // current piece
	pos    int    // next byte in buf
}

// newStringZIO streams a complete string: one piece, then EOZ (PUC getS).
func newStringZIO(s string) *ZIO { return &ZIO{buf: s} }

// newReaderZIO streams an initial piece followed by whatever reader yields,
// pulled on demand (PUC getF / a load() reader function).
func newReaderZIO(initial string, reader func() (string, bool)) *ZIO {
	return &ZIO{buf: initial, reader: reader}
}

// getc returns the next byte, or eoz at end of input (PUC zgetc).
func (z *ZIO) getc() int {
	if z.pos >= len(z.buf) && !z.fill() {
		return eoz
	}
	c := int(z.buf[z.pos])
	z.pos++
	return c
}

// fill refills buf from the reader (PUC luaZ_fill). Returns false at end of
// input; a nil/empty piece ends the stream (and clears the reader so further
// getc calls are cheap EOZ).
func (z *ZIO) fill() bool {
	if z.reader == nil {
		return false
	}
	chunk, ok := z.reader()
	if !ok || len(chunk) == 0 {
		z.reader = nil
		return false
	}
	z.buf = chunk
	z.pos = 0
	return true
}
