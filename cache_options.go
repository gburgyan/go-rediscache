package rediscache

import "time"

var defaultCacheOptions = CacheOptions{
	TTL:       5 * time.Minute,
	LockTTL:   10 * time.Second,
	LockWait:  10 * time.Second,
	LockRetry: 100 * time.Millisecond,
	KeyPrefix: "GoCache-",
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
}
