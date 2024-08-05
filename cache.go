package rediscache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

type inputValueHandler struct {
	serializer func(any) ([]byte, error)
	skip       bool
}

func (r *RedisCache) Cached(f any) any {
	return r.CachedOpts(f, CacheOptions{})
}

func (r *RedisCache) CachedOpts(f any, funcOpts CacheOptions) any {
	funcOpts.overlayCacheOptions(r.opts)

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

	inputValueHandlers := r.validateInputParams(in)
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

		key := r.keyForArgs(inputValueHandlers, args, returnTypeKey, funcOpts)
		hash := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("%s%x", funcOpts.KeyPrefix, hash)

		// Look up key in cache
		cachedValue, isLocked, err := r.getCachedValueOrLock(ctx, key, funcOpts)
		if err != nil {
			// If there was an error, call the main function and return the results
			// and try to save the result to the cache in the background anyway.
		}

		// If found, return the value
		if cachedValue != nil {
			// Deserialize the value
			results, err := r.deserializeCacheToResults(cachedValue, outputValueHandlers)
			if err == nil {
				//fmt.Println("Cache hit!")
				return results
			}
			// If we got an error deserializing, we can still
			// call the main function and cache the result if
			// it succeeds.
		}

		//fmt.Println("Cache miss!")

		// If not found, call f and cache the result
		results := realFunction.Call(args)

		// Extract the return value from the results/error
		isError := false
		for _, result := range results {
			if result.Type() == errorType {
				if !result.IsNil() {
					isError = true
				}
			}
		}

		// Update the cache in the background
		go func() {
			// trap any panics
			defer func() {
				if p := recover(); p != nil {
					// Log the panic and print stack trace
					fmt.Printf("Panic in background goroutine saving to cache: %v\n", p)
					buf := make([]byte, 1<<16)
					stackSize := runtime.Stack(buf, true)
					fmt.Printf("Stack trace: %s\n", buf[:stackSize])
				}
			}()

			backgroundCtx := context.Background()

			var serialized []byte
			if !isError {
				var err error
				// Serialize the value
				serialized, err = r.serializeResultsToCache(results, outputValueHandlers, out)

				if err != nil {
					isError = true
				} else {
					// Store the serialized value in the cache
					fmt.Printf("serialized: %v\n", serialized)

					// Saving to the cache does an unlock implicitly.
					r.saveToCache(ctx, key, serialized, funcOpts)
				}
			}

			if isError {
				// unlock the cache
				if isLocked {
					err := r.unlockCache(backgroundCtx, key)
					if err != nil {
					}
				}
				return
			}
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

func (r *RedisCache) validateInputParams(inputs []reflect.Type) []inputValueHandler {
	// f should have 0 or 1 context argument, and 1 or more arguments that are Keyable. The last argument should be a pointer to a Serializable.
	if len(inputs) < 1 {
		panic("f should have at least 1 argument")
	}
	result := make([]inputValueHandler, len(inputs))
	for i := 0; i < len(inputs); i++ {
		if inputs[i].Implements(contextType) {
			result[i] = inputValueHandler{skip: true}
		} else if inputs[i].Implements(keyableType) {
			result[i] = inputValueHandler{serializer: func(a any) ([]byte, error) {
				return []byte(a.(Keyable).CacheKey()), nil
			}}
		} else if inputs[i].Implements(stringerType) {
			result[i] = inputValueHandler{serializer: func(a any) ([]byte, error) {
				return []byte(a.(fmt.Stringer).String()), nil
			}}
		} else if inputs[i] == stringType {
			result[i] = inputValueHandler{serializer: func(a any) ([]byte, error) {
				return []byte(a.(string)), nil
			}}
		} else if _, found := r.typeHandlers[inputs[i]]; found {
			result[i] = inputValueHandler{
				serializer: func(a any) ([]byte, error) {
					return r.typeHandlers[inputs[i]].serializer(a)
				},
			}
		} else {
			panic("invalid argument type")
		}
	}
	return result
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

func (r *RedisCache) keyForArgs(handlers []inputValueHandler, args []reflect.Value, returnTypes string, opts CacheOptions) string {
	keyBuilder := strings.Builder{}

	for i := 0; i < len(handlers); i++ {
		if handlers[i].skip {
			continue
		}
		if keyBuilder.Len() > 0 {
			keyBuilder.WriteString(":")
		}
		serialized, err := handlers[i].serializer(args[i].Interface())
		if err != nil {
			panic(err)
		}
		keyBuilder.Write(serialized)
	}
	keyBuilder.WriteString("/")
	keyBuilder.WriteString(returnTypes)
	key := keyBuilder.String()
	//fmt.Printf("key: %s\n", key)

	return key
}
