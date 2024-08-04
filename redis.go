package go_rediscache

import (
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"time"
)

type LockWaitExpiredError struct {
	message string
}

func (e *LockWaitExpiredError) Error() string {
	return e.message
}

func (r *RedisCache) getCachedValueOrLock(ctx context.Context, key string, opts CacheOptions) (value []byte, locked bool, err error) {
	lockWaitExpire := time.After(opts.LockWait)
	for {
		// Attempt to get the value from the cache
		val, err := r.connection.Get(ctx, key).Bytes()
		if err == nil {
			if len(val) > 0 {
				return val, false, nil
			}
		} else {
			if !errors.Is(err, redis.Nil) {
				return nil, false, err
			}
			// The key does not exist in the cache, attempt to lock
			ok, err := r.connection.SetNX(ctx, key, "", opts.LockTTL).Result()
			if ok && err == nil {
				// Lock successfully acquired
				return nil, true, nil
			}
			// In case there is an error while setting the lock, this is
			// likely due to a race with another process. Retry.
		}

		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-lockWaitExpire:
			return nil, false, &LockWaitExpiredError{}
		case <-time.After(opts.LockRetry):
			continue
		}
	}
}

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

func (r *RedisCache) saveToCache(ctx context.Context, key string, value []byte, opts CacheOptions) {
	set := r.connection.Set(ctx, key, value, opts.TTL)
	if set.Err() != nil {
		panic(set.Err())
	}
}
