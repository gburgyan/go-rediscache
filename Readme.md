# About

Redis is a common place to store cached information in systems. Normally everyone does a simple implementation of this caching and moves on with life. The downside of this approach is that there are many corner cases in implementing this that are hard to account for. Additionally, there are cases where you really don't want to invoke an expensive operation if it's already been invoked by another instance of your system.

`go-rediscache` is a tool that will take an existing function and wrap all of the caching logic around it with no additional work by the caller. This is aimed at having a very clean and testable system since the only responsibility of the caller is to supply a function that does work.

As a bonus, this works well with the concepts that are used in the related library `go-ctxdep`.

# Usage

Instantiate a new `go-rediscache` object:

```go
redisConnection := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

cache := &rediscahce.NewRedisCache(ctx, redisConnection, CacheOptions{
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

The requirements for introducing `go-rediscache` to your system are that the paramaters of the function can be used to generate a hash. Underlying this, the function should be stable, such that invoking the same function for the same paramaters should generate identical (or identical semantically) results. The results also need to be able to be marshaled and unmarshalled from to a `[]byte`.

From a high-level perspective, all the inputs are used to construct the cache key which is used to access Redis. The outputs of the function are then saved in the cache. If the same set of inputs are encountered again until the cache expires, the same set of outputs are returned.

Note: The `context.Context` parameter is passed through to the function directly and is _not_ used in the key generation. It is important to ensure that the result of the function that is called does **not** vary based on the context. 

### Input Parameter Requirements

Input parameters must be or implement one of these types:

* `string`
* `Stringer`
* `Keyable`
* Registered with `RegisterTypeHandler`

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

### Function Result Requirements

The results of a function may only be:

* `error` types
  * Results of function calls that return non-`nil` are not cached
* `Serializable`
* Registered with `RegisterTypeHandler`

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
    RedisCheck --> LockLine : Not found in cache
    LockLine --> LockLine : Already locked (wait)
    LockLine --> DeserializeResponse : Found valid data
    LockLine --> FillerFunction : Lock successful
    FillerFunction --> UnlockLine : Error
    UnlockLine --> [*] : Return error
    
    FillerFunction --> BackgroundSave : Success
    BackgroundSave --> [*] : return result of function
    BackgroundSave --> SerializeResponse : Background
    SerializeResponse --> SaveCache
    DeserializeResponse --> [*] : Return cached results

```
