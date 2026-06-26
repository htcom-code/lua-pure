package luapure

import "testing"

func TestCoroutineBasic(t *testing.T) {
	r := runLib(t, `
local co = coroutine.create(function(a, b)
  local c = coroutine.yield(a + b)
  local d = coroutine.yield(c * 2)
  return d + 1
end)
local ok1, v1 = coroutine.resume(co, 3, 4)   -- a+b = 7
local ok2, v2 = coroutine.resume(co, 10)      -- c=10 -> 20
local ok3, v3 = coroutine.resume(co, 100)     -- d=100 -> 101
local ok4, v4 = coroutine.resume(co)          -- dead
return ok1, v1, ok2, v2, ok3, v3, ok4`)
	wantBool(t, r[0], true)
	wantInt(t, r[1], 7)
	wantBool(t, r[2], true)
	wantInt(t, r[3], 20)
	wantBool(t, r[4], true)
	wantInt(t, r[5], 101)
	wantBool(t, r[6], false) // resuming dead coroutine
}

func TestCoroutineStatus(t *testing.T) {
	r := runLib(t, `
local co = coroutine.create(function() coroutine.yield() end)
local s1 = coroutine.status(co)
coroutine.resume(co)
local s2 = coroutine.status(co)
coroutine.resume(co)
local s3 = coroutine.status(co)
return s1, s2, s3`)
	wantStr(t, r[0], "suspended")
	wantStr(t, r[1], "suspended")
	wantStr(t, r[2], "dead")
}

func TestCoroutineWrap(t *testing.T) {
	r := runLib(t, `
local gen = coroutine.wrap(function()
  for i = 1, 3 do coroutine.yield(i * i) end
end)
return gen(), gen(), gen()`)
	wantInt(t, r[0], 1)
	wantInt(t, r[1], 4)
	wantInt(t, r[2], 9)
}

func TestCoroutineWrapError(t *testing.T) {
	r := runLib(t, `
local f = coroutine.wrap(function() error("boom") end)
local ok, msg = pcall(f)
return ok, type(msg) == "string"`)
	wantBool(t, r[0], false)
	wantBool(t, r[1], true)
}

func TestCoroutineResumeError(t *testing.T) {
	r := runLib(t, `
local co = coroutine.create(function() error("kaboom") end)
local ok, msg = coroutine.resume(co)
return ok, type(msg) == "string", coroutine.status(co)`)
	wantBool(t, r[0], false)
	wantBool(t, r[1], true)
	wantStr(t, r[2], "dead")
}

func TestCoroutineRunning(t *testing.T) {
	r := runLib(t, `
local mainco, ismain = coroutine.running()
local inner
local co = coroutine.create(function()
  local c, m = coroutine.running()
  inner = (c ~= mainco) and (m == false)
end)
coroutine.resume(co)
return ismain, inner, coroutine.isyieldable()`)
	wantBool(t, r[0], true)  // main thread is main
	wantBool(t, r[1], true)  // inner coroutine is not main
	wantBool(t, r[2], false) // main thread is not yieldable
}

func TestCoroutineYieldThroughPcall(t *testing.T) {
	// gopher's goroutine model allows yielding across a pcall boundary.
	r := runLib(t, `
local co = coroutine.create(function()
  pcall(function()
    coroutine.yield("from inside pcall")
  end)
  return "done"
end)
local ok1, v1 = coroutine.resume(co)
local ok2, v2 = coroutine.resume(co)
return ok1, v1, ok2, v2`)
	wantBool(t, r[0], true)
	wantStr(t, r[1], "from inside pcall")
	wantBool(t, r[2], true)
	wantStr(t, r[3], "done")
}

func TestCoroutineProducerConsumer(t *testing.T) {
	r := runLib(t, `
local function producer()
  return coroutine.wrap(function()
    for i = 1, 5 do coroutine.yield(i) end
  end)
end
local sum = 0
for v in producer() do sum = sum + v end
return sum`)
	wantInt(t, r[0], 15)
}

func TestCoroutineYieldOutsideError(t *testing.T) {
	L := NewState()
	L.OpenLibs()
	_, err := L.DoString(`return coroutine.yield(1)`, "=t")
	if err == nil {
		t.Fatal("expected error yielding from main thread")
	}
}
