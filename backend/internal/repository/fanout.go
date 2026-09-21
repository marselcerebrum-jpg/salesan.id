package repository

import (
	"fmt"
	"sync"
)

// Running the same aggregate many times, in parallel, without losing the first
// failure or the whole process.
//
// Two reports are built this way: the per-application split and the per-person
// table. Both reuse the ordinary performance aggregate rather than writing a
// second set of grouped queries, because a parallel set of SQL would drift from
// the cards it sits under, and the place that drift shows is one screen quoting
// two different numbers for the same day.

// analyticsFanout is the whole server's budget for report fan-outs.
//
// Shared, deliberately, rather than one budget per endpoint. Opening Performa
// fires the per-application breakdown and the per-person table at the same
// time; with a cap each, the two together could hold eight of the pool's
// connections and leave the inbox queueing behind a report nobody has looked at
// yet. One budget means the total is bounded no matter how many reports are in
// flight, and the number that matters is how much of the pool analytics may
// ever hold: three of twenty-four.
//
// The effect of the cap is that reports get slower under load, which is the
// right thing to give up. Chat is what people notice.
var analyticsFanout = make(chan struct{}, 3)

// fanOut runs n units of work, bounded by the shared analytics budget.
//
// Returns the first error rather than a list: the caller renders a failure, and
// twelve copies of "connection refused" says nothing the first one did not. A
// panic inside a unit becomes an error too, because a panic in a goroutine takes
// the whole process down, and these run on every page load.
//
// `work` is expected to write its result into a slot the caller owns, indexed by
// i. Distinct indices need no lock; anything shared does.
func fanOut(n, limit int, work func(i int) error) error {
	if n == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		slot  = make(chan struct{}, limit)
		first error
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if first == nil {
			first = err
		}
	}

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					fail(fmt.Errorf("panic: %v", p))
				}
			}()

			// Two gates, in this order: this report's own pacing first, then
			// the server-wide budget. A report can therefore never have more
			// units queued against the shared budget than its own cap allows,
			// which is what stops one reader crowding out another.
			slot <- struct{}{}
			defer func() { <-slot }()

			analyticsFanout <- struct{}{}
			defer func() { <-analyticsFanout }()

			if err := work(i); err != nil {
				fail(err)
			}
		}(i)
	}
	wg.Wait()

	return first
}
