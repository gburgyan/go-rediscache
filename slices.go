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

func CacheBulkSlice[IN any, OUT any](c *RedisCache, f CtxSliceFunc[IN, OUT], funcOpts CacheOptions, refreshEntireBatch bool) func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
	functionConfig := c.setupCacheFunctionConfig(func(IN) (OUT, error) { panic("not to be called") }, funcOpts)

	return func(ctx context.Context, in []IN) ([]BulkReturn[OUT], error) {
		doTiming := funcOpts.EnableTiming

		keyStatuses := parallelRun(ctx, func(ctx context.Context, input IN) (keyStatus, error) {
			key := functionConfig.keyForArgs([]reflect.Value{reflect.ValueOf(input)})
			value, locked, err := c.getCachedValueOrLock(ctx, key, funcOpts, doTiming, true)
			return keyStatus{key: key, cachedValue: value, status: locked}, err
		}, in)

		var items, cachedItems, uncachedItems, lockedItems, alreadyLockedItems []*keyStatus
		results := make([]BulkReturn[OUT], len(in))

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

		if len(cachedItems) == len(in) {
			deserializeAllCachedResults(cachedItems, functionConfig, results)
			return results, nil
		}

		if refreshEntireBatch {
			if err := refreshAllInBatch(ctx, in, f, items, c, functionConfig, funcOpts, results); err != nil {
				return nil, err
			}
			return results, nil
		}

		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			handleCachedItems(cachedItems, functionConfig, results)
		}()

		go func() {
			defer wg.Done()
			handleLockedItems(ctx, in, c, funcOpts, doTiming, functionConfig, f, lockedItems, results)
		}()

		go func() {
			defer wg.Done()
			handleUncachedItems(ctx, in, uncachedItems, f, results, funcOpts, functionConfig, c)
		}()

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

		return results, nil
	}
}

func refreshAllInBatch[IN any, OUT any](ctx context.Context, in []IN, f CtxSliceFunc[IN, OUT], items []*keyStatus, c *RedisCache, functionConfig cacheFunctionConfig, funcOpts CacheOptions, results []BulkReturn[OUT]) error {
	outs, err := f(ctx, in)
	if err != nil {
		// Unlock everything that we locked
		for _, item := range items {
			if item.status == LockStatusLockAcquired {
				unlockIfNeeded(ctx, item, LockStatusLockAcquired, c)
			}
		}
		return err
	}
	// Save all the results to the cache
	for i, item := range items {
		key := item.key
		cacheVal, err := serializeResultsToCache(funcOpts, []reflect.Value{reflect.ValueOf(outs[i])}, functionConfig.outputValueHandlers)
		if err != nil {
			log.Printf("Error serializing value: %v", err)
			unlockIfNeeded(ctx, item, LockStatusLockAcquired, c)
		} else {
			err = c.saveToCache(ctx, key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, item, LockStatusLockAcquired, c)
			}
		}
		results[i] = BulkReturn[OUT]{Result: outs[i], Error: nil}
	}
	return nil
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
		} else {
			err = c.saveToCache(ctx, uncachedItem.key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, uncachedItem, uncachedItem.status, c)
			}
		}
	}
}

func handleLockedItems[IN any, OUT any](ctx context.Context, in []IN, c *RedisCache, funcOpts CacheOptions, doTiming bool, functionConfig cacheFunctionConfig, f CtxSliceFunc[IN, OUT], lockedItems []*keyStatus, results []BulkReturn[OUT]) {
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
				unlockIfNeeded(ctx, item, status, c)
				return BulkReturn[OUT]{Result: singleResult[0], Error: nil}, nil
			}
			err = c.saveToCache(ctx, item.key, cacheVal, funcOpts)
			if err != nil {
				log.Printf("Error setting cache: %v", err)
				unlockIfNeeded(ctx, item, status, c)
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

func unlockIfNeeded(ctx context.Context, item *keyStatus, status LockStatus, c *RedisCache) {
	if status == LockStatusLockAcquired {
		// Unlock the cache
		err := c.unlockCache(ctx, item.key)
		if err != nil {
			log.Printf("Error unlocking cache: %v", err)
		}
	}
}

func handleCachedItems[OUT any](cachedItems []*keyStatus, functionConfig cacheFunctionConfig, results []BulkReturn[OUT]) {
	// First, deal with the cached items
	for _, item := range cachedItems {
		toResults, _, err := deserializeCacheToResults(item.cachedValue, functionConfig.outputValueHandlers)
		results[item.index] = BulkReturn[OUT]{Result: toResults[0].Interface().(OUT), Error: err}
	}
}
