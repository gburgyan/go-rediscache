package go_rediscache

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"reflect"
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
	// entry will never expire in Redis.
	TTL time.Duration

	// LockTTL is the time-to-live for the lock on the cache entry. If LockTTL
	// is 0, the lock will never expire. This controls how long the called function
	// is allowed to run before the lock expires.
	LockTTL time.Duration

	// LockWait is the maximum time to wait for a lock on the cache entry. Usually this
	// should be the same as LockTTL.
	LockWait time.Duration

	// LockRetry is the time to wait before retrying to acquire a lock on the
	// cache entry.
	LockRetry time.Duration

	// KeyPrefix is a prefix that will be added to the key used to store the cache entry.
	KeyPrefix string
}

var serializableType = reflect.TypeOf((*Serializable)(nil)).Elem()
var keyableType = reflect.TypeOf((*Keyable)(nil)).Elem()
var stringerType = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
var stringType = reflect.TypeOf((*string)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
var valueType = reflect.TypeOf((*reflect.Value)(nil)).Elem()

type RedisCache struct {
	defaultContext context.Context
	connection     *redis.Client
	typeHandlers   map[reflect.Type]outputValueHandler
	opts           CacheOptions
}

type Serializer func(any) ([]byte, error)
type Deserializer func([]byte) (any, error)

type outputValueHandler struct {
	serializer   Serializer
	deserializer Deserializer
}

func NewRedisCache(ctx context.Context, client *redis.Client, opts CacheOptions) *RedisCache {
	return &RedisCache{
		defaultContext: ctx,
		connection:     client,
		opts:           opts,
		typeHandlers:   make(map[reflect.Type]outputValueHandler),
	}
}

func (r *RedisCache) RegisterTypeHander(typ reflect.Type, ser Serializer, des Deserializer) {
	r.typeHandlers[typ] = outputValueHandler{
		serializer:   ser,
		deserializer: des,
	}
}
