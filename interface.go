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
	Deserialize([]byte, *any) error
}

var serializableType = reflect.TypeOf((*Serializable)(nil)).Elem()
var keyableType = reflect.TypeOf((*Keyable)(nil)).Elem()
var stringerType = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
var stringType = reflect.TypeOf((*string)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

type RedisCache struct {
	connection     redis.Conn
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
	r.validateOutputParams(out)

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
		r.getCachedValueOrLock(ctx, key)
		// If found, return the value
		// If not found, call f and cache the result

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

func (r *RedisCache) validateOutputParams(out []reflect.Type) {
	// f's return type should be a pointer to a Serializable
	if len(out) != 2 {
		panic("f should have exactly 2 return values")
	}
	if !out[0].Implements(serializableType) {
		panic("invalid return type")
	}
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
			if string(val) != "LOCKED" {
				return val, false, nil
			} else {
				// The key is locked, wait for the lock to be released
				time.Sleep(1 * time.Second)
				continue
			}
		}
		if !errors.Is(err, redis.Nil) {
			return nil, false, err
		}
		// The key does not exist in the cache, attempt to lock
		ok, err := r.connection.SetNX(ctx, key, "LOCKED", 30*time.Second).Result()
		if ok && err == nil {
			// Lock successfully acquired
			return nil, true, nil
		}

		time.Sleep(1 * time.Second)
	}
}
