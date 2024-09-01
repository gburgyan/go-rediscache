package rediscache

import (
	"context"
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

// Serializable is an interface that defines methods for serializing and deserializing data.
// Implementing this interface allows custom types to be serialized to and deserialized from byte slices.
type Serializable interface {
	// Serialize converts the implementing type to a byte slice.
	//
	// Returns:
	// - A byte slice representing the serialized data.
	// - An error if the serialization fails.
	Serialize() ([]byte, error)

	// Deserialize converts a byte slice to the implementing type.
	//
	// Parameters:
	// - data: A byte slice representing the serialized data.
	//
	// Returns:
	// - The deserialized instance of the implementing type.
	// - An error if the deserialization fails.
	Deserialize([]byte) (any, error)
}

type CacheOptions struct {
	// TTL is the time-to-live for the cache entry. If TTL is 0, the cache
	// entry will never expire in Redis.
	TTL time.Duration

	// RefreshPercentage expresses the percentage of the TTL at which the cache
	// entry should be refreshed. If RefreshPercentage is 1, the cache entry will
	// not be refreshed. If RefreshPercentage is 0.5, the cache entry will be refreshed
	// halfway through its TTL. This setting is useful for ensuring that the cache
	// entry is always fresh and fetching new data before the cache entry expires.
	RefreshPercentage float64

	// RefreshAlpha is the alpha value used to calculate the probability of refreshing
	// the cache entry. The time range between when a cache entry is eligible for
	// refresh and the TTL-LockTTL is scaled to the range [0, 1] and called x.
	// The probability of refreshing the cache entry is calculated as x^(alpha-1).
	// If RefreshAlpha is 1 or less, the cache entry will be refreshed immediately
	// when it is eligible for refresh. A higher alpha value will make it less likely
	// that the cache entry will be refreshed.
	// A value of 0 will inherit the default alpha for the cache.
	RefreshAlpha float64

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

	// now is a function that returns the current time. This is used for testing purposes
	// to mock the current time.
	now func() time.Time
}

var serializableType = reflect.TypeOf((*Serializable)(nil)).Elem()
var keyableType = reflect.TypeOf((*Keyable)(nil)).Elem()
var stringType = reflect.TypeOf((*string)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()
var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
var valueType = reflect.TypeOf((*reflect.Value)(nil)).Elem()

type RedisCache struct {
	defaultContext    context.Context
	connection        *redis.Client
	typeHandlers      map[reflect.Type]valueHandler
	interfaceHandlers map[reflect.Type]valueHandler
	opts              CacheOptions
}

// Serializer is a function type that defines a method for serializing data.
// It takes an input of any type and returns a byte slice and an error.
//
// Parameters:
// - any: The input data to be serialized.
//
// Returns:
// - A byte slice representing the serialized data.
// - An error if the serialization fails.
type Serializer func(any) ([]byte, error)

// Deserializer is a function type that defines a method for deserializing data.
// It takes a reflect.Type and a byte slice, and returns an instance of the type and an error.
//
// Parameters:
// - reflect.Type: The type to which the data should be deserialized.
// - []byte: The byte slice representing the serialized data.
//
// Returns:
// - An instance of the deserialized type.
// - An error if the deserialization fails.
type Deserializer func(reflect.Type, []byte) (any, error)

type valueHandler struct {
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
		defaultContext:    ctx,
		connection:        client,
		opts:              opts,
		typeHandlers:      make(map[reflect.Type]valueHandler),
		interfaceHandlers: make(map[reflect.Type]valueHandler),
	}
}

// RegisterTypeHandler registers a custom serializer and deserializer for a specific type.
//
// Parameters:
//
//	typ (reflect.Type): The type for which the handler is being registered.
//	ser (Serializer): The function used to serialize instances of the type.
//	des (Deserializer): The function used to deserialize byte slices into instances of the type.
//
// If the type is an interface, the handler is registered in the interfaceHandlers map.
// Otherwise, it is registered in the typeHandlers map.
func (r *RedisCache) RegisterTypeHandler(typ reflect.Type, ser Serializer, des Deserializer) {
	if typ.Kind() == reflect.Interface {
		r.interfaceHandlers[typ] = valueHandler{
			typ:          typ,
			serializer:   ser,
			deserializer: des,
		}
	} else {
		r.typeHandlers[typ] = valueHandler{
			typ:          typ,
			serializer:   ser,
			deserializer: des,
		}
	}
}
