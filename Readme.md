# About

Redis is a common place to store cached information in systems. Normally everyone does a simple implementation of this caching and moves on with life. The downside of this approach is that there are many corner cases in implementing this that are hard to account for. Additionally, there are cases where you really don't want to invoke an expensive operation if it's already been invoked by another instance of your system.

`go-rediscache` is a tool that will take an existing function and wrap all of the caching logic around it with no additional work by the caller. This is aimed at having a very clean and testable system since the only responsibility of the caller is to supply a function that does work.

As a bonus, this works well with the concepts that are used in the related library `go-ctxdep`

## Requirements

The requirements for introducing `go-rediscache` to your system are that the paramaters of the function can be used to generate a hash. Underlying this, the function should be stable, such that invoking the same function for the same paramaters should generate identical (or identical semantically) results. The results also need to be able to be marshaled and unmarshalled from to a `[]byte`.


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
    LockLine --> RedisCheck : Found valid data
    LockLine --> FillerFunction : Lock successful
    FillerFunction --> UnlockLine : Error
    UnlockLine --> [*] : Return error
    
    FillerFunction --> BackgroundSave : Success
    BackgroundSave --> [*] : return result of function
    BackgroundSave --> SerializeResponse : Background
    SerializeResponse --> SaveCache
    DeserializeResponse --> [*] : Return cached results

```
