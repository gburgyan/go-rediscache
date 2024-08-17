package rediscache

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func Test_OverlayCacheOptions_AllDefaults(t *testing.T) {
	base := CacheOptions{
		TTL:               1 * time.Minute,
		LockTTL:           5 * time.Second,
		LockWait:          5 * time.Second,
		LockRetry:         50 * time.Millisecond,
		KeyPrefix:         "BaseCache-",
		EnableTiming:      true,
		EncryptionHandler: &nullEncryptor{},
	}
	co := &CacheOptions{}
	co.overlayCacheOptions(base)

	assert.Equal(t, base.TTL, co.TTL)
	assert.Equal(t, base.LockTTL, co.LockTTL)
	assert.Equal(t, base.LockWait, co.LockWait)
	assert.Equal(t, base.LockRetry, co.LockRetry)
	assert.Equal(t, base.KeyPrefix, co.KeyPrefix)
	assert.Equal(t, base.EnableTiming, co.EnableTiming)
	assert.Equal(t, base.EncryptionHandler, co.EncryptionHandler)
}

func Test_OverlayCacheOptions_CustomValues(t *testing.T) {
	encryptor1 := &nullEncryptor{}
	encryptor2 := &nullEncryptor{}
	base := CacheOptions{
		TTL:               1 * time.Minute,
		LockTTL:           5 * time.Second,
		LockWait:          5 * time.Second,
		LockRetry:         50 * time.Millisecond,
		KeyPrefix:         "BaseCache-",
		EnableTiming:      true,
		EncryptionHandler: encryptor1,
	}
	co := &CacheOptions{
		TTL:               2 * time.Minute,
		LockTTL:           10 * time.Second,
		LockWait:          10 * time.Second,
		LockRetry:         100 * time.Millisecond,
		KeyPrefix:         "CustomCache-",
		EnableTiming:      false,
		EncryptionHandler: encryptor2,
	}
	co.overlayCacheOptions(base)

	assert.Equal(t, 2*time.Minute, co.TTL)
	assert.Equal(t, 10*time.Second, co.LockTTL)
	assert.Equal(t, 10*time.Second, co.LockWait)
	assert.Equal(t, 100*time.Millisecond, co.LockRetry)
	assert.Equal(t, "CustomCache-", co.KeyPrefix)
	assert.True(t, co.EnableTiming) // Once set, always set
	assert.IsType(t, encryptor2, co.EncryptionHandler)
}

func Test_OverlayCacheOptions_PartialDefaults(t *testing.T) {
	base := CacheOptions{
		TTL:               1 * time.Minute,
		LockTTL:           5 * time.Second,
		LockWait:          5 * time.Second,
		LockRetry:         50 * time.Millisecond,
		KeyPrefix:         "BaseCache-",
		EnableTiming:      true,
		EncryptionHandler: &nullEncryptor{},
	}
	co := &CacheOptions{
		TTL:       2 * time.Minute,
		KeyPrefix: "CustomCache-",
	}
	co.overlayCacheOptions(base)

	assert.Equal(t, 2*time.Minute, co.TTL)
	assert.Equal(t, base.LockTTL, co.LockTTL)
	assert.Equal(t, base.LockWait, co.LockWait)
	assert.Equal(t, base.LockRetry, co.LockRetry)
	assert.Equal(t, "CustomCache-", co.KeyPrefix)
	assert.Equal(t, base.EnableTiming, co.EnableTiming)
	assert.Equal(t, base.EncryptionHandler, co.EncryptionHandler)
}

func Test_OverlayCacheOptions_NilCacheOptions(t *testing.T) {
	var co *CacheOptions
	base := CacheOptions{}

	assert.Panics(t, func() {
		co.overlayCacheOptions(base)
	})
}
