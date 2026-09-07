package threadsafe_test

import (
	"fmt"
	"sync"

	"github.com/tecnickcom/nurago/pkg/threadsafe"
	"github.com/tecnickcom/nurago/pkg/threadsafe/tsmap"
)

// counter accepts any lock satisfying threadsafe.Locker, so callers choose
// between sync.Mutex, sync.RWMutex, or a custom lock without the type being
// hard-coded here.
type counter struct {
	mux    threadsafe.Locker
	values map[string]int
}

func (c *counter) inc(key string) {
	c.mux.Lock()
	defer c.mux.Unlock()

	c.values[key]++
}

func ExampleLocker() {
	c := &counter{
		mux:    &sync.Mutex{},
		values: make(map[string]int),
	}

	var wg sync.WaitGroup

	for range 100 {
		wg.Go(func() {
			c.inc("hits")
		})
	}

	wg.Wait()

	fmt.Println(c.values["hits"])

	// Output:
	// 100
}

// stats reads under a shared lock, so it declares threadsafe.RLocker and
// concurrent readers do not block each other.
type stats struct {
	mux    threadsafe.RLocker
	values map[string]int
}

func (s *stats) get(key string) int {
	s.mux.RLock()
	defer s.mux.RUnlock()

	return s.values[key]
}

func ExampleRLocker() {
	mux := &sync.RWMutex{}

	s := &stats{
		mux:    mux,
		values: map[string]int{"hits": 7},
	}

	// The same sync.RWMutex satisfies Locker for writes through tsmap.
	tsmap.Set(mux, s.values, "misses", 3)

	fmt.Println(s.get("hits"), s.get("misses"))

	// Output:
	// 7 3
}
