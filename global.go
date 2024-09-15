package rediscache

import (
	"context"
	"log"
	"reflect"
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

func CacheBulkSlice[IN any, OUT any](c *RedisCache, f CtxSliceFunc[IN, OUT], funcOpts CacheOptions, refreshEntireBatch bool) func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
	baseFunc := func(in IN) (OUT, error) {
		panic("not to be called")
	}

	functionConfig := c.setupCacheFunctionConfig(baseFunc, funcOpts)

	return func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
		doTiming := funcOpts.EnableTiming

		keyStatuses := parallelRun(ctx, func(ctx context.Context, input IN) (keyStatus, error) {
			key := functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(input)})
			value, locked, err := c.getCachedValueOrLock(ctx, key, funcOpts, doTiming, true)
			return keyStatus{key: key, cachedValue: value, status: locked}, err
		}, in)

		items := make([]*keyStatus, len(in))
		var cachedItems []*keyStatus
		var uncachedItems []*keyStatus
		var lockedItems []*keyStatus
		var alreadyLockedItems []*keyStatus

		for i, item := range keyStatuses {
			status := item.Result
			status.index = i
			items[i] = &status
			if item.Error != nil || status.status == LockStatusError {
				uncachedItems = append(uncachedItems, &status)
			} else if len(status.cachedValue) > 0 {
				// This is a cached item and the lock status is unlocked
				cachedItems = append(cachedItems, &status)
			} else if status.status == LockStatusLockAcquired {
				lockedItems = append(lockedItems, &status)
			} else if status.status == LockStatusLockFailed {
				alreadyLockedItems = append(alreadyLockedItems, &status)
			} else {
				panic("unexpected lock status")
			}
		}

		// At this point, we have three sets of items:
		// 1. cachedItems: items that were found in the cache
		// 2. uncachedItems: items that were not found in the cache, likely due to an error (otherwise they would be in lockedItems)
		// 3. lockedItems: items that were locked and we have committed to refreshing
		// 4. alreadyLockedItems: items that were already locked by another process and are being computed by that process

		results := make([]BulkReturn[OUT], len(in))
		// Simple case: everything is cached
		if len(cachedItems) == len(in) {
			for i, item := range cachedItems {
				toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
				results[i] = BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}
			}
			return results, nil
		}

		// Another simple case: If we are refreshing the entire batch, we can just refresh everything at this point.
		if refreshEntireBatch {
			outs, err := f(ctx, in)
			if err != nil {
				// Unlock everything that we locked
				for _, item := range lockedItems {
					err := c.unlockCache(ctx, item.key)
					if err != nil {
						log.Printf("Error unlocking cache: %v", err)
					}
				}
				return nil, err
			}
			// Save all the results to the cache
			for i, out := range outs {
				key := functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(in[i])})
				cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(out)}, functionConfig.outputValueHandlers)
				c.saveToCache(ctx, key, cacheVal, funcOpts)
				if err != nil {
					log.Printf("Error setting cache: %v", err)
				}
				results[i] = BulkReturn[OUT]{Result: out, Error: nil}
			}
			// TODO: Deal with any locking issues
			return results, nil
		}

		// Here is where the fun begins. We have several things to deal with:
		// * For things that are cached, deserialize them
		// * For things that are locked, wait for them to be unlocked and then deserialize them.
		// * For things that are uncached, compute them and then save them to the cache. This has two sub-cases:
		//   * If we were able to lock the item we are computing, we can save it to the cache.
		//   * If we were not able to get the item or lock it (and it wasn't already locked) then we compute it and return it -- and no locking involved

		// First, deal with the cached items
		for _, item := range cachedItems {
			toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
			results[item.index] = BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}
		}

		// Next, deal with the locked items -- do the parallel run of waiting for the lock to be released
		lockedResults := parallelRun(ctx, func(ctx context.Context, item *keyStatus) (BulkReturn[OUT], error) {
			value, status, err := c.getCachedValueOrLock(ctx, item.key, funcOpts, doTiming, false)
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
					if status == LockStatusLockAcquired {
						// Unlock the cache
						err := c.unlockCache(ctx, item.key)
						if err != nil {
							log.Printf("Error unlocking cache: %v", err)
						}
					}
					return BulkReturn[OUT]{Result: singleResult[0], Error: nil}, nil
				}
				c.saveToCache(ctx, item.key, cacheVal, funcOpts)
			}
			return BulkReturn[OUT]{Result: singleResult[0], Error: err}, nil
		}, lockedItems)
		// Now deserialize the locked items
		for i, item := range lockedItems {
			results[item.index] = lockedResults[i].Result
			results[item.index].Error = lockedResults[i].Error
		}

		// Now, deal with the uncached items. Since the function takes a slice, we can just pass the slice of uncached items to the function.
		// Create a slice of the uncached items that we can pass to the function and call it.
		uncachedInputs := make([]IN, len(uncachedItems))
		for i, item := range uncachedItems {
			uncachedInputs[i] = in[item.index]
		}
		uncachedResults, err := f(ctx, uncachedInputs)
		for i, uncachedItem := range uncachedItems {
			results[uncachedItem.index] = BulkReturn[OUT]{Result: uncachedResults[i], Error: err}
			// Save the result to the cache
			cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(uncachedResults[i])}, functionConfig.outputValueHandlers)
			c.saveToCache(ctx, uncachedItem.key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
			}
		}

		return results, nil
	}
}
