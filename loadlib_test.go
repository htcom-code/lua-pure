package luapure

import "testing"

func TestRequireCoreLibs(t *testing.T) {
	r := runLib(t, `return require"string" == string, require"table" == table,
		require"math" == math, require"os" == os, require"io" == io,
		type(package) == "table", type(package.loaded) == "table"`)
	for i := 0; i < 5; i++ {
		wantBool(t, r[i], true)
	}
	wantBool(t, r[5], true)
	wantBool(t, r[6], true)
}

func TestLoad(t *testing.T) {
	r := runLib(t, `
local f = load("return 1 + 2")
local g, err = load("this is not lua $$$")
local h = load("return x + 1", "chunk", "t", {x = 41})
return f(), g == nil, type(err) == "string", h()`)
	wantInt(t, r[0], 3)
	wantBool(t, r[1], true)
	wantBool(t, r[2], true)
	wantInt(t, r[3], 42)
}

func TestLoadWithEnv(t *testing.T) {
	r := runLib(t, `
local env = {}
local f = load("a = 10; return a", "c", "t", env)
local result = f()
return result, env.a`)
	wantInt(t, r[0], 10)
	wantInt(t, r[1], 10)
}

func TestOsBasics(t *testing.T) {
	r := runLib(t, `
return type(os.time()) == "number", type(os.clock()) == "number",
       os.difftime(10, 4) == 6.0, type(os.date()) == "string",
       type(os.date("*t").year) == "number"`)
	for i := 0; i < 5; i++ {
		wantBool(t, r[i], true)
	}
}

func TestIoOpenReadWrite(t *testing.T) {
	r := runLib(t, `
local name = os.tmpname()
local f = io.open(name, "w")
f:write("hello\n")
f:write("world\n")
f:close()
local g = io.open(name, "r")
local l1 = g:read("l")
local l2 = g:read("L")
g:close()
os.remove(name)
return l1, l2`)
	wantStr(t, r[0], "hello")
	wantStr(t, r[1], "world\n")
}

func TestIoLines(t *testing.T) {
	r := runLib(t, `
local name = os.tmpname()
local f = io.open(name, "w")
f:write("a\nb\nc\n")
f:close()
local n, last = 0, nil
for line in io.lines(name) do n = n + 1; last = line end
os.remove(name)
return n, last`)
	wantInt(t, r[0], 3)
	wantStr(t, r[1], "c")
}
