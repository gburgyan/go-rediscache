package rediscache

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

	// EnableTiming enables timing of the cache entry using the go-timing package. This
	// will add timing information to the timing context.
	EnableTiming bool

	// CustomTimingName is the name used for the timing context. If not set, the default
	// name will be "redis-cache:<return types>". This setting does not inherit.
	CustomTimingName string

	// EncryptionHandler is an optional handler that can be used to encrypt and decrypt
	// cache entries. If set, the cache entries will be encrypted before being stored
	// in Redis.
	EncryptionHandler EncryptionHandler
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
type Deserializer func(reflect.Type, []byte) (any, error)

type outputValueHandler struct {
	typ          reflect.Type
	serializer   Serializer
	deserializer Deserializer
}

type inputValueHandler struct {
	serializer Serializer
	skip       bool
}

// EncryptionHandler is an interface that defines methods for encrypting and decrypting data.
// Implementing this interface allows custom encryption and decryption logic to be used
// with the RedisCache.
//
// Methods:
type EncryptionHandler interface {
	// Encrypt encrypts the given byte slice and returns the encrypted data or an
	// error if the encryption fails.
	Encrypt([]byte) ([]byte, error)

	// Decrypt decrypts the given byte slice and returns the decrypted data or an error
	// if the decryption fails.
	Decrypt([]byte) ([]byte, error)
}

// NewRedisCache creates a new instance of RedisCache with the provided context, Redis client, and cache options.
//
// Parameters:
//
//	ctx (context.Context): The default context to be used for Redis operations.
//	client (*redis.Client): The Redis client used to interact with the Redis server.
//	opts (CacheOptions): Configuration options for the cache, including TTL, lock settings, and key prefix.
//
// Returns:
//
//	*RedisCache: A pointer to the newly created RedisCache instance.
//
// Example usage:
//
//	ctx := context.Background()
//	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
//	opts := CacheOptions{}
//	cache := NewRedisCache(ctx, client, opts)
func NewRedisCache(ctx context.Context, client *redis.Client, opts CacheOptions) *RedisCache {
	opts.overlayCacheOptions(defaultCacheOptions)

	return &RedisCache{
		defaultContext: ctx,
		connection:     client,
		opts:           opts,
		typeHandlers:   make(map[reflect.Type]outputValueHandler),
	}
}

// RegisterTypeHandler registers a custom serializer and deserializer for a specific type.
// This allows the RedisCache to handle serialization and deserialization of custom types.
//
// Parameters:
//
//	typ (reflect.Type): The type for which the custom serializer and deserializer are being registered.
//	ser (Serializer): A function that takes an instance of the type and returns its serialized byte representation.
//	des (Deserializer): A function that takes a byte slice and returns an instance of the type.
//
// Example usage:
//
//	cache.RegisterTypeHandler(reflect.TypeOf(MyType{}), myTypeSerializer, myTypeDeserializer)
//
// This function is useful when you have custom types that need special handling for caching in Redis.
func (r *RedisCache) RegisterTypeHandler(typ reflect.Type, ser Serializer, des Deserializer) {
	r.typeHandlers[typ] = outputValueHandler{
		typ:          typ,
		serializer:   ser,
		deserializer: des,
	}
}
