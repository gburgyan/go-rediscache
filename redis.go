package rediscache

import (
	"context"
	"errors"
	"fmt"
	"github.com/gburgyan/go-timing"
	"github.com/go-redis/redis/v8"
	"reflect"
	"sync"
	"time"
)

// RedisCache is a wrapper around a Redis client that provides caching functionality with various configuration options.
type RedisCache struct {
	defaultContext    context.Context
	connection        *redis.Client
	typeHandlers      map[reflect.Type]valueHandler
	interfaceHandlers map[reflect.Type]valueHandler
	opts              CacheOptions

	mu             sync.Mutex
	contentWaiters map[string][]chan []byte
}

type LockWaitExpiredError struct {
	message string
}

func (e *LockWaitExpiredError) Error() string {
	return e.message
}

// LockStatus represents the status of a lock operation.
type LockStatus int

const (
	// LockStatusUnlocked indicates that the lock is not held.
	LockStatusUnlocked LockStatus = iota

	// LockStatusLockAcquired indicates that the lock was successfully acquired.
	LockStatusLockAcquired

	// LockStatusLockFailed indicates that the lock acquisition failed.
	LockStatusLockFailed

	// LockStatusError indicates that an error occurred during the lock operation.
	LockStatusError
)

// LockMode represents the mode of locking behavior.
type LockMode int

const (
	// LockModeDefault is the default locking mode.
	LockModeDefault LockMode = iota

	// LockModeSkipSpinning skips the spinning phase of the lock.
	LockModeSkipSpinning

	// LockModeSkipInitial skips the initial lock attempt.
	LockModeSkipInitial
)

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
func (r *RedisCache) getCachedValueOrLock(ctx context.Context, key string, opts CacheOptions, doTiming bool, mode LockMode) (value []byte, locked LockStatus, err error) {
	var timingCtx *timing.Context
	if doTiming {
		var complete timing.Complete
		timingCtx, complete = timing.Start(ctx, "redis")
		defer complete()
	}

	waiterChannel := make(chan []byte)
	defer close(waiterChannel)
	unregisterWaiter := r.registerWaiterChannel(key, waiterChannel)
	defer unregisterWaiter()

	lockWaitExpire := time.After(opts.LockWait)
	spins := 0
	for {
		if mode != LockModeSkipInitial {
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
					return val, LockStatusUnlocked, nil
				}
			} else {
				if !errors.Is(err, redis.Nil) {
					if doTiming {
						timingCtx.AddDetails("cache-error", true)
					}
					return nil, LockStatusError, err
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
					return nil, LockStatusLockAcquired, nil
				}
				// In case there is an error while setting the lock, this is
				// likely due to a race with another process. Retry.
			}
		}
		if mode == LockModeSkipSpinning {
			return nil, LockStatusLockFailed, nil
		}
		if mode == LockModeSkipInitial {
			mode = LockModeDefault
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
			return nil, LockStatusLockFailed, ctx.Err()
		case <-lockWaitExpire:
			if doTiming {
				timingCtx.AddDetails("lock-wait-expired", true)
			}
			return nil, LockStatusLockFailed, &LockWaitExpiredError{}
		case val := <-waiterChannel:
			if doTiming {
				timingCtx.AddDetails("in-proc-val", true)
			}
			if len(val) > 0 {
				return val, LockStatusUnlocked, nil
			}
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
func (r *RedisCache) saveToCache(ctx context.Context, key string, value []byte, opts CacheOptions) error {
	r.notifyWaiters(key, value)
	set := r.connection.Set(ctx, key, value, opts.TTL)
	if set.Err() != nil {
		return fmt.Errorf("failed to save value to cache: %w", set.Err())
	}
	return nil
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

// registerWaiterChannel registers a channel to wait for a specific cache key and returns a function to unregister the channel.
//
// Parameters:
// - key: The cache key to wait for.
// - ch: The channel to register.
//
// Returns:
// - A function to unregister the channel.
func (r *RedisCache) registerWaiterChannel(key string, ch chan []byte) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.contentWaiters == nil {
		r.contentWaiters = make(map[string][]chan []byte)
	}
	r.contentWaiters[key] = append(r.contentWaiters[key], ch)

	// return unlocker function
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if chs, ok := r.contentWaiters[key]; ok {
			for i, c := range chs {
				if c == ch {
					r.contentWaiters[key] = append(chs[:i], chs[i+1:]...)
					break
				}
			}
			if len(r.contentWaiters[key]) == 0 {
				delete(r.contentWaiters, key)
			}
		}
	}
}

// notifyWaiters sends the given value to all registered channels waiting for the specified cache key
// and then removes the channels from the waiters list.
//
// Parameters:
// - key: The cache key for which the waiters are registered.
// - value: The byte slice to send to the registered channels.
func (r *RedisCache) notifyWaiters(key string, value []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if chs, ok := r.contentWaiters[key]; ok {
		for _, ch := range chs {
			ch <- value
		}
		delete(r.contentWaiters, key)
	}
}
