package rediscache

import (
	"context"
	"fmt"
	"github.com/gburgyan/go-timing"
	"log"
	"reflect"
	"sort"
	"sync"
)

type CtxArgFunc[IN any, OUT any] func(ctx context.Context, in IN) (OUT, error)
type CtxSliceFunc[IN any, OUT any] func(ctx context.Context, in []IN) ([]OUT, error)

type BulkReturn[OUT any] struct {
	Result OUT
	Error  error
}

func CacheBulk[IN any, OUT any](c *RedisCache, f CtxArgFunc[IN, OUT], funcOpts CacheOptions) func(ctx context.Context, in []IN) []BulkReturn[OUT] {
	cf := CacheOpts(c, f, funcOpts)
	return func(ctx context.Context, in []IN) []BulkReturn[OUT] {
		return parallelRun(ctx, in, cf)
	}
}

type keyStatus[IN any, OUT any] struct {
	index       int
	key         string
	cachedValue []byte
	status      LockStatus

	input     IN
	resultVal OUT
	resultErr error
}

// CacheBulkSlice is a function that returns a function to handle bulk caching operations.
// It takes a RedisCache instance, a function to process the input slice, cache options,
// and a boolean to indicate if the entire batch should be refreshed if there is a cache miss
// on any input.
//
// Parameters:
//   - c: *RedisCache - The Redis cache instance.
//   - f: CtxSliceFunc[IN, OUT] - The function to process the input slice.
//   - funcOpts: CacheOptions - The cache options.
//   - refreshEntireBatch: bool - Whether to refresh the entire batch if there
//     is a cache miss on any input.
//
// Returns:
// - func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) - A function that processes the input slice and returns the results.
func CacheBulkSlice[IN any, OUT any](c *RedisCache, f CtxSliceFunc[IN, OUT], funcOpts CacheOptions) func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
	// Setup the cache function configuration
	functionConfig := c.setupCacheFunctionConfig(func(IN) OUT { panic("not to be called") }, funcOpts)

	return func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
		doTiming := funcOpts.EnableTiming

		var timingCtx *timing.Context
		if doTiming {
			var complete timing.Complete
			timingCtx, complete = timing.Start(ctx, "CacheBulkSlice-"+functionConfig.returnTypeKey)
			ctx = timingCtx
			defer complete()
		}

		var initialLockComplete timing.Complete
		initialLockCtx := ctx
		if doTiming {
			var initialLockTimingCtx *timing.Context
			initialLockTimingCtx, initialLockComplete = timing.Start(ctx, "InitialLock")
			initialLockTimingCtx.Async = true
			initialLockCtx = initialLockTimingCtx
		}

		// Run the parallel function to get key statuses
		keyStatuses := parallelRun(initialLockCtx, in, func(ctx context.Context, input IN) (keyStatus[IN, OUT], error) {
			key := functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(input)})
			value, locked, err := c.getCachedValueOrLock(ctx, key, funcOpts, doTiming, true)
			return keyStatus[IN, OUT]{key: key, cachedValue: value, status: locked, input: input}, err
		})

		if doTiming {
			initialLockComplete()
		}

		var items, cachedItems, alreadyLocked, lockedItems, alreadyLockedItems []*keyStatus[IN, OUT]

		// Categorize key statuses
		for i, item := range keyStatuses {
			status := item.Result
			status.index = i
			status.input = in[i]
			items = append(items, &status)
			switch {
			case item.Error != nil || status.status == LockStatusError:
				alreadyLocked = append(alreadyLocked, &status)
			case len(status.cachedValue) > 0:
				cachedItems = append(cachedItems, &status)
			case status.status == LockStatusLockAcquired:
				lockedItems = append(lockedItems, &status)
			case status.status == LockStatusLockFailed:
				alreadyLockedItems = append(alreadyLockedItems, &status)
			default:
				panic("unexpected lock status")
			}
		}

		// If all items are cached, deserialize and return results
		if len(cachedItems) == len(in) {
			var deserializeComplete timing.Complete
			if doTiming {
				_, deserializeComplete = timing.Start(ctx, "DeserializeAllCachedResults")
				timingCtx.AddDetails("all-hit", true)
				timingCtx.AddDetails("miss", len(alreadyLocked))
				timingCtx.AddDetails("hit", len(cachedItems))
				timingCtx.AddDetails("already-locked", len(alreadyLocked))
				timingCtx.AddDetails("miss", len(lockedItems))
			}

			deserializeAllCachedResults(cachedItems, functionConfig)

			if doTiming {
				deserializeComplete()
			}
			return composeResults(cachedItems)
		}

		// If refreshEntireBatch is true, refresh all items in batch
		if funcOpts.RefreshEntireBatch {
			var refreshComplete timing.Complete
			if doTiming {
				_, refreshComplete = timing.Start(ctx, "RefreshAllInBatch")
				timingCtx.AddDetails("complete-refresh", true)
				timingCtx.AddDetails("hit", len(cachedItems))
				timingCtx.AddDetails("already-locked", len(alreadyLocked))
				timingCtx.AddDetails("miss", len(lockedItems))
			}
			refreshAllInBatch(ctx, f, items, functionConfig, funcOpts)
			if doTiming {
				refreshComplete()
			}
			return composeResults(items)
		}

		var parallelComplete timing.Complete
		var parallelContext *timing.Context
		if doTiming {
			timingCtx.AddDetails("hit", len(cachedItems))
			timingCtx.AddDetails("already-locked", len(alreadyLocked))
			timingCtx.AddDetails("miss", len(lockedItems))

			parallelContext, parallelComplete = timing.Start(ctx, "Parallel")
			parallelContext.Async = true
			ctx = parallelContext
		}

		var wg sync.WaitGroup

		if len(cachedItems) > 0 {
			go func() {
				defer wg.Done()
				handleCachedItems(ctx, cachedItems, functionConfig, funcOpts)
			}()
			wg.Add(1)
		}

		if len(lockedItems) > 0 {
			go func() {
				defer wg.Done()
				handleUncachedItems(ctx, lockedItems, f, funcOpts, functionConfig)
			}()
			wg.Add(1)
		}

		if len(alreadyLocked) > 0 {
			go func() {
				defer wg.Done()
				handleAlreadyLockedItems(ctx, funcOpts, functionConfig, f, alreadyLocked)
			}()
			wg.Add(1)
		}

		// Wait for all goroutines to complete or context to timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		if doTiming {
			parallelComplete()
		}

		return composeResults(items)
	}
}

func composeResults[IN, OUT any](items []*keyStatus[IN, OUT]) ([]BulkReturn[OUT], error) {
	// Sort the items by index
	sort.Slice(items, func(i, j int) bool {
		return items[i].index < items[j].index
	})
	results := make([]BulkReturn[OUT], len(items))
	for i, item := range items {
		if item.index != i {
			return nil, fmt.Errorf("expected index %d, got %d", i, item.index)
		}
		results[item.index] = BulkReturn[OUT]{Result: item.resultVal, Error: item.resultErr}
	}
	return results, nil
}

func refreshAllInBatch[IN any, OUT any](ctx context.Context, f CtxSliceFunc[IN, OUT], items []*keyStatus[IN, OUT], functionConfig cacheFunctionConfig, funcOpts CacheOptions) {
	in := make([]IN, len(items))
	for i, item := range items {
		in[i] = item.input
	}

	outs, err := f(ctx, in)
	if len(outs) != len(items) {
		err = fmt.Errorf("expected %d results, got %d", len(items), len(outs))
	}
	if err != nil {
		// Unlock everything that we locked
		for _, item := range items {
			if item.status == LockStatusLockAcquired {
				unlockIfNeeded(ctx, functionConfig.cache, item)
			}
			item.resultErr = err
		}
		return
	}
	// Save all the results to the cache
	for i, item := range items {
		key := item.key
		cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(outs[i])}, functionConfig.outputValueHandlers)
		if err != nil {
			log.Printf("Error serializing value: %v", err)
			unlockIfNeeded(ctx, functionConfig.cache, item)
		} else {
			err = functionConfig.cache.saveToCache(ctx, key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, functionConfig.cache, item)
			}
		}
		item.resultVal = outs[i]
	}
}

func deserializeAllCachedResults[IN any, OUT any](cachedItems []*keyStatus[IN, OUT], functionConfig cacheFunctionConfig) {
	for _, item := range cachedItems {
		toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
		item.resultVal = toResults[0].Interface().(OUT)
		item.resultErr = err
	}
}

func handleUncachedItems[IN any, OUT any](ctx context.Context, uncachedItems []*keyStatus[IN, OUT], f CtxSliceFunc[IN, OUT], funcOpts CacheOptions, functionConfig cacheFunctionConfig) {
	if funcOpts.EnableTiming {
		var functionTimingCtx *timing.Context
		var functionComplete timing.Complete
		functionTimingCtx, functionComplete = timing.Start(ctx, "RunFunction")
		ctx = functionTimingCtx
		defer functionComplete()
	}

	uncachedInputs := make([]IN, len(uncachedItems))
	for i, item := range uncachedItems {
		uncachedInputs[i] = item.input
	}
	uncachedResults, err := f(ctx, uncachedInputs)
	if len(uncachedResults) != len(uncachedInputs) {
		err = fmt.Errorf("expected %d results, got %d", len(uncachedInputs), len(uncachedResults))
		for _, item := range uncachedItems {
			item.resultErr = err
		}
		return
	}
	for i, uncachedItem := range uncachedItems {
		uncachedItem.resultVal = uncachedResults[i]
		uncachedItem.resultErr = err

		if err == nil {
			// Save the result to the cache
			cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(uncachedItem.resultVal)}, functionConfig.outputValueHandlers)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, functionConfig.cache, uncachedItem)
			} else {
				err = functionConfig.cache.saveToCache(ctx, uncachedItem.key, cacheVal, funcOpts)
				if err != nil {
					log.Printf("Error setting cache: %v", err)
					unlockIfNeeded(ctx, functionConfig.cache, uncachedItem)
				}
			}
		} else {
			unlockIfNeeded(ctx, functionConfig.cache, uncachedItem)
		}
	}
}

func handleAlreadyLockedItems[IN any, OUT any](ctx context.Context, funcOpts CacheOptions, functionConfig cacheFunctionConfig, f CtxSliceFunc[IN, OUT], lockedItems []*keyStatus[IN, OUT]) {
	if funcOpts.EnableTiming {
		var lockedTimingCtx *timing.Context
		var complete timing.Complete
		lockedTimingCtx, complete = timing.Start(ctx, "LockedWaiting")
		lockedTimingCtx.Async = true
		ctx = lockedTimingCtx
		defer complete()
	}

	lockedResults := parallelRun(ctx, lockedItems, func(ctx context.Context, item *keyStatus[IN, OUT]) (BulkReturn[OUT], error) {
		value, status, err := functionConfig.cache.getCachedValueOrLock(ctx, item.key, funcOpts, funcOpts.EnableTiming, false)
		item.status = status
		if len(value) > 0 {
			toResults, _, err := deserializeCacheToResults(value, functionConfig.outputValueHandlers)
			return BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}, nil
		}
		// At this point we have a couple of possibilities:
		// * The lock was acquired and the value was not in the cache, so we need to compute it
		inSlice := []IN{item.input}
		functionCtx := ctx
		var functionComplete timing.Complete
		if funcOpts.EnableTiming {
			var functionTimingCtx *timing.Context
			functionTimingCtx, functionComplete = timing.Start(ctx, "RunFunction")
			functionCtx = functionTimingCtx
		}
		singleResult, err := f(functionCtx, inSlice)
		if funcOpts.EnableTiming {
			functionComplete()
		}
		if len(singleResult) != 1 {
			err = fmt.Errorf("expected 1 result, got %d", len(singleResult))
		}
		if err == nil {
			// Save to cache
			cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(singleResult[0])}, functionConfig.outputValueHandlers)
			if err != nil {
				log.Printf("Error serializing to cache: %v", err)
				unlockIfNeeded(ctx, functionConfig.cache, item)
			} else {
				err = functionConfig.cache.saveToCache(ctx, item.key, cacheVal, funcOpts)
				if err != nil {
					log.Printf("Error setting cache: %v", err)
					unlockIfNeeded(ctx, functionConfig.cache, item)
				}
			}
		}
		return BulkReturn[OUT]{Result: singleResult[0], Error: err}, nil
	})
	// Now deserialize the locked items
	for i, item := range lockedItems {
		item.resultVal = lockedResults[i].Result.Result
		item.resultErr = lockedResults[i].Error
	}
}

// unlockIfNeeded unlocks the cache for the given key if the lock was acquired.
//
// Parameters:
//   - ctx: context.Context - The context for the operation.
//   - c: *RedisCache - The Redis cache instance.
//   - item: *keyStatus - The key status containing the key to unlock.
func unlockIfNeeded[IN, OUT any](ctx context.Context, c *RedisCache, item *keyStatus[IN, OUT]) {
	if item.status == LockStatusLockAcquired {
		// Unlock the cache
		err := c.unlockCache(ctx, item.key)
		if err != nil {
			log.Printf("Error unlocking cache: %v", err)
		}
	}
}

// handleCachedItems processes the cached items and deserializes their cached values into results.
//
// Parameters:
//   - cachedItems: []*keyStatus - A slice of keyStatus pointers representing the cached items.
//   - functionConfig: cacheFunctionConfig - The configuration for the cache function.
//   - results: []BulkReturn[OUT] - A slice to store the deserialized results and any errors.
func handleCachedItems[IN, OUT any](ctx context.Context, cachedItems []*keyStatus[IN, OUT], functionConfig cacheFunctionConfig, funcOpts CacheOptions) {
	if funcOpts.EnableTiming {
		_, complete := timing.Start(ctx, "Deserialize")
		defer complete()
	}
	// Iterate over each cached item
	for _, item := range cachedItems {
		// Deserialize the cached value into results
		toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
		item.resultVal = toResults[0].Interface().(OUT)
		item.resultErr = err
	}
}
