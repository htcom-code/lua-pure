package luapure_test

import (
	"sync"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// NewState must be safe to call from many goroutines at once — a State pool
// builds states concurrently. Run under -race; this caught a data race on the
// auto-seed counter in newRNG (a plain ++ shared across NewState calls).
func TestConcurrentNewState(t *testing.T) {
	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			L := luapure.NewState(luapure.WithOpenLibs())
			res, err := L.DoString(`return math.random ~= nil and 1 + 1 or 0`, "=c")
			if err != nil {
				errs <- err
				return
			}
			if res[0].AsInt() != 2 {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
