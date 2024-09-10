package rediscache

import "time"

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

var defaultCacheOptions = CacheOptions{
	TTL:               5 * time.Minute,
	LockTTL:           10 * time.Second,
	LockWait:          10 * time.Second,
	LockRetry:         100 * time.Millisecond,
	KeyPrefix:         "GoCache-",
	EnableTiming:      false,
	EncryptionHandler: nil,
	RefreshPercentage: 0.8,
	RefreshAlpha:      1,
	now:               time.Now,
}

func (co *CacheOptions) overlayCacheOptions(base CacheOptions) {
	if co == nil {
		panic("CacheOptions is nil")
	}
	if co.TTL == 0 {
		co.TTL = base.TTL
	}
	if co.LockTTL == 0 {
		co.LockTTL = base.LockTTL
	}
	if co.LockWait == 0 {
		co.LockWait = base.LockWait
	}
	if co.LockRetry == 0 {
		co.LockRetry = base.LockRetry
	}
	if co.KeyPrefix == "" {
		co.KeyPrefix = base.KeyPrefix
	}
	if !co.EnableTiming {
		co.EnableTiming = base.EnableTiming
	}
	if co.EncryptionHandler == nil {
		co.EncryptionHandler = base.EncryptionHandler
	}
	if co.RefreshPercentage == 0 {
		co.RefreshPercentage = base.RefreshPercentage
	}
	if co.RefreshAlpha == 0 {
		co.RefreshAlpha = base.RefreshAlpha
	}
	if co.now == nil {
		co.now = base.now
	}
}
