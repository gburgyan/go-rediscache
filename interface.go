package go_rediscache

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"reflect"
	"strings"
	"time"
)

// Keyable is an interface that can be implemented by a
// dependency to provide a unique key that can be used to cache the
// result of the dependency. Implementing this interface is required
// if you want to use the Cached() function.
type Keyable interface {
	// CacheKey returns a key that can be used to cache the result of a
	// dependency. The key must be unique for the given dependency.
	// The intent is that the results of calling generators based on the
	// value represented by this key will be invariant if the key is
	// the same.
	CacheKey() string
}

type Serializable interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) (any, error)
}

type CacheOptions struct {
	// TTL is the time-to-live for the cache entry. If TTL is 0, the cache
	// entry will never expire.
	TTL time.Duration

	// LockTTL is the time-to-live for the lock on the cache entry. If LockTTL
	// is 0, the lock will never expire. This controls how long the called function
	// is allowed to run before the lock expires.
	LockTTL time.Duration

	// LockWait is the maximum time to wait for a lock on the cache entry.
	LockWait time.Duration

	// LockRetry is the time to wait before retrying to acquire a lock on the
	// cache entry.
	LockRetry time.Duration
}

var serializableType = reflect.TypeOf((*Serializable)(nil)).Elem()
var keyableType = reflect.TypeOf((*Keyable)(nil)).Elem()
var stringerType = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
var stringType = reflect.TypeOf((*string)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

type RedisCache struct {
	connection     *redis.Conn
	defaultContext context.Context
}

type cacheWriteback func(context.Context, string, Serializable) error

type CacheGetter[K any, T Serializable] func(context.Context, K) (T, error)
type CacheGetter2[K1 any, K2 any, T Serializable] func(context.Context, K1, K2) (T, error)

func (r *RedisCache) Cached(f any) any {
	// f should be a function
	t := reflect.TypeOf(f)
	if t.Kind() != reflect.Func {
		panic("f should be a function")
	}
	realFunction := reflect.ValueOf(f)

	in := []reflect.Type{}
	out := []reflect.Type{}
	contextIndex := -1
	for i := 0; i < t.NumIn(); i++ {
		in = append(in, t.In(i))
		if t.In(i) == contextType {
			contextIndex = i
		}
	}
	for i := 0; i < t.NumOut(); i++ {
		out = append(out, t.Out(i))
	}

	r.validateInputParams(in)
	serializables := r.validateOutputParams(out)

	returnTypeKey := makeReturnTypeKey(out)

	//retTypeInstance := reflect.New(t.Out(0).Elem()).Interface().(Serializable)

	// Use reflection to create a new function that wraps f and caches its result
	cft := reflect.FuncOf(in, out, false)
	cf := reflect.MakeFunc(cft, func(args []reflect.Value) []reflect.Value {
		var ctx context.Context
		if contextIndex != -1 {
			ctx = args[contextIndex].Interface().(context.Context)
		} else {
			ctx = r.defaultContext
		}

		key := r.keyForArgs(args, returnTypeKey)

		// Look up key in cache
		cachedValue, _, err2 := r.getCachedValueOrLock(ctx, key)
		if err2 != nil {
		}
		// If found, return the value
		if cachedValue != nil {
			// Deserialize the value
			results, err := r.deserializeCacheToResults(cachedValue, serializables)
			if err == nil {
				fmt.Println("Cache hit!")
				return results
			}
			// If we got an error deserializing, we can still
			// call the main function and cache the result if
			// it succeeds.
		}

		fmt.Println("Cache miss!")

		// If not found, call f and cache the result
		results := realFunction.Call(args)

		// Extract the return value from the results/error
		for _, result := range results {
			if result.Type() == errorType {
				if !result.IsNil() {
					// Return the error
					return results
				}
			}
		}

		// Serialize the value
		serialized, err := r.serializeResultsToCache(results, out)
		if err != nil {
			// unlock the cache
			r.unlockCache(ctx, key)
			// If there is an error, log it
			return results
		}

		// Store the serialized value in the cache
		fmt.Printf("serialized: %v\n", serialized)

		cacheValue, err := r.serializeResultsToCache(results, out)
		r.saveToCache(ctx, key, cacheValue)
		// Return the value

		return results
	})
	return cf.Interface()
}

// makeReturnTypeKey creates a unique key for the return type of a function. Any
// error type is ignored. The key is a string representation of the return types.
func makeReturnTypeKey(out []reflect.Type) string {
	keyBuilder := strings.Builder{}
	for i := 0; i < len(out); i++ {
		if out[i] == errorType {
			continue
		}
		if keyBuilder.Len() > 0 {
			keyBuilder.WriteString(":")
		}
		keyBuilder.WriteString(out[i].String())
	}
	return keyBuilder.String()
}

func (r *RedisCache) validateInputParams(inputs []reflect.Type) {
	// f should have 0 or 1 context argument, and 1 or more arguments that are Keyable. The last argument should be a pointer to a Serializable.
	if len(inputs) < 2 {
		panic("f should have at least 1 argument")
	}
	for i := 0; i < len(inputs); i++ {
		if i == 0 {
			if inputs[i] == contextType {
				continue
			}
		}
		if inputs[i].Implements(keyableType) {
			continue
		}
		if inputs[i].Implements(stringerType) {
			continue
		}
		if inputs[i] == stringType {
			continue
		}
		panic("invalid argument type")
	}
}

func (r *RedisCache) validateOutputParams(out []reflect.Type) []Serializable {
	// The function should return 1 or more serializable types, and an optional error type.
	// For the serializable types, also make a new instance of each type to allow for serialization.
	if len(out) == 0 {
		panic("f should return at least 1 value")
	}
	serializables := make([]Serializable, len(out))
	errorCount := 0
	for i := 0; i < len(out); i++ {
		if out[i].Implements(serializableType) {
			serializables[i] = reflect.New(out[i]).Interface().(Serializable)
			continue
		}
		if out[i] == errorType {
			errorCount++
			if errorCount > 1 {
				panic("f should return at most 1 error")
			}
			continue
		}
		panic("invalid return type")
	}
	return serializables
}

func (r *RedisCache) keyForArgs(args []reflect.Value, returnTypes string) string {
	keyBuilder := strings.Builder{}
	for i := 0; i < len(args); i++ {
		if i == 0 && args[i].Type() == contextType {
			continue
		}
		if keyBuilder.Len() > 0 {
			keyBuilder.WriteString(":")
		}
		if args[i].Type().Implements(stringerType) {
			keyBuilder.WriteString(args[i].Interface().(fmt.Stringer).String())
			continue
		}
		if args[i].Type().Implements(keyableType) {
			keyBuilder.WriteString(args[i].Interface().(Keyable).CacheKey())
		}
		if args[i].Type() == stringType {
			keyBuilder.WriteString(args[i].Interface().(string))
			continue
		}
		panic("invalid argument type")
	}
	keyBuilder.WriteString("/")
	keyBuilder.WriteString(returnTypes)
	key := keyBuilder.String()
	fmt.Printf("key: %s\n", key)

	hash := sha256.Sum256([]byte(key))
	fmt.Printf("hash: %x\n", hash)

	// Return the hash as a string
	return fmt.Sprintf("%x", hash)
}

func Cache1[K1 any, T Serializable](c *RedisCache, getter CacheGetter[K1, T]) CacheGetter[K1, T] {
	ret := c.Cached(getter)
	//reflect.TypeOf(ret).ConvertibleTo(reflect.TypeOf(getter))
	return ret.(func(context.Context, K1) (T, error))
}

func Cache2[K1 any, K2 any, T Serializable](c *RedisCache, getter CacheGetter2[K1, K2, T]) CacheGetter2[K1, K2, T] {
	return c.Cached(getter).(CacheGetter2[K1, K2, T])
}

func (r *RedisCache) getCachedValueOrLock(ctx context.Context, key string) (value []byte, locked bool, err error) {
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
				case <-time.After(1 * time.Second):
					continue
				}
			}
		}
		if !errors.Is(err, redis.Nil) {
			return nil, false, err
		}
		// The key does not exist in the cache, attempt to lock
		ok, err := r.connection.SetNX(ctx, key, "", 30*time.Second).Result()
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
		case <-time.After(1 * time.Second):
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

func (r *RedisCache) deserializeCacheToResults(value []byte, out []Serializable) ([]reflect.Value, error) {
	parts, err := deserialize(value)
	if err != nil {
		return nil, err
	}
	if len(parts) != len(out) {
		return nil, errors.New("invalid number of parts")
	}
	results := make([]reflect.Value, len(parts))
	for i := 0; i < len(parts); i++ {
		if out[i] != nil {
			desVal, err := out[i].Deserialize(parts[i])
			if err != nil {
				return nil, err
			}
			results[i] = reflect.ValueOf(desVal)
		} else {
			results[i] = reflect.Zero(errorType)
		}
	}
	return results, nil
}

func (r *RedisCache) saveToCache(ctx context.Context, key string, value []byte) {
	set := r.connection.Set(ctx, key, value, time.Minute)
	if set.Err() != nil {
		panic(set.Err())
	}
}
