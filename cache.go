package rediscache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/gburgyan/go-timing"
	"log"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"strings"
	"time"
)

// cacheFunctionConfig holds the configuration for a cache-enabled function.
// It includes information about the function's input and output types, context index,
// cache options, and handlers for input and output values.
type cacheFunctionConfig struct {
	// in represents the input types of the function.
	in []reflect.Type

	// out represents the output types of the function.
	out []reflect.Type

	// contextIndex is the index of the context argument in the function's input types.
	// If the function does not have a context argument, this will be -1.
	contextIndex int

	// funcOpts contains the cache options for the function.
	funcOpts CacheOptions

	// returnTypeKey is a unique key representing the return types of the function.
	returnTypeKey string

	// inputValueHandlers are handlers used to serialize the function's input arguments.
	inputValueHandlers []inputValueHandler

	// outputValueHandlers are handlers used to serialize and deserialize the function's output values.
	outputValueHandlers []valueHandler

	// realFunction is the original function that is being wrapped with caching logic.
	realFunction reflect.Value

	// cache is a reference to the RedisCache instance.
	cache *RedisCache

	// slope is the slope of the pre-refresh probability function.
	slope float64

	// intercept is the intercept of the pre-refresh probability function.
	intercept float64
}

// Cached wraps the provided function with caching logic using default cache options.
//
// Parameters:
// - f: The function to be wrapped with caching logic. It should be of type `func`.
//
// Returns:
// - The wrapped function with caching logic.
func (r *RedisCache) Cached(f any) any {
	return r.CachedOpts(f, CacheOptions{})
}

// CachedOpts wraps the provided function with caching logic using the specified cache options.
//
// Parameters:
// - f: The function to be wrapped with caching logic. It should be of type `func`.
// - funcOpts: CacheOptions containing the configuration for caching behavior.
//
// Returns:
// - The wrapped function with caching logic.
func (r *RedisCache) CachedOpts(f any, funcOpts CacheOptions) any {
	cfc := r.setupCacheFunctionConfig(f, funcOpts)
	cf := cfc.makeCacheFunction()
	return cf.Interface()
}

// setupCacheFunctionConfig sets up the configuration for a cache-enabled function.
// It validates the input function, determines its input and output types, and prepares handlers for caching.
//
// Parameters:
// - f: The function to be wrapped with caching logic. It should be of type `func`.
// - funcOpts: CacheOptions containing the configuration for caching behavior.
//
// Returns:
// - A cacheFunctionConfig struct containing the configuration for the cache-enabled function.
func (r *RedisCache) setupCacheFunctionConfig(f any, funcOpts CacheOptions) cacheFunctionConfig {
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

	inputValueHandlers := r.makeInputValueHandlers(in)
	outputValueHandlers := r.makeOutputValueHandlers(out)
	returnTypeKey := makeReturnTypeKey(out)

	cfc := cacheFunctionConfig{
		cache:               r,
		in:                  in,
		out:                 out,
		contextIndex:        contextIndex,
		funcOpts:            funcOpts,
		returnTypeKey:       returnTypeKey,
		inputValueHandlers:  inputValueHandlers,
		outputValueHandlers: outputValueHandlers,
		realFunction:        realFunction,
	}
	cfc.slope, cfc.intercept = calculatePreRefreshCoefficients(funcOpts)

	return cfc
}

// makeCacheFunction creates a new function that wraps the original function with caching logic.
// It uses reflection to define the function signature and the caching behavior.
//
// Returns:
// - A reflect.Value representing the new function that wraps the original function with caching logic.
func (cfc cacheFunctionConfig) makeCacheFunction() reflect.Value {
	// Use reflection to create a new function that wraps f and caches its result
	cft := reflect.FuncOf(cfc.in, cfc.out, false)
	cf := reflect.MakeFunc(cft, cfc.cacher)
	return cf
}

// handleEncryption encrypts the serialized byte slice if an EncryptionHandler is provided in the cache options.
//
// Parameters:
// - funcOpts: CacheOptions containing the encryption handler.
// - serialized: The byte slice representing the serialized data to be encrypted.
//
// Returns:
// - A byte slice representing the encrypted data, or the original serialized data if encryption is not needed.
// - An error if the encryption fails.
func handleEncryption(funcOpts CacheOptions, serialized []byte) ([]byte, error) {
	if funcOpts.EncryptionHandler == nil {
		return serialized, nil
	}
	// Encrypt the value
	return funcOpts.EncryptionHandler.Encrypt(serialized)
}

// handleDecryption decrypts the cached value if an EncryptionHandler is provided in the cache options.
// It also measures the decryption time if timing is enabled.
//
// Parameters:
// - ctx: The context in which the decryption is performed.
// - funcOpts: CacheOptions containing the encryption handler and timing settings.
// - doTiming: A boolean indicating whether to measure the decryption time.
// - cachedValue: The byte slice representing the cached value to be decrypted.
//
// Returns:
// - A byte slice representing the decrypted value, or the original cached value if decryption is not needed or fails.
// - An error if the decryption fails.
func handleDecryption(ctx context.Context, funcOpts CacheOptions, cachedValue []byte) ([]byte, error) {
	if funcOpts.EncryptionHandler == nil {
		return cachedValue, nil
	}

	var decryptionComplete timing.Complete
	if funcOpts.EnableTiming {
		_, decryptionComplete = timing.Start(ctx, "decrypt")
	}
	// Decrypt the value
	decrypted, err := funcOpts.EncryptionHandler.Decrypt(cachedValue)
	if funcOpts.EnableTiming {
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

// callBackingFunction calls the provided function with the given arguments and extracts any error from the results.
//
// Parameters:
// - ctx: The context in which the function is called.
// - realFunction: The reflect.Value representing the function to be called.
// - args: A slice of reflect.Value representing the arguments to be passed to the function.
// - doTiming: A boolean indicating whether to measure the execution time of the function.
//
// Returns:
// - A slice of reflect.Value representing the results of the function call.
// - An error if one of the results is of error type and is not nil.
func callBackingFunction(ctx context.Context, realFunction reflect.Value, args []reflect.Value, doTiming bool) ([]reflect.Value, error) {
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

// cacher is a method of cacheFunctionConfig that wraps the original function with caching logic.
// It checks if the result is already cached and returns it if available. If not, it calls the original function,
// caches the result, and returns it.
//
// Parameters:
// - args: A slice of reflect.Value representing the arguments passed to the original function.
//
// Returns:
// - A slice of reflect.Value representing the results of the original function, either from the cache or freshly computed.
func (cfc cacheFunctionConfig) cacher(args []reflect.Value) []reflect.Value {
	var ctx context.Context
	doTiming := false

	// Set the context based on both the base context as well as if the function has a context argument.
	if cfc.contextIndex != -1 {
		ctx = args[cfc.contextIndex].Interface().(context.Context)
		if cfc.funcOpts.EnableTiming {
			doTiming = true
			var timingName string
			if cfc.funcOpts.CustomTimingName != "" {
				timingName = cfc.funcOpts.CustomTimingName
			} else {
				timingName = "redis-cache:" + cfc.returnTypeKey
			}
			timingCtx, complete := timing.Start(ctx, timingName)
			defer complete()
			ctx = timingCtx
		}
	} else {
		ctx = cfc.cache.defaultContext
	}

	key := cfc.keyForArgs(args)

	// Look up key in cache
	cachedValue, lockStatus, err := cfc.cache.getCachedValueOrLock(ctx, key, cfc.funcOpts, doTiming, LockModeDefault)
	if err != nil {
		// If there was an error, call the main function and return the callResults
		// and try to save the result to the cache in the background anyway.
	}

	// If found, return the value
	if cachedValue != nil {
		// Deserialize the value
		var complete timing.Complete
		if doTiming {
			_, complete = timing.Start(ctx, "deserialize")
		}
		results, lastSaveTime, err := deserializeCacheToResults(ctx, cfc.funcOpts, cachedValue, cfc.outputValueHandlers)
		if doTiming {
			complete()
		}
		if err == nil {
			cfc.handleBackgroundRefresh(ctx, key, args, lastSaveTime)
			return results
		}
		// If we got an error deserializing, we can still
		// call the main function and cache the result if
		// it succeeds.
		log.Printf("Error deserializing cache value: %v\n", err)
	}

	callResults, err := callBackingFunction(ctx, cfc.realFunction, args, doTiming)
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
			serialized, err = serializeResultsToCache(cfc.funcOpts, callResults, cfc.outputValueHandlers)

			if err != nil {
				isError = true
			} else {
				// Saving to the cache does an unlock implicitly.
				err := cfc.cache.saveToCache(ctx, key, serialized, cfc.funcOpts)
				if err != nil {
					isError = true
				}
			}
		}

		if isError {
			// unlock the cache
			if lockStatus == LockStatusLockAcquired {
				err := cfc.cache.unlockCache(backgroundCtx, key)
				if err != nil {
					log.Printf("Error unlocking cache: %v\n", err)
				}
			}
			return
		}
	}()

	// Return the value
	return callResults
}

func (cfc cacheFunctionConfig) keyForArgs(args []reflect.Value) string {
	key := keyForArgs(cfc.inputValueHandlers, args, cfc.returnTypeKey)
	hash := sha256.Sum256([]byte(key))
	key = fmt.Sprintf("%s%x", cfc.funcOpts.KeyPrefix, hash)
	return key
}

// handleBackgroundRefresh checks if the cache entry should be pre-refreshed based on the saved time.
// If pre-refresh is needed, it triggers the background refresh process.
//
// Parameters:
// - ctx: The context in which the refresh is performed.
// - key: The cache key for the entry to be refreshed.
// - args: A slice of reflect.Value representing the arguments passed to the original function.
// - savedTime: The time when the cache entry was last saved.
func (cfc cacheFunctionConfig) handleBackgroundRefresh(ctx context.Context, key string, args []reflect.Value, savedTime time.Time) {
	if !cfc.shouldPreRefresh(savedTime) {
		return
	}

	go cfc.doBackgroundRefresh(ctx, key, args...)
}

// doBackgroundRefresh performs a background refresh of the cache entry.
// It locks the cache entry, calls the original function, serializes the result,
// encrypts it if necessary, and saves it back to the cache.
//
// Parameters:
// - ctx: The context in which the refresh is performed.
// - key: The cache key for the entry to be refreshed.
// - args: A variadic slice of reflect.Value representing the arguments passed to the original function.
func (cfc cacheFunctionConfig) doBackgroundRefresh(ctx context.Context, key string, args ...reflect.Value) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("Panic in background goroutine refreshing cache: %v\n", p)
			buf := make([]byte, 1<<16)
			stackSize := runtime.Stack(buf, true)
			log.Printf("Stack trace: %s\n", buf[:stackSize])
		}
	}()
	backgroundCtx := context.Background()
	// Lock the cache
	err := cfc.cache.lockRefresh(backgroundCtx, key, cfc.funcOpts)
	if err != nil {
		return
	}
	defer func() {
		cfc.cache.unlockRefresh(backgroundCtx, key, cfc.funcOpts)
	}()

	// Call the main function
	callResults, err := callBackingFunction(ctx, cfc.realFunction, args, false)
	if err != nil {
		return
	}

	// Serialize the value
	serialized, err := serializeResultsToCache(cfc.funcOpts, callResults, cfc.outputValueHandlers)
	if err != nil {
		return
	}

	serialized, err = handleEncryption(cfc.funcOpts, serialized)
	if err != nil {
		return
	}

	// Save to the cache (we can ignore the error here)
	_ = cfc.cache.saveToCache(ctx, key, serialized, cfc.funcOpts)
}

// shouldPreRefresh determines whether the cache entry should be pre-refreshed based on the saved time and cache options.
//
// Parameters:
// - savedTime: The time when the cache entry was last saved.
//
// Returns:
// - A boolean indicating whether the cache entry should be pre-refreshed.
func (cfc cacheFunctionConfig) shouldPreRefresh(savedTime time.Time) bool {
	if cfc.funcOpts.RefreshPercentage == 0 || cfc.funcOpts.RefreshPercentage == 1 {
		return false
	}

	// Compute the probability of refreshing the cache.
	percentage := cfc.preRefreshFactor(savedTime)

	if percentage < 0 {
		return false
	}

	if percentage > 1 {
		return true
	}

	alpha := cfc.funcOpts.RefreshAlpha
	probability := math.Pow(percentage, alpha-1)

	if rand.Float64() < probability {
		return true
	}

	return false
}

// preRefreshFactor calculates the percentage of the TTL at which the cache entry should be refreshed.
// It uses the age of the cache entry and the pre-computed slope and intercept to determine the refresh factor.
//
// Parameters:
// - savedTime: The time when the cache entry was last saved.
//
// Returns:
// - A float64 representing the percentage of the TTL at which the cache entry should be refreshed.
func (cfc cacheFunctionConfig) preRefreshFactor(savedTime time.Time) float64 {
	age := cfc.funcOpts.now().Sub(savedTime).Seconds()
	percentage := cfc.slope*age + cfc.intercept
	return percentage
}

// calculatePreRefreshCoefficients calculates the slope and intercept for the pre-refresh probability function.
// The function uses the cache options to determine the effective window for refreshing the cache.
//
// Parameters:
// - opts: CacheOptions containing the TTL, LockTTL, and RefreshPercentage.
//
// Returns:
// - slope: The slope of the pre-refresh probability function.
// - intercept: The intercept of the pre-refresh probability function.
func calculatePreRefreshCoefficients(opts CacheOptions) (slope float64, intercept float64) {
	scaleWindow := (opts.TTL - opts.LockTTL).Seconds()
	effectiveWindow := (opts.TTL.Seconds() * (1 - opts.RefreshPercentage)) - opts.LockTTL.Seconds()
	scale := scaleWindow / effectiveWindow
	slope = 1 / effectiveWindow
	intercept = 1 - scale
	return
}

func (r *RedisCache) makeInputValueHandlers(inputs []reflect.Type) []inputValueHandler {
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
		default:
			if handler, found := r.typeHandlers[typ]; found {
				if handler.serializer == nil {
					panic("serializer is required for custom types")
				}
				result[i] = inputValueHandler{
					serializer: handler.serializer,
				}
				continue
			}
			for interfaceType, handler := range r.interfaceHandlers {
				if typ.Implements(interfaceType) {
					if handler.serializer == nil {
						panic("serializer is required for custom types")
					}
					result[i] = inputValueHandler{
						serializer: handler.serializer,
					}
					break
				}
			}
			if result[i].serializer == nil {
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
	}
	return result
}

func (r *RedisCache) makeOutputValueHandlers(out []reflect.Type) []valueHandler {
	// The function should return 1 or more serializable types, and an optional error type.
	// For the serializable types, also make a new instance of each type to allow for serialization.
	if len(out) == 0 {
		panic("f should return at least 1 value")
	}
	serializables := make([]valueHandler, len(out))
	errorCount := 0
	for i := 0; i < len(out); i++ {
		typ := out[i]
		switch {
		case typ.Implements(serializableType):
			obj := reflect.New(typ).Interface().(Serializable)
			serializableHandler := valueHandler{
				typ:          typ,
				serializer:   func(o any) ([]byte, error) { return o.(Serializable).Serialize() },
				deserializer: func(_ reflect.Type, data []byte) (any, error) { return obj.Deserialize(data) },
			}
			serializables[i] = serializableHandler
			continue
		case typ == stringType:
			serializables[i] = valueHandler{
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
			serializables[i] = valueHandler{
				typ:          typ,
				serializer:   func(o any) ([]byte, error) { return nil, nil },
				deserializer: func(_ reflect.Type, data []byte) (any, error) { return reflect.Zero(errorType), nil },
			}
			continue
		default:
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
			for interfaceType, handler := range r.interfaceHandlers {
				if typ.Implements(interfaceType) {
					if handler.serializer == nil {
						panic("serializer is required for custom types")
					}
					if handler.deserializer == nil {
						panic("deserializer is required for custom types")
					}
					serializables[i] = valueHandler{
						typ:          typ,
						serializer:   handler.serializer,
						deserializer: handler.deserializer,
					}
					break
				}
			}
			if serializables[i].typ == nil {
				panic("invalid return type")
			}
		}
	}
	return serializables
}

// keyForArgs generates a unique cache key based on the provided arguments and return types.
// It serializes each argument using the corresponding input value handler and appends the return types.
//
// Parameters:
// - handlers: A slice of inputValueHandler used to serialize the function arguments.
// - args: A slice of reflect.Value representing the arguments passed to the original function.
// - returnTypes: A string representing the return types of the function.
//
// Returns:
// - A string representing the unique cache key.
func keyForArgs(handlers []inputValueHandler, args []reflect.Value, returnTypes string) string {
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
