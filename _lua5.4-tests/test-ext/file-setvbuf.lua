-- file:setvbuf buffering modes, oracle: PUC Lua 5.4.8.
-- A second read handle (fr) on the same file observes when buffered output
-- actually reaches disk, so it distinguishes full / no / line buffering.

local file = os.tmpname()

-- "full": output is held until the buffer fills or the file is flushed/closed.
do
  local f = assert(io.open(file, "w"))
  local fr = assert(io.open(file, "r"))
  assert(f:setvbuf("full", 2000))
  f:write("x")
  assert(fr:read("a") == "")    -- not flushed yet
  f:close()                     -- close flushes the buffer
  fr:seek("set")
  assert(fr:read("a") == "x")
  fr:close()
end

-- "no": output is written through immediately.
do
  local f = assert(io.open(file, "w"))
  local fr = assert(io.open(file, "r"))
  assert(f:setvbuf("no"))
  f:write("y")
  fr:seek("set")
  assert(fr:read("a") == "y")   -- already on disk
  f:close(); fr:close()
end

-- "line": output is flushed on each newline.
do
  local f = assert(io.open(file, "w"))
  local fr = assert(io.open(file, "r"))
  assert(f:setvbuf("line"))
  f:write("z")                  -- no newline -> buffered
  fr:seek("set")
  assert(fr:read("a") == "")
  f:write("\n")                 -- newline -> flush
  fr:seek("set")
  assert(fr:read("a") == "z\n")
  f:close(); fr:close()
end

assert(os.remove(file))
print("file-setvbuf: all cases passed")
