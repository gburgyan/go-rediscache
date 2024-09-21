package rediscache

import (
	"context"
	"log"
	"runtime"
	"sync"
)

func parallelRun[IN any, OUT any](ctx context.Context, in []IN, f func(context.Context, IN) (OUT, error)) []BulkReturn[OUT] {
	results := make([]BulkReturn[OUT], len(in))
	var wg sync.WaitGroup

	for i, input := range in {
		wg.Add(1)
		i := i
		go func(input IN) {
			// Panic check
			defer func() {
				if r := recover(); r != nil {
					stackTrace := make([]byte, 1<<16)
					stackTrace = stackTrace[:runtime.Stack(stackTrace, true)]
					log.Printf("Panic in function: %v\n%s", r, stackTrace)
				}
				wg.Done()
			}()

			result, err := f(ctx, input)
			results[i] = BulkReturn[OUT]{Result: result, Error: err}
		}(input)
	}

	wg.Wait()
	return results
}
