package rediscache

import (
	"context"
	"errors"
	"github.com/gburgyan/go-timing"
	"github.com/go-redis/redis/v8"
	"time"
)

type LockWaitExpiredError struct {
	message string
}

func (e *LockWaitExpiredError) Error() string {
	return e.message
}

// getCachedValueOrLock attempts to retrieve a value from the cache or acquire a lock if the value is not present.
//
// Parameters:
// - ctx: The context in which the operation is performed.
// - key: The cache key for the entry to be retrieved or locked.
// - opts: CacheOptions containing the configuration for caching behavior.
// - doTiming: A boolean indicating whether to measure the execution time of the operation.
//
// Returns:
// - value: A byte slice representing the cached value, or nil if the value is not present.
// - locked: A boolean indicating whether a lock was successfully acquired.
// - err: An error if the operation fails.
func (r *RedisCache) getCachedValueOrLock(ctx context.Context, key string, opts CacheOptions, doTiming bool) (value []byte, locked bool, err error) {
	var timingCtx *timing.Context
	if doTiming {
		var complete timing.Complete
		timingCtx, complete = timing.Start(ctx, "redis")
		defer complete()
	}

	lockWaitExpire := time.After(opts.LockWait)
	spins := 0
	for {
		// Attempt to get the value from the cache
		var getComplete timing.Complete
		if doTiming {
			_, getComplete = timing.Start(timingCtx, "get")
		}
		getResult := r.connection.Get(ctx, key)
		val, err := getResult.Bytes()
		if doTiming {
			getComplete()
		}

		if err == nil {
			if len(val) > 0 {
				if doTiming {
					timingCtx.AddDetails("cache-hit", true)
				}
				return val, false, nil
			}
		} else {
			if !errors.Is(err, redis.Nil) {
				if doTiming {
					timingCtx.AddDetails("cache-error", true)
				}
				return nil, false, err
			}
			// The key does not exist in the cache, attempt to lock
			if doTiming {
				timingCtx.AddDetails("cache-miss", true)
			}
			var lockComplete timing.Complete
			if doTiming {
				_, lockComplete = timing.Start(timingCtx, "lock")
			}
			ok, err := r.connection.SetNX(ctx, key, "", opts.LockTTL).Result()
			if doTiming {
				lockComplete()
			}
			if ok && err == nil {
				// Lock successfully acquired
				return nil, true, nil
			}
			// In case there is an error while setting the lock, this is
			// likely due to a race with another process. Retry.
		}

		spins++
		if doTiming {
			timingCtx.AddDetails("spins", spins)
		}

		select {
		case <-ctx.Done():
			if doTiming {
				timingCtx.AddDetails("context-done", true)
			}
			return nil, false, ctx.Err()
		case <-lockWaitExpire:
			if doTiming {
				timingCtx.AddDetails("lock-wait-expired", true)
			}
			return nil, false, &LockWaitExpiredError{}
		case <-time.After(opts.LockRetry):
			continue
		}
	}
}

// unlockCache attempts to unlock a cache entry by deleting the key if it is a 0-byte value.
//
// Parameters:
// - ctx: The context in which the operation is performed.
// - key: The cache key for the entry to be unlocked.
//
// Returns:
// - An error if the operation fails, or nil if the key is successfully unlocked or does not need unlocking.
func (r *RedisCache) unlockCache(ctx context.Context, key string) error {
	// Start the transaction

	err := r.connection.Watch(ctx, func(tx *redis.Tx) error {
		// Get the value of the key
		bytes, err := r.connection.Get(ctx, key).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return err
		}

		// Check if the key exists and is a 0-byte value
		if len(bytes) > 0 {
			// A valid value exists, do not delete the key
			return nil
		}

		// Delete the key
		r.connection.Del(ctx, key)
		return nil
	}, key)

	return err
}

// saveToCache stores the given value in the cache with the specified key and options.
//
// Parameters:
// - ctx: The context in which the operation is performed.
// - key: The cache key for the entry to be stored.
// - value: The byte slice representing the value to be cached.
// - opts: CacheOptions containing the configuration for caching behavior.
//
// Panics:
// - If the operation to set the value in the cache fails.
func (r *RedisCache) saveToCache(ctx context.Context, key string, value []byte, opts CacheOptions) {
	set := r.connection.Set(ctx, key, value, opts.TTL)
	if set.Err() != nil {
		panic(set.Err())
	}
}

// lockRefresh attempts to acquire a lock for refreshing the cache entry.
//
// Parameters:
// - ctx: The context in which the operation is performed.
// - key: The cache key for the entry to be locked.
// - opts: CacheOptions containing the configuration for caching behavior.
//
// Returns:
// - An error if the operation to acquire the lock fails.
func (r *RedisCache) lockRefresh(ctx context.Context, key string, opts CacheOptions) error {
	fullKey := key + "-refresh"
	_, err := r.connection.SetNX(ctx, fullKey, "", opts.LockTTL).Result()
	return err
}

// unlockRefresh releases the lock for refreshing the cache entry.
//
// Parameters:
// - ctx: The context in which the operation is performed.
// - key: The cache key for the entry to be unlocked.
// - opts: CacheOptions containing the configuration for caching behavior.
func (r *RedisCache) unlockRefresh(ctx context.Context, key string, opts CacheOptions) {
	fullKey := key + "-refresh"
	r.connection.Del(ctx, fullKey)
}
