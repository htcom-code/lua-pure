-- io.tmpfile: an anonymous read/write file, oracle PUC Lua 5.4.8.
local f = io.tmpfile()
assert(io.type(f) == "file")
f:write("alo")
f:seek("set")
assert(f:read("a") == "alo")
assert(f:close())
print("io-tmpfile: all cases passed")
