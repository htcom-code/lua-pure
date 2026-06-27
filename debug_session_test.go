package luapure_test

import (
	"testing"

	luapure "github.com/htcom-code/lua-pure"
)

const dbgProg = `local function add(a, b)
  local s = a + b        -- line 2
  return s               -- line 3
end                      -- line 4
local total = 0          -- line 5
for i = 1, 3 do          -- line 6
  total = add(total, i)  -- line 7 (call site)
end                      -- line 8
return total             -- line 9`

// A breakpoint stops every time its line executes; the handler reads a local
// from the stopped frame.
func TestDebuggerBreakpoint(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	d := luapure.NewDebugger(L)
	d.SetBreakpoints("bp", []int{7})

	var iterValues []int64
	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		if reason != luapure.StopBreakpoint {
			t.Errorf("reason = %v, want breakpoint", reason)
		}
		f := d.Frames()[0]
		if f.CurrentLine() != 7 {
			t.Errorf("stopped on line %d, want 7", f.CurrentLine())
		}
		for _, lv := range f.Locals() {
			if lv.Name == "i" {
				iterValues = append(iterValues, lv.Value.AsInt())
			}
		}
		d.Continue()
	})

	res, err := L.DoString(dbgProg, "=bp")
	if err != nil {
		t.Fatal(err)
	}
	if got := res[0].AsInt(); got != 6 {
		t.Fatalf("program result = %d, want 6", got)
	}
	if len(iterValues) != 3 || iterValues[0] != 1 || iterValues[2] != 3 {
		t.Fatalf("loop var values at breakpoint = %v, want [1 2 3]", iterValues)
	}
}

// StepOver advances line-by-line in the current frame, treating a call as one
// step (it does not descend into add()).
func TestDebuggerStepOver(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	d := luapure.NewDebugger(L)
	d.SetBreakpoints("so", []int{5}) // stop once at `local total = 0`

	var lines []int
	stepping := false
	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		f := d.Frames()[0]
		lines = append(lines, f.CurrentLine())
		stepping = true
		d.StepOver()
	})

	if _, err := L.DoString(dbgProg, "=so"); err != nil {
		t.Fatal(err)
	}
	if !stepping {
		t.Fatal("never hit the initial breakpoint")
	}
	// After line 5 we step over: we must stay in the main chunk (never see
	// add()'s body lines 2/3) and visit the loop/return lines.
	for _, ln := range lines {
		if ln == 2 || ln == 3 {
			t.Fatalf("step-over descended into add() (line %d): %v", ln, lines)
		}
	}
	// Should have progressed past the breakpoint line.
	if len(lines) < 3 {
		t.Fatalf("step-over visited too few lines: %v", lines)
	}
}

// StepInto descends into the called function (we see add()'s body lines).
func TestDebuggerStepInto(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	d := luapure.NewDebugger(L)
	d.SetBreakpoints("si", []int{7}) // first call site

	sawAddBody := false
	steps := 0
	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		f := d.Frames()[0]
		ln := f.CurrentLine()
		if ln == 2 || ln == 3 {
			sawAddBody = true
		}
		steps++
		if steps > 6 { // bound the walk
			d.Continue()
			return
		}
		d.StepInto()
	})

	if _, err := L.DoString(dbgProg, "=si"); err != nil {
		t.Fatal(err)
	}
	if !sawAddBody {
		t.Fatal("step-into never reached add()'s body")
	}
}

// StepOut runs to the caller: after stopping inside add(), stepping out returns
// to the main chunk (depth decreases).
func TestDebuggerStepOut(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	d := luapure.NewDebugger(L)
	d.SetBreakpoints("out", []int{2}) // inside add()

	var depthAtBreak, depthAfterOut int
	phase := 0
	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		switch phase {
		case 0:
			depthAtBreak = d.L.StackDepth()
			phase = 1
			d.StepOut()
		case 1:
			depthAfterOut = d.L.StackDepth()
			phase = 2
			d.Continue()
		default:
			d.Continue()
		}
	})

	if _, err := L.DoString(dbgProg, "=out"); err != nil {
		t.Fatal(err)
	}
	if phase < 2 {
		t.Fatalf("step-out did not produce a second stop (phase=%d)", phase)
	}
	if depthAfterOut >= depthAtBreak {
		t.Fatalf("step-out depth %d not shallower than break depth %d", depthAfterOut, depthAtBreak)
	}
}

// The OnStop handler can coordinate with a controller on another goroutine: the
// handler blocks reading a command channel, the controller sends a resume. This
// is the model a TCP/DAP front end uses.
func TestDebuggerCrossGoroutineHandoff(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	d := luapure.NewDebugger(L)
	d.SetBreakpoints("hand", []int{7})

	type stopInfo struct {
		line int
		i    int64
		ack  chan struct{}
	}
	stops := make(chan stopInfo)

	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		f := d.Frames()[0]
		si := stopInfo{line: f.CurrentLine(), ack: make(chan struct{})}
		for _, lv := range f.Locals() {
			if lv.Name == "i" {
				si.i = lv.Value.AsInt()
			}
		}
		stops <- si  // hand the stop to the controller goroutine
		<-si.ack     // block until it tells us to resume
		d.Continue() // resume action issued on the VM goroutine
	})

	done := make(chan []luapure.Value, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := L.DoString(dbgProg, "=hand")
		if err != nil {
			errc <- err
			return
		}
		done <- res
	}()

	// Controller goroutine: receive each stop, verify, acknowledge.
	var seen []int64
	for {
		select {
		case si := <-stops:
			if si.line != 7 {
				t.Errorf("stop line = %d, want 7", si.line)
			}
			seen = append(seen, si.i)
			close(si.ack)
		case err := <-errc:
			t.Fatalf("run error: %v", err)
		case res := <-done:
			if res[0].AsInt() != 6 {
				t.Fatalf("result = %d, want 6", res[0].AsInt())
			}
			if len(seen) != 3 {
				t.Fatalf("controller saw %d stops, want 3 (%v)", len(seen), seen)
			}
			return
		}
	}
}
