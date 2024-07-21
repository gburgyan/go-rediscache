package go_rediscache

import (
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"reflect"
	"time"
)

func (r *RedisCache) getCachedValueOrLock(ctx context.Context, key string, opts CacheOptions) (value []byte, locked bool, err error) {
	lockWaitExpire := time.After(opts.LockWait)
	for {
		// Attempt to get the value from the cache
		val, err := r.connection.Get(ctx, key).Bytes()
		if err == nil {
			if len(val) > 0 {
				return val, false, nil
			} else {
				// The key is locked, wait for the lock to be released
				select {
				case <-ctx.Done():
					return nil, false, ctx.Err()
				case <-lockWaitExpire:
					return nil, false, errors.New("lock wait expired")
				case <-time.After(opts.LockRetry):
					continue
				}
			}
		}
		if !errors.Is(err, redis.Nil) {
			return nil, false, err
		}
		// The key does not exist in the cache, attempt to lock
		ok, err := r.connection.SetNX(ctx, key, "", opts.LockTTL).Result()
		if ok && err == nil {
			// Lock successfully acquired
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}

		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-lockWaitExpire:
			return nil, false, errors.New("lock wait expired")
		case <-time.After(opts.LockRetry):
		}
	}
}

func (r *RedisCache) unlockCache(ctx context.Context, key string) error {
	// Start the transaction
	_, err := r.connection.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		// Get the value of the key
		val, err := pipe.Get(ctx, key).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}

		// Check if the key exists and is a 0-byte value
		if val != "" {
			// A valid value exists, do not delete the key
			return nil
		}

		// Delete the key
		pipe.Del(ctx, key)
		return nil
	})

	return err
}

func (r *RedisCache) serializeResultsToCache(results []reflect.Value, out []reflect.Type) ([]byte, error) {
	parts := make([][]byte, len(results))
	for i := 0; i < len(results); i++ {
		if out[i].Implements(serializableType) {
			serialized, err := results[i].Interface().(Serializable).Serialize()
			if err != nil {
				return nil, err
			}
			parts[i] = serialized
		}
	}
	return serialize(parts)
}

func (r *RedisCache) deserializeCacheToResults(value []byte, out []outputValueHandler) ([]reflect.Value, error) {
	parts, err := deserialize(value)
	if err != nil {
		return nil, err
	}
	if len(parts) != len(out) {
		return nil, errors.New("invalid number of parts")
	}
	results := make([]reflect.Value, len(parts))
	for i := 0; i < len(parts); i++ {
		desVal, err := out[i].deserializer(parts[i])
		if err != nil {
			return nil, err
		}
		if reflect.TypeOf(desVal) == valueType {
			results[i] = desVal.(reflect.Value)
		} else {
			results[i] = reflect.ValueOf(desVal)
		}
	}
	return results, nil
}

func (r *RedisCache) saveToCache(ctx context.Context, key string, value []byte, opts CacheOptions) {
	set := r.connection.Set(ctx, key, value, opts.TTL)
	if set.Err() != nil {
		panic(set.Err())
	}
}
