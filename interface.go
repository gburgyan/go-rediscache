package go_rediscache

import (
	"context"
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
	Deserialize([]byte, *any) error
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
	connection redis.Conn
}

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
	for i := 0; i < t.NumIn(); i++ {
		in = append(in, t.In(i))
	}
	for i := 0; i < t.NumOut(); i++ {
		out = append(out, t.Out(i))
	}

	r.validateInputParams(in)
	r.validateOutputParams(out)

	//retTypeInstance := reflect.New(t.Out(0).Elem()).Interface()

	// Use reflection to create a new function that wraps f and caches its result
	cft := reflect.FuncOf(in, out, false)
	cf := reflect.MakeFunc(cft, func(args []reflect.Value) []reflect.Value {
		r.keyForArgs(args, out)

		// Look up key in cache
		// If found, return the value
		// If not found, call f and cache the result

		// Lock the cache in Redis to prevent multiple instances of the same function from running

		results := realFunction.Call(args)

		// Extract the return value from the results/error
		var resultValue reflect.Value
		for _, result := range results {
			if result.Type() == errorType {
				if !result.IsNil() {
					// Return the error
					return results
				}
			}
			if result.Type().Implements(serializableType) {
				resultValue = result
				break
			}
			panic("invalid return type")
		}

		// Serialize the value
		serVal := resultValue.Interface().(Serializable)
		serialized, err := serVal.Serialize()
		if err != nil {
			// Unlock the cache

			// Return the error
			// TODO: if there is an error result slot, set it to the error, otherwise panic.
			return results
		}

		// Store the serialized value in the cache
		fmt.Printf("serialized: %v\n", serialized)

		// Return the value

		return results
	})
	return cf.Interface()
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

func (r *RedisCache) validateOutputParams(out []reflect.Type) {
	// f's return type should be a pointer to a Serializable
	if len(out) != 2 {
		panic("f should have exactly 2 return values")
	}
	if !out[0].Implements(serializableType) {
		panic("invalid return type")
	}
}

func (r *RedisCache) keyForArgs(args []reflect.Value, out []reflect.Type) {
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
	key := keyBuilder.String()
	fmt.Printf("key: %s\n", key)
}

func Cache1[K1 any, T Serializable](c *RedisCache, getter CacheGetter[K1, T]) CacheGetter[K1, T] {
	ret := c.Cached(getter)
	//reflect.TypeOf(ret).ConvertibleTo(reflect.TypeOf(getter))
	return ret.(func(context.Context, K1) (T, error))
}

func Cache2[K1 any, K2 any, T Serializable](c *RedisCache, getter CacheGetter2[K1, K2, T]) CacheGetter2[K1, K2, T] {
	return c.Cached(getter).(CacheGetter2[K1, K2, T])
}
