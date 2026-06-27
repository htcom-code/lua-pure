-- lua-pure PUC-conformance regression: io file read/seek/type behaviour
--
-- The headline case: file:seek("cur") (and the no-arg form) must report the
-- LOGICAL read position, not the underlying fd offset. lua-pure buffers reads
-- with a bufio.Reader that reads ahead, so the fd sits at EOF after the first
-- read; seek("cur") previously returned that fd offset instead of subtracting
-- the buffered bytes (C stdio's ftell accounts for the buffer). Also pins the
-- read-format menu, io.type, and the open-failure tuple. Pinned to lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("io [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

local TMP = os.tmpname()

-- seek("cur") after a buffered read reports the logical position
do
  local f = assert(io.open(TMP, "w"))
  f:write("0123456789")
  f:close()
  local g = assert(io.open(TMP, "r"))
  eq(g:seek(), 0, "start position")
  g:read(3)
  eq(g:seek(), 3, "position after read(3)")     -- was EOF (10) before the fix
  eq(g:seek("cur", 2), 5, "relative cur+2")
  g:read(1)
  eq(g:seek(), 6, "position after cur seek + read(1)")
  eq(g:seek("set", 7), 7, "absolute set")
  eq(g:seek("end"), 10, "end")
  eq(g:seek("end", -4), 6, "end-4")
  g:close()
end

-- read-format menu: a (all), l (line, no EOL), L (line, keep EOL), n (number)
do
  local f = assert(io.open(TMP, "w"))
  f:write("alpha\nbeta\n")
  f:close()
  local g = assert(io.open(TMP, "r"))
  eq(g:read("l"), "alpha", "read 'l' strips EOL")
  eq(g:read("L"), "beta\n", "read 'L' keeps EOL")
  g:close()

  f = assert(io.open(TMP, "w"))
  f:write("42 3.5")
  f:close()
  g = assert(io.open(TMP, "r"))
  local a, b = g:read("n", "n")
  eq(a, 42, "read 'n' integer")
  eq(b, 3.5, "read 'n' float")
  g:close()

  f = assert(io.open(TMP, "w"))
  f:write("the whole thing")
  f:close()
  g = assert(io.open(TMP, "r"))
  eq(g:read("a"), "the whole thing", "read 'a' whole file")
  eq(g:read("a"), "", "read 'a' at EOF is empty string")
  g:close()
end

-- io.lines streams a file line by line
do
  local f = assert(io.open(TMP, "w"))
  f:write("x\ny\nz\n")
  f:close()
  local acc = {}
  for line in io.lines(TMP) do acc[#acc + 1] = line end
  eq(table.concat(acc, ","), "x,y,z", "io.lines")
end

-- io.type reports "file" for an open handle, "closed file" once closed, nil otherwise
do
  local f = assert(io.open(TMP, "r"))
  eq(io.type(f), "file", "io.type open")
  f:close()
  eq(io.type(f), "closed file", "io.type closed")
  eq(io.type("not a file"), nil, "io.type non-file")
end

-- a failed open returns the (nil, message, errno) tuple, not an error
do
  local f, msg, code = io.open("/no/such/dir/zzz_" .. tostring(TMP):gsub("%W", ""), "r")
  eq(f, nil, "failed open returns nil")
  eq(type(msg), "string", "failed open returns a message")
  eq(math.type(code), "integer", "failed open returns an integer errno")
end

os.remove(TMP)
print("io-file-ops: all cases passed")
