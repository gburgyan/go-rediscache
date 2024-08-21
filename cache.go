package rediscache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/gburgyan/go-timing"
	"log"
	"reflect"
	"runtime"
	"strings"
)

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

	contextIndex := -1
	in := make([]reflect.Type, t.NumIn())
	for i := 0; i < t.NumIn(); i++ {
		in[i] = t.In(i)
		if t.In(i) == contextType {
			if contextIndex != -1 {
				panic("f should have at most 1 context argument")
			}
			contextIndex = i
		}
	}
	out := make([]reflect.Type, t.NumOut())
	for i := 0; i < t.NumOut(); i++ {
		out[i] = t.Out(i)
	}

	inputValueHandlers := r.validateInputParams(in)
	outputValueHandlers := r.validateOutputParams(out)

	returnTypeKey := makeReturnTypeKey(out)

	// Use reflection to create a new function that wraps f and caches its result
	cft := reflect.FuncOf(in, out, false)
	cf := reflect.MakeFunc(cft, func(args []reflect.Value) []reflect.Value {
		var ctx context.Context
		doTiming := false

		// Set the context based on both the base context as well as if the function has a context argument.
		if contextIndex != -1 {
			ctx = args[contextIndex].Interface().(context.Context)
			if funcOpts.EnableTiming {
				doTiming = true
				var timingName string
				if funcOpts.CustomTimingName != "" {
					timingName = funcOpts.CustomTimingName
				} else {
					timingName = "redis-cache:" + returnTypeKey
				}
				timingCtx, complete := timing.Start(ctx, timingName)
				defer complete()
				ctx = timingCtx
			}
		} else {
			ctx = r.defaultContext
		}

		key := r.keyForArgs(inputValueHandlers, args, returnTypeKey, funcOpts)
		hash := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("%s%x", funcOpts.KeyPrefix, hash)

		// Look up key in cache
		cachedValue, isLocked, err := r.getCachedValueOrLock(ctx, key, funcOpts, doTiming)
		if err != nil {
			// If there was an error, call the main function and return the callResults
			// and try to save the result to the cache in the background anyway.
		}

		// If found, return the value
		if cachedValue != nil {
			err = nil
			cachedValue, err = r.handleDecryption(ctx, funcOpts, doTiming, cachedValue)

			if err == nil {
				// Deserialize the value
				var complete timing.Complete
				if doTiming {
					_, complete = timing.Start(ctx, "deserialize")
				}
				results, err := r.deserializeCacheToResults(cachedValue, outputValueHandlers)
				if doTiming {
					complete()
				}
				if err == nil {
					return results
				}
				// If we got an error deserializing, we can still
				// call the main function and cache the result if
				// it succeeds.
				log.Printf("Error deserializing cache value: %v\n", err)
			}
		}

		callResults, err := r.callBackingFunction(ctx, realFunction, args, doTiming)
		isError := false
		if err != nil {
			isError = true
		}

		// Update the cache in the background
		go func() {
			// trap any panics
			defer func() {
				if p := recover(); p != nil {
					log.Printf("Panic in background goroutine saving to cache: %v\n", p)
					buf := make([]byte, 1<<16)
					stackSize := runtime.Stack(buf, true)
					log.Printf("Stack trace: %s\n", buf[:stackSize])
				}
			}()

			backgroundCtx := context.Background()

			var serialized []byte
			if !isError {
				// Serialize the value
				serialized, err = r.serializeResultsToCache(funcOpts, callResults, outputValueHandlers)

				if err != nil {
					isError = true
				} else {
					serialized, err = r.handleEncryption(funcOpts, serialized)

					if err == nil {
						// Saving to the cache does an unlock implicitly.
						r.saveToCache(ctx, key, serialized, funcOpts)
					} else {
						isError = true
					}
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
		return callResults
	})
	return cf.Interface()
}

func (r *RedisCache) handleEncryption(funcOpts CacheOptions, serialized []byte) ([]byte, error) {
	if funcOpts.EncryptionHandler == nil {
		return serialized, nil
	}
	// Encrypt the value
	return funcOpts.EncryptionHandler.Encrypt(serialized)
}

func (r *RedisCache) handleDecryption(ctx context.Context, funcOpts CacheOptions, doTiming bool, cachedValue []byte) ([]byte, error) {
	if funcOpts.EncryptionHandler == nil {
		return cachedValue, nil
	}

	var decryptionComplete timing.Complete
	if doTiming {
		_, decryptionComplete = timing.Start(ctx, "decrypt")
	}
	// Decrypt the value
	decrypted, err := funcOpts.EncryptionHandler.Decrypt(cachedValue)
	if doTiming {
		decryptionComplete()
	}
	if err != nil {
		// If there was an error decrypting, we can still call the main function
		// and cache the result if it succeeds.
		log.Printf("Error decrypting cache value: %v\n", err)
	} else {
		cachedValue = decrypted
	}

	return cachedValue, err
}

func (r *RedisCache) callBackingFunction(ctx context.Context, realFunction reflect.Value, args []reflect.Value, doTiming bool) ([]reflect.Value, error) {
	// If not found, call f and cache the result
	var fillFuncComplete timing.Complete
	if doTiming {
		_, fillFuncComplete = timing.Start(ctx, "fillFunc")
	}
	results := realFunction.Call(args)
	if doTiming {
		fillFuncComplete()
	}

	// Extract the return value from the results/error
	var err error
	for _, result := range results {
		if result.Type() == errorType {
			if !result.IsNil() {
				err = result.Interface().(error)
			}
		}
	}
	return results, err
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
		typ := inputs[i]
		switch {
		case typ.Implements(contextType):
			result[i] = inputValueHandler{skip: true}
		case typ.Implements(keyableType):
			result[i] = inputValueHandler{serializer: func(a any) ([]byte, error) {
				return []byte(a.(Keyable).CacheKey()), nil
			}}
		case typ == stringType:
			result[i] = inputValueHandler{serializer: func(a any) ([]byte, error) {
				return []byte(a.(string)), nil
			}}
		case r.typeHandlers != nil:
			if handler, found := r.typeHandlers[typ]; found {
				if handler.serializer == nil {
					panic("serializer is required for custom types")
				}
				result[i] = inputValueHandler{
					serializer: func(a any) ([]byte, error) {
						return handler.serializer(a)
					},
				}
			}

		default:
			result[i] = inputValueHandler{
				serializer: func(a any) ([]byte, error) {
					buf := new(bytes.Buffer)
					err := binary.Write(buf, binary.LittleEndian, a)
					if err != nil {
						return nil, err
					}
					return buf.Bytes(), nil
				},
			}
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
		typ := out[i]
		switch {
		case typ.Implements(serializableType):
			obj := reflect.New(typ).Interface().(Serializable)
			serializableHandler := outputValueHandler{
				typ:          typ,
				serializer:   func(o any) ([]byte, error) { return o.(Serializable).Serialize() },
				deserializer: func(_ reflect.Type, data []byte) (any, error) { return obj.Deserialize(data) },
			}
			serializables[i] = serializableHandler
			continue
		case typ == stringType:
			serializables[i] = outputValueHandler{
				typ:          typ,
				serializer:   func(o any) ([]byte, error) { return []byte(o.(string)), nil },
				deserializer: func(_ reflect.Type, data []byte) (any, error) { return string(data), nil },
			}
			continue
		case typ == errorType:
			errorCount++
			if errorCount > 1 {
				panic("f should return at most 1 error")
			}
			serializables[i] = outputValueHandler{
				typ:          typ,
				serializer:   func(o any) ([]byte, error) { return nil, nil },
				deserializer: func(_ reflect.Type, data []byte) (any, error) { return reflect.Zero(errorType), nil },
			}
			continue
		case r.typeHandlers != nil:
			if handler, ok := r.typeHandlers[typ]; ok {
				if handler.serializer == nil {
					panic("serializer is required for custom types")
				}
				if handler.deserializer == nil {
					panic("deserializer is required for custom types")
				}
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

	return key
}
