package rediscache

import "time"

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
