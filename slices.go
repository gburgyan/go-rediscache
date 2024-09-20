package rediscache

import (
	"context"
	"log"
	"reflect"
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
		return parallelRun(ctx, cf, in)
	}
}

type keyStatus struct {
	index       int
	key         string
	cachedValue []byte
	status      LockStatus
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
func CacheBulkSlice[IN any, OUT any](c *RedisCache, f CtxSliceFunc[IN, OUT], funcOpts CacheOptions, refreshEntireBatch bool) func(ctx context.Context, in []IN) []BulkReturn[OUT] {
	// Setup the cache function configuration
	functionConfig := c.setupCacheFunctionConfig(func(IN) (OUT, error) { panic("not to be called") }, funcOpts)

	return func(ctx context.Context, in []IN) []BulkReturn[OUT] {
		doTiming := funcOpts.EnableTiming

		// Run the parallel function to get key statuses
		keyStatuses := parallelRun(ctx, func(ctx context.Context, input IN) (keyStatus, error) {
			key := functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(input)})
			value, locked, err := c.getCachedValueOrLock(ctx, key, funcOpts, doTiming, true)
			return keyStatus{key: key, cachedValue: value, status: locked}, err
		}, in)

		var items, cachedItems, uncachedItems, lockedItems, alreadyLockedItems []*keyStatus
		results := make([]BulkReturn[OUT], len(in))

		// Categorize key statuses
		for i, item := range keyStatuses {
			status := item.Result
			status.index = i
			items = append(items, &status)
			switch {
			case item.Error != nil || status.status == LockStatusError:
				uncachedItems = append(uncachedItems, &status)
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
			deserializeAllCachedResults(cachedItems, functionConfig, results)
			return results
		}

		// If refreshEntireBatch is true, refresh all items in batch
		if refreshEntireBatch {
			refreshAllInBatch(ctx, in, f, items, c, functionConfig, funcOpts, results)
			return results
		}

		var wg sync.WaitGroup
		wg.Add(3)

		// Handle cached items in a separate goroutine
		go func() {
			defer wg.Done()
			handleCachedItems(cachedItems, functionConfig, results)
		}()

		// Handle locked items in a separate goroutine
		go func() {
			defer wg.Done()
			handleLockedItems(ctx, in, c, funcOpts, doTiming, functionConfig, f, lockedItems, results)
		}()

		// Handle uncached items in a separate goroutine
		go func() {
			defer wg.Done()
			handleUncachedItems(ctx, in, uncachedItems, f, results, funcOpts, functionConfig, c)
		}()

		// Wait for all goroutines to complete or context to timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			log.Println("Operation timed out")
		}

		return results
	}
}

func refreshAllInBatch[IN any, OUT any](ctx context.Context, in []IN, f CtxSliceFunc[IN, OUT], items []*keyStatus, c *RedisCache, functionConfig cacheFunctionConfig, funcOpts CacheOptions, results []BulkReturn[OUT]) {
	outs, err := f(ctx, in)
	if err != nil {
		// Unlock everything that we locked
		for _, item := range items {
			if item.status == LockStatusLockAcquired {
				unlockIfNeeded(ctx, c, item)
			}
			results[item.index] = BulkReturn[OUT]{Error: err}
		}
	}
	// Save all the results to the cache
	for i, item := range items {
		key := item.key
		cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(outs[i])}, functionConfig.outputValueHandlers)
		if err != nil {
			log.Printf("Error serializing value: %v", err)
			unlockIfNeeded(ctx, c, item)
		} else {
			err = c.saveToCache(ctx, key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, c, item)
			}
		}
		results[i] = BulkReturn[OUT]{Result: outs[i], Error: nil}
	}
}

func deserializeAllCachedResults[OUT any](cachedItems []*keyStatus, functionConfig cacheFunctionConfig, results []BulkReturn[OUT]) {
	for i, item := range cachedItems {
		toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
		results[i] = BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}
	}
}

func handleUncachedItems[IN any, OUT any](ctx context.Context, in []IN, uncachedItems []*keyStatus, f CtxSliceFunc[IN, OUT], results []BulkReturn[OUT], funcOpts CacheOptions, functionConfig cacheFunctionConfig, c *RedisCache) {
	uncachedInputs := make([]IN, len(uncachedItems))
	for i, item := range uncachedItems {
		uncachedInputs[i] = in[item.index]
	}
	uncachedResults, err := f(ctx, uncachedInputs)
	for i, uncachedItem := range uncachedItems {
		results[uncachedItem.index] = BulkReturn[OUT]{Result: uncachedResults[i], Error: err}
		// Save the result to the cache
		cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(uncachedResults[i])}, functionConfig.outputValueHandlers)
		if err != nil {
			log.Printf("Error setting cache: %v", err)
			unlockIfNeeded(ctx, c, uncachedItem)
		} else {
			err = c.saveToCache(ctx, uncachedItem.key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, c, uncachedItem)
			}
		}
	}
}

func handleLockedItems[IN any, OUT any](ctx context.Context, in []IN, c *RedisCache, funcOpts CacheOptions, doTiming bool, functionConfig cacheFunctionConfig, f CtxSliceFunc[IN, OUT], lockedItems []*keyStatus, results []BulkReturn[OUT]) {
	lockedResults := parallelRun(ctx, func(ctx context.Context, item *keyStatus) (BulkReturn[OUT], error) {
		value, status, err := c.getCachedValueOrLock(ctx, item.key, funcOpts, doTiming, false)
		item.status = status
		if len(value) > 0 {
			toResults, _, err := deserializeCacheToResults(value, functionConfig.outputValueHandlers)
			return BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}, nil
		}
		// At this point we have a couple of possibilities:
		// * The lock was acquired and the value was not in the cache, so we need to compute it
		inSlice := []IN{in[item.index]}
		singleResult, err := f(ctx, inSlice)
		if err != nil {
			// Save to cache
			cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(singleResult[0])}, functionConfig.outputValueHandlers)
			if err != nil {
				log.Printf("Error serializing to cache: %v", err)
				unlockIfNeeded(ctx, c, item)
			} else {
				err = c.saveToCache(ctx, item.key, cacheVal, funcOpts)
				if err != nil {
					log.Printf("Error setting cache: %v", err)
					unlockIfNeeded(ctx, c, item)
				}
			}
		}
		return BulkReturn[OUT]{Result: singleResult[0], Error: err}, nil
	}, lockedItems)
	// Now deserialize the locked items
	for i, item := range lockedItems {
		results[item.index] = lockedResults[i].Result
		results[item.index].Error = lockedResults[i].Error
	}
}

// unlockIfNeeded unlocks the cache for the given key if the lock was acquired.
//
// Parameters:
//   - ctx: context.Context - The context for the operation.
//   - c: *RedisCache - The Redis cache instance.
//   - item: *keyStatus - The key status containing the key to unlock.
func unlockIfNeeded(ctx context.Context, c *RedisCache, item *keyStatus) {
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
func handleCachedItems[OUT any](cachedItems []*keyStatus, functionConfig cacheFunctionConfig, results []BulkReturn[OUT]) {
	// Iterate over each cached item
	for _, item := range cachedItems {
		// Deserialize the cached value into results
		toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
		// Store the result and any error in the results slice
		results[item.index] = BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}
	}
}
