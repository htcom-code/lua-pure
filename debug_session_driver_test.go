package luapure_test

import (
	"fmt"
	"strings"
	"testing"

	luapure "github.com/htcom-code/lua-pure"
)

// The full front-end flow when the script lives only on the server: the
// controller holds no source text, drives the session by chunk id + line, and
// fetches display context from the server's "DB" via the resolver.
func TestSessionSourceOnServer(t *testing.T) {
	// Server-side "database": the only place the source exists.
	db := map[string]string{
		"script/42": "local function add(a, b)\n" + // line 1
			"  return a + b\n" + // line 2
			"end\n" + // line 3
			"local total = 0\n" + // line 4
			"for i = 1, 3 do\n" + // line 5
			"  total = add(total, i)\n" + // line 6 (breakpoint)
			"end\n" + // line 7
			"return total\n", // line 8
	}

	L := luapure.NewState()
	L.OpenLibs()
	sess := luapure.NewSession(L, func(id string) (string, bool) {
		src, ok := db[id]
		return src, ok
	})
	sess.SetBreakpoints("script/42", []int{6})

	// The controller does NOT have the source; it only knows the id.
	const src = "" // intentionally empty here — it comes from the DB on the server
	source := db["script/42"]
	done := sess.Start(source, "=script/42")
	_ = src

	var seenIters []string
	var snippetShown bool
	for {
		select {
		case st := <-sess.Stops():
			if st.Source != "script/42" {
				t.Fatalf("stop source id = %q, want script/42", st.Source)
			}
			if st.Line != 6 {
				t.Fatalf("stop line = %d, want 6", st.Line)
			}
			// Fetch display context purely from the server (client has no source).
			if snip, ok := sess.Snippet(st.Source, st.Line, 1); ok {
				if !strings.Contains(snip, "-> 6") {
					t.Errorf("snippet missing current-line marker:\n%s", snip)
				}
				snippetShown = true
			} else {
				t.Error("Snippet returned nothing despite a resolver")
			}
			// Inspect: evaluate the loop variable in scope.
			val, err := sess.Eval(0, "i")
			if err != nil {
				t.Fatalf("eval i: %v", err)
			}
			seenIters = append(seenIters, val)
			sess.Continue()
		case r := <-done:
			if r.Err != nil {
				t.Fatalf("run error: %v", r.Err)
			}
			if got := r.Values[0].AsInt(); got != 6 {
				t.Fatalf("result = %d, want 6", got)
			}
			if !snippetShown {
				t.Fatal("never fetched a source snippet from the server")
			}
			if strings.Join(seenIters, ",") != "1,2,3" {
				t.Fatalf("loop var values via Eval = %v, want [1 2 3]", seenIters)
			}
			return
		}
	}
}

// Stack and Variables snapshot a deeper stop for a front end to render.
func TestSessionStackAndVariables(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	sess := luapure.NewSession(L, nil)
	sess.SetBreakpoints("s", []int{2}) // inside add()

	// Non-tail call so add() and main both stay on the stack.
	source := "local function add(a, b)\n  return a + b\nend\nlocal r = add(3, 4)\nreturn r\n"
	done := sess.Start(source, "=s")

	checked := false
	for {
		select {
		case <-sess.Stops():
			stack := sess.Stack()
			if len(stack) < 2 {
				t.Fatalf("stack depth = %d, want >= 2 (add, main)", len(stack))
			}
			if stack[0].Func != "add" {
				t.Errorf("innermost func = %q, want add", stack[0].Func)
			}
			vars := sess.Variables(0)
			got := map[string]string{}
			for _, v := range vars {
				if v.Kind == "local" {
					got[v.Name] = v.Value
				}
			}
			if got["a"] != "3" || got["b"] != "4" {
				t.Errorf("locals a,b = %q,%q want 3,4 (all: %+v)", got["a"], got["b"], vars)
			}
			checked = true
			sess.Continue()
		case r := <-done:
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			if !checked {
				t.Fatal("breakpoint never hit")
			}
			return
		}
	}
}

// Example_session shows the front-end loop: a breakpoint stops the program, the
// controller reads a variable, then resumes. Source is fetched by id, never
// held by the controller.
func Example_session() {
	db := map[string]string{
		"demo": "local x = 21\nlocal y = x * 2\nreturn y\n",
	}
	L := luapure.NewState()
	L.OpenLibs()
	sess := luapure.NewSession(L, func(id string) (string, bool) { s, ok := db[id]; return s, ok })
	sess.SetBreakpoints("demo", []int{2}) // stop at `local y = x * 2`

	done := sess.Start(db["demo"], "=demo")
	for {
		select {
		case st := <-sess.Stops():
			x, _ := sess.Eval(0, "x")
			fmt.Printf("stopped at %s:%d, x=%s\n", st.Source, st.Line, x)
			sess.Continue()
		case r := <-done:
			if r.Err != nil {
				fmt.Println("error:", r.Err)
				return
			}
			fmt.Printf("result=%d\n", r.Values[0].AsInt())
			// Output:
			// stopped at demo:2, x=21
			// result=42
			return
		}
	}
}
