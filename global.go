package rediscache

import (
	"context"
	"errors"
	"log"
	"reflect"
	"runtime"
	"sync"
)

// Cache wraps the provided function with caching logic using default cache options.
//
// Parameters:
// - c: A pointer to the RedisCache instance.
// - f: The function to be wrapped with caching logic. It should be of any type.
//
// Returns:
// - The wrapped function with caching logic.
func Cache[F any](c *RedisCache, f F) F {
	return c.Cached(f).(F)
}

// CacheOpts wraps the provided function with caching logic using the specified cache options.
//
// Parameters:
// - c: A pointer to the RedisCache instance.
// - f: The function to be wrapped with caching logic. It should be of any type.
// - funcOpts: CacheOptions containing the configuration for caching behavior.
//
// Returns:
// - The wrapped function with caching logic.
func CacheOpts[F any](c *RedisCache, f F, funcOpts CacheOptions) F {
	return c.CachedOpts(f, funcOpts).(F)
}

type CtxArgFunc[I any, O any] func(ctx context.Context, in I) (O, error)
type CtxSliceFunc[I any, O any] func(ctx context.Context, in []I) ([]O, error)

type BulkReturn[O any] struct {
	Result O
	Error  error
}

func CacheBulk[IN any, OUT any](c *RedisCache, f CtxArgFunc[IN, OUT], funcOpts CacheOptions) func(ctx context.Context, in []IN) []BulkReturn[OUT] {
	cf := CacheOpts(c, f, funcOpts)
	return func(ctx context.Context, in []IN) []BulkReturn[OUT] {
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
						results[i] = BulkReturn[OUT]{Error: errors.New("panic in function")}
					}
				}()
				defer wg.Done()
				result, err := cf(ctx, input)
				results[i] = BulkReturn[OUT]{Result: result, Error: err}
			}(input)
		}
		wg.Wait()
		return results
	}
}

func CacheBulkSlice[IN any, OUT any](c *RedisCache, f CtxSliceFunc[IN, OUT], funcOpts CacheOptions, refreshEntireBatch bool) func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
	baseFunc := func(in IN) (OUT, error) {
		panic("not to be called")
	}

	functionConfig := c.setupCacheFunctionConfig(baseFunc, funcOpts)

	return func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
		keys := make([]string, len(in))
		doTiming := funcOpts.EnableTiming

		for i, input := range in {
			keys[i] = functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(input)})
		}

		for i, key := range keys {
			value, locked, err := c.getCachedValueOrLock(ctx, key, funcOpts, doTiming, true)

		}

		values, err := c.getCachedValues(ctx, keys, doTiming)
		if err == nil {
			// Ignore this as we can call the function to get the results. Log it for debugging.
		}

		// Check to see if all the values are cached
		uncachedIndexes := []int{}
		lockedIndexes := []int{}
		results := make([]BulkReturn[OUT], len(in))
		allCached := true
		for i, value := range values {
			if value == nil {
				allCached = false
				uncachedIndexes = append(uncachedIndexes, i)
				continue
			}
			// Check if the value is locked
			if len(value) == 0 {
				lockedIndexes = append(lockedIndexes, i)
				continue
			}
			cachedVals, _, err := deserializeCacheToResults(value, functionConfig.outputValueHandlers)
			if err != nil {
				allCached = false
				uncachedIndexes = append(uncachedIndexes, i)
				continue
			}
			results[i] = BulkReturn[OUT]{Result: cachedVals[0].Interface().(OUT), Error: nil}
		}

		if allCached {
			return results, nil
		}

		// If we are not refreshing the entire batch, we need to call the function for the uncached values
		var refreshArgs []IN
		var refreshIndexes []int
		var lockIndexes []int
		if !refreshEntireBatch {
			for _, i := range uncachedIndexes {
				refreshArgs = append(refreshArgs, in[i])
				refreshIndexes = append(refreshIndexes, i)
			}
			for _, i := range lockedIndexes {
				lockIndexes = append(lockIndexes, i)
			}
		} else {
			refreshArgs = in
			for i := range in {
				refreshIndexes = append(refreshIndexes, i)
			}
		}

		refreshResults, err := f(ctx, refreshArgs)

		for i, result := range refreshResults {
			results[refreshIndexes[i]] = BulkReturn[OUT]{Result: result, Error: err}
			if err == nil {
				// Cache the results
				serialized, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(result)}, functionConfig.outputValueHandlers)
				if err != nil {
					// Log this error
					continue
				}
				c.saveToCache(ctx, keys[refreshIndexes[i]], serialized, funcOpts)
			}
		}

		return results, nil
	}
}
