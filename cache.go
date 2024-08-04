package go_rediscache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
)

func (r *RedisCache) Cached(f any) any {
	return r.CachedOpts(f, r.opts)
}

func (r *RedisCache) CachedOpts(f any, funcOpts CacheOptions) any {
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
	outputValueHandlers := r.validateOutputParams(out)

	returnTypeKey := makeReturnTypeKey(out)

	// Use reflection to create a new function that wraps f and caches its result
	cft := reflect.FuncOf(in, out, false)
	cf := reflect.MakeFunc(cft, func(args []reflect.Value) []reflect.Value {
		var ctx context.Context
		if contextIndex != -1 {
			ctx = args[contextIndex].Interface().(context.Context)
		} else {
			ctx = r.defaultContext
		}

		key := r.keyForArgs(args, returnTypeKey, funcOpts)

		// Look up key in cache
		cachedValue, isLocked, err2 := r.getCachedValueOrLock(ctx, key, funcOpts)
		if err2 != nil {
		}
		// If found, return the value
		if cachedValue != nil {
			// Deserialize the value
			results, err := r.deserializeCacheToResults(cachedValue, outputValueHandlers)
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

		// Update the cache in the background
		go func() {
			// trap any panics
			defer func() {
				if r := recover(); r != nil {
					// Todo: Log the panic
					fmt.Println("Panic in background cache update")
				}
			}()

			backgroundCtx := context.Background()
			// Serialize the value
			serialized, err := r.serializeResultsToCache(results, out)
			if err != nil {
				// unlock the cache
				if isLocked {
					err := r.unlockCache(backgroundCtx, key)
					if err != nil {
						// TODO: log error
						fmt.Println("Error unlocking cache")
					}
				}
			}

			// Store the serialized value in the cache
			fmt.Printf("serialized: %v\n", serialized)

			// Saving to the cache does an unlock implicitly.
			r.saveToCache(ctx, key, serialized, funcOpts)
		}()

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

func (r *RedisCache) validateOutputParams(out []reflect.Type) []outputValueHandler {
	// The function should return 1 or more serializable types, and an optional error type.
	// For the serializable types, also make a new instance of each type to allow for serialization.
	if len(out) == 0 {
		panic("f should return at least 1 value")
	}
	serializables := make([]outputValueHandler, len(out))
	errorCount := 0
	for i := 0; i < len(out); i++ {
		if out[i].Implements(serializableType) {
			// Make a new instance of the serializable type
			obj := reflect.New(out[i]).Interface().(Serializable)
			serializableHandler := outputValueHandler{
				serializer:   func(o any) ([]byte, error) { return o.(Serializable).Serialize() },
				deserializer: obj.Deserialize,
			}
			serializables[i] = serializableHandler
			continue
		}
		if out[i] == stringType {
			serializables[i] = outputValueHandler{
				serializer:   func(o any) ([]byte, error) { return []byte(o.(string)), nil },
				deserializer: func(data []byte) (any, error) { return string(data), nil },
			}
			continue
		}
		if out[i] == errorType {
			errorCount++
			if errorCount > 1 {
				panic("f should return at most 1 error")
			}
			serializables[i] = outputValueHandler{
				serializer:   func(o any) ([]byte, error) { return nil, nil },
				deserializer: func(data []byte) (any, error) { return reflect.Zero(errorType), nil },
			}
			continue
		}
		if r.typeHandlers != nil {
			if handler, ok := r.typeHandlers[out[i]]; ok {
				serializables[i] = handler
				continue
			}
		}
		panic("invalid return type")
	}
	return serializables
}

func (r *RedisCache) keyForArgs(args []reflect.Value, returnTypes string, opts CacheOptions) string {
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
	finalKey := fmt.Sprintf("%s%x", opts.KeyPrefix, hash)
	fmt.Printf("hash: %s\n", finalKey)

	// Return the hash as a string
	return finalKey
}
