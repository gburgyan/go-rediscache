![Build status](https://github.com/gburgyan/go-rediscache/actions/workflows/go.yml/badge.svg) [![Go Report Card](https://goreportcard.com/badge/github.com/gburgyan/go-rediscache)](https://goreportcard.com/report/github.com/gburgyan/go-rediscache) [![PkgGoDev](https://pkg.go.dev/badge/github.com/gburgyan/go-rediscache)](https://pkg.go.dev/github.com/gburgyan/go-rediscache)

# go-rediscache

`go-rediscache` is a Go package designed to simplify the implementation of caching with Redis. It abstracts away the complexity of managing cache keys, handling concurrency, and ensuring consistency, allowing developers to focus on building reliable and performant applications. Whether you're working with expensive function calls, external API requests, or complex data processing, `go-rediscache` helps you avoid redundant operations and optimize resource usage.

# About

Redis is a common place to store cached information in systems. Normally everyone does a simple implementation of this caching and moves on with life. The downside of this approach is that there are many corner cases in implementing this that are hard to account for. Additionally, there are cases where you really don't want to invoke an expensive operation if it's already been invoked by another instance of your system.

`go-rediscache` is a tool that will take an existing function and wrap all of the caching logic around it with no additional work by the caller. This is aimed at creating and maintaining a very clean and testable system since the only responsibility of the caller is to supply a function that does work.

As a bonus, this works well with the concepts that are used in the related library [`go-ctxdep`](https://github.com/gburgyan/go-ctxdep), though this is in no way a requirement.

## Installation

To install `go-rediscache`, use the following command:

```bash
go get github.com/gburgyan/go-rediscache
```

# Usage

Instantiate a new `go-rediscache` object:

```go
redisConnection := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

cache := rediscache.NewRedisCache(ctx, redisConnection, CacheOptions{
        TTL:       time.Minute,
        LockTTL:   time.Minute,
        LockWait:  time.Second * 10,
        LockRetry: time.Millisecond * 200,
        KeyPrefix: "GoCache-"
    })
```

Then, given any function:

```go

func getUserInfo(ctx context.Context, userId string) (string, error) {}

cachedUserInfoFunc := rediscache.Cache(cache, getUserInfo)

result, err := cachedUserInfoFunc(ctx, "User42")
```

If the user's info was already cached, it will be returned without calling the function. Otherwise, the function is called and the results will be saved.

## Requirements

The requirements for introducing `go-rediscache` to your system are that the parameters of the function can be used to generate a hash. The function should be stable, such that invoking the same function for the same parameters should generate identical (or identical semantically) results. The results also need to be able to be marshaled and unmarshalled from to a `[]byte`.

From a high-level perspective, all the inputs are used to construct the cache key which is used to access Redis. The outputs of the function are then saved in the cache. If the same set of inputs are encountered again until the cache expires, the same set of outputs are returned.

Note: The `context.Context` parameter is passed through to the function directly and is _not_ used in the key generation. It is important to ensure that the result of the function that is called does **not** vary based on the context. 

### Input Parameter Requirements

Input parameters must be or implement one of these types:

* `string`
* `Keyable`
* Registered with `RegisterTypeHandler`
* Registered with `RegisterInterfaceHandler`
* Able to be written with `binary.Write` 

The `Keyable` is defined by the library:

```go
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
```

Regardless of _how_ the key is made, it is critical that anything that can affect the result of the function call must make it into the key.

### Function Result Requirements

The results of a function may only be:

* `error` types
  * Results of function calls that return a non-`nil` error are not cached
* `Serializable`
* Registered with `RegisterTypeHandler`
* Registered with `RegisterInterfaceHandler`

In whatever way the serialization and deserialization happen, an object that is serialized the deserialized from the cached `[]byte` should remain semantically identical to the originally returned object.

### Custom Type and Interface Handlers

`go-rediscache` allows you to register custom serializers and deserializers for specific types and interfaces. This is useful when you have custom types or interfaces that need special handling for caching in Redis.

#### Registering a Custom Type Handler

To register a custom type handler, use the `RegisterTypeHandler` method. This method takes the type, a serializer function, and a deserializer function.

If the type is an interface, then any object that implements that interface will match and use the provided serializer and deserializer.

**Example:**

```go
// Define a custom type
type MyType struct {
    Field1 string
    Field2 int
}

// Define a serializer for MyType
func myTypeSerializer(data any) ([]byte, error) {
    myType, ok := data.(MyType)
    if !ok {
        return nil, errors.New("data does not match MyType")
    }
    return json.Marshal(myType)
}

// Define a deserializer for MyType
func myTypeDeserializer(typ reflect.Type, data []byte) (any, error) {
    var myType MyType
    if err := json.Unmarshal(data, &myType); err != nil {
        return nil, err
    }
    return myType, nil
}

// Register the custom type handler
cache.RegisterTypeHandler(reflect.TypeOf(MyType{}), myTypeSerializer, myTypeDeserializer)
```

### Pointers

There is no special handing of pointers in this package. There are default serializers for some common types, but there is no magic around pointers.

If you need to add a handler for a pointer type:

```go
// Non-pointer version
cache.RegisterTypeHandler(reflect.TypeOf((*someType)(nil)).Elem(), rediscache.JsonSerializer, rediscache.JsonDeserializer)

// reflect.TypeOf((*someType)(nil)).Elem() can be replaced with reflect.TypeOf(someType{}) as they
// are generally interchangeable.

// Pointer version
cache.RegisterTypeHandler(reflect.TypeOf((*someType)(nil)), rediscache.JsonSerializer, rediscache.JsonDeserializer)
```

In this case, this is using the included JSON serializer and deserializers. The two lines above are distinct and deal with two different types with differing semantics. This package does not want to assume what the caller's requirements are.

The included JSON serializer and deserializer handles both instances and pointers just fine.

## State Diagram

```mermaid
stateDiagram-v2
    HashParams: Hash function input arguments
    RedisCheck : Check Redis for cached results
    LockLine : Attempt to lock cache line
    FillerFunction : Call base function to get results
    SerializeResponse : Marshal results to []byte
    SaveCache : Save results (implicit unlock)
    UnlockLine : Delete lock semaphore
    DeserializeResponse : Unmarshal []byte to result objects
    
    state BackgroundSave <<fork>>
    
    [*] --> HashParams
    HashParams --> RedisCheck
    RedisCheck --> DeserializeResponse : Found in cache
    RedisCheck --> RedisCheck : Already locked (wait)
    RedisCheck --> LockLine : Not found in cache
    LockLine --> FillerFunction : Lock successful
    LockLine --> RedisCheck : Lock failed (wait)
    FillerFunction --> UnlockLine : Error
    UnlockLine --> [*] : Return error
    
    FillerFunction --> BackgroundSave : Success
    BackgroundSave --> [*] : return result of function
    BackgroundSave --> SerializeResponse : Background
    SerializeResponse --> SaveCache
    DeserializeResponse --> [*] : Return cached results
```

The looping that occurs in the "check Redis for cached results" works thusly:

- Read the value of the cache key
  - If the value is present and not empty then it's a cache hit and no further actions are needed
  - If the value is present and it _is_ empty, that indicates that another instance is in the process of getting the value -- loop and see if it shows up
  - If no value is present, then the result simply isn't in the Redis cache and we continue
- Attempt to lock the cache line
  - Write a blank into the cache line
  - If it succeeds, then we have locked the cache line and we can call the base function to compute what should go into the cache
  - It it _fails_ then we hit a race condition and another instance locked it before we did -- simply loop back to reading the cache with the expectation that the value will eventually show up

All of this is handled by `getCachedValueOrLock()` in `redis.go`.

# Slices

There are times when you need to get a bunch of things, and you would like to have them cached. We can handle those as well!

There are two classes of functions that we can implicitly parallelize:

1. `func(ctx context.Context, in IN) (OUT, error)`
2. `func(ctx context.Context, in []IN) ([]OUT, error)`

## Single Input Functions

The first one is very easy as all we do is basically a convenience function that runs all of the cache lookups in parallel.

Calling `CacheBulk` or `CacheBulkOpts` with a function in the form of `func(ctx context.Context, in IN) (OUT, error)` will return a function that handles caching in the form of `func(ctx context.Context, in []IN) ([]OUT, error)`

Each of these lookups are handled on their own goroutine. If any of the underlying cached functions returns an error, those will be collected in a `SliceError` object.

## Slice Input Functions

Functions in the form `func(ctx context.Context, in []IN) ([]OUT, error)` are handled by `CacheBulkSlice` and `CacheBulkSliceOpts`. The results of each of the `IN` are cached individually.

This is a more complicated as some of the results of the inputs may already be cached, or already being processed by another call. This is smart enough to look at the global state of affairs and behave appropriately.

Consider the case of:

```go
func FetchSomething(ctx context.Context, id []string) ([]string, error) {
	// Each of the result []string need to correspond 1 to 1 with the inputs
}

cachedFunc := rediscache.CacheBulkSlice(cache, FetchSomething)

results, err := cachedFunc(ctx, []string{"Cached", "Locked", "Miss"})
```

Each of the inputs are stored in the cached individually, so they can be in different states:

* **Already cached** - We already have the results for this input, so we don't have to call the underlying function to fetch a new instance of this.
* **Locked** - Another process is already busy fetching it. So we don't need to do additional work to get this value, we'll wait for the other process to complete and write the results to the cache.
* **Cache miss** - We don't have the value, and no one else is working on it. All the cache misses will be bundled together when the backing function is called.

This is based on the idea that the cost of calling the backing function scales with the number of input values, which is normally the case. There _are_ cases where it makes sense to call the backing function if there are any cache misses. If the cache filling cost doesn't scale with input count, then you can set the `RefreshEntireBatch` option. In this case, if there are any cache misses, then the entirety of the input values will be refreshed. In the case where all the inputs are already locked, we'll simply wait for the other process that's already calling the function to finish.

There is also error handling in cases where there is a cache error (i.e. cannot be deserialized) or a lock timeout. In either case, the backing function will be called as a one-off in either case.

## Error Reporting

Both types of slice caching functions return a single error object. If the error originates from the backing function, then it'll return a `SliceError` object to aggregate the errors from the function calls. 

```go
type SliceError struct {
	Errors []error
}

type SliceItemError struct {
    Index int
    Err   error
}
```

# Options

## Configuration Options

The `CacheOptions` struct allows you to customize the behavior of the cache:

- `TTL`: Defines the time-to-live for each cache entry. Default is 5 minutes. Expiration of the cache is handled entirely by Redis.
- `LockTTL`: Specifies the duration for which the cache line is locked during a cache miss to prevent race conditions. Default is 10 seconds.
- `LockWait`: The maximum duration to wait for a lock to be released before giving up. Default is 10 seconds. This should be greater than the expected time for the call to the base function.
- `LockRetry`: The interval between retries when waiting for a lock. Default is 100 milliseconds. This controls the polling behavior of the cache is there's another call that is being made at the same time. If this is too low, it'll needlessly increase the load on Redis, if it's too long then it will cause unneeded delays in picking up a value that was cached from another call.
- `KeyPrefix`: The prefix for all cache keys to avoid collisions with other cache entries in Redis. Default is "GoCache-"
- `CustomTimingName`: If using the integration with `go-timing`, this is the name that is used for the timing nodes that are used for cache timing. The default is the types of the result objects.
- `EncryptionHandler`: If encrypting the cached values stored in Redis, this provides the encryption and decryption functions. The default is storing the values unencrypted and relying on Redis's security to prevent access.
- `EnableLogging`: If set to `true`, the cache will log information about the cache hits and misses. Default is `false`.
- `RefreshPercentage`: The percentage of the TTL that will be used to refresh the cache. Default is 0.0, which means that the cache will not be refreshed. If set to 0.8, the cache will be refreshed 80% of the way through the TTL. This is useful for ensuring that the cache is always up-to-date and that the cache is not stale. In case of a refresh, it will be done in the background go routine and the old value will be returned to the caller.
- `RefreshAlpha`: The alpha value used to calculate the probability of refreshing the cache entry. The time range between when a cache entry is eligible for refresh and the TTL-LockTTL is scaled to the range [0, 1] and called x. The probability of refreshing the cache entry is calculated as x^(alpha-1). If RefreshAlpha is 1 or less, the cache entry will be refreshed immediately when it is eligible for refresh. A higher alpha value will make it less likely that the cache entry will be refreshed. A value of 0 will inherit the default alpha for the cache. An alpha of two will be a linear ramp of probabilities from 0 to 1. Default is 1 which will immediately refresh the cache upon it being eligible.
- `RefreshEntireBatch`: When calling the `CacheBulkSlice`, if there are any cache misses, will trigger refreshing the entire batch. 

Normally `LockWait` and `LockTTL` should be set to the same value. If `LockWait` times out before the `LockTTL` expires, an additional call to the backing function will be made.

The configuration needs to be driven from the needs and behaviors of the system. Generally, the expectation is that there is no contention for a cache line. The retry behavior needs to be tuned to the expected use case.

In very high contention cases, tuning the alpha can be useful to prevent overly aggressive refresh locks in the cache.

## Timing

The `go-rediscache` was designed to be able to properly interact with the [`go-timing`](https://github.com/gburgyan/go-timing) package. This can be enabled by using the `EnableTiming` flag on the `CacheOptions` object. Enabling this will cause additional timing contexts to be added to the context to record some additional useful details:

* How long the entire cache call took?
* If there was any spinning to get a locked cache line, how long was the wait and how many times did it spin?
* How long the calls to Redis actually took?
* Was the item found in the cache or not?
* How long it took to call the backing function if there was a miss?
* How long did deserialization take?

An example of what this looks like from the output of the unit tests (using a mocked Redis service):

```
redis-cache:string - 23.708µs
redis-cache:string > redis - 14.583µs (cache-hit:true)
redis-cache:string > redis > get - 12.417µs
redis-cache:string > deserialize - 3.125µs
```

In this case, the entire cache workflow took 23.708µs, the result was found in Redis and the call to that took 12.417µs. Once it has serialized results, it took 3.125µs to deserialize it into the actual objects.

The default key looks like `redis-cache:` and the return types of the cached function. If you want to use a different key, you can set that in the `CustomTimingName` field of the options. Note that the `CustomTimingName` does _not_ inherit so there is no chance that setting this at the overall cache level will affect the real calls. 

## gRPC Support

Supporting gRPC messages for both parameters and responses is easy. The only reason it's not in the package by default is that I didn't want to needlessly expand the dependencies of the package.

Here are working serializers and deserializers for any gRPC message:

```go
// GRPCSerializer serializes a protobuf message to a byte slice.
func GRPCSerializer(data any) ([]byte, error) {
	msg, ok := data.(proto.Message)
	if !ok {
		return nil, errors.New("data does not implement proto.Message")
	}
	return proto.Marshal(msg)
}

// GRPCDeserializer deserializes a byte slice to a protobuf message.
func GRPCDeserializer(typ reflect.Type, data []byte) (any, error) {
	msg, ok := reflect.New(typ.Elem()).Interface().(proto.Message)
	if !ok {
		return nil, errors.New("type does not implement proto.Message")
	}
	err := proto.Unmarshal(data, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}
```

You also need to register the `proto.Message` interface using the `RegisterTypeHandler`:

```go
cache.RegisterTypeHandler(reflect.TypeOf((*proto.Message)(nil)).Elem(), GRPCSerializer, GRPCDeserializer)
```

This enables everything that conforms to the gRPC message interface to be serialized and deserialized using these functions.

## Encryption and Security

Since the cache values are stored in Redis, which depending on how things are set up in your environment, there are cases where having the values encrypted is a needed feature.

You can set the `EncryptionHandler` to an object that implements the same interface name:

```go
type EncryptionHandler interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
```

Ensure that:

```go
plaintext := []byte{ ...}

cyphertext, _ := provider.Encrypt(plaintext)
decrypted, _ := provider.Decrypt(cyphertext)

Assert.Equal(t, plaintext, decrypted)
```

It is important that all instances of a cache that access the same Redis backend be able to decrypt each other's data.

You can use any encryption method that is suitable for your use case. Keep in mind that the cached values may be relatively stable so some information leakage may be present if one were to run a correlation attack against everything stored in Redis. If this is important, something that may be considered is having some salting present in the provided algorithm.

The key generation process employs the SHA-256 hashing algorithm, which is recognized for its cryptographic security. Given the requirement for deterministic key generation, the use of salting is not feasible as it would disrupt the stability of the generated keys. Consequently, the primary attack vectors are limited to correlation attacks, such as identifying the presence of a specific key when a particular user, e.g., Alice, logs in. Even with salting, given the requirement of stable keys, most attacks would still be possible. Note that this is a limitation of storing things in a database of any type, and not specifically related to this package.

Regardless of what is in this README, always do your own research and be aware of any pitfalls around the entire topic of security.

# License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.