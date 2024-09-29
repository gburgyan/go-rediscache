package rediscache

import (
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func Test_getCachedValueOrLock_CacheHit(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetVal("valid-value")

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.NoError(t, err)
	assert.Equal(t, LockStatusUnlocked, locked)
	assert.Equal(t, []byte("valid-value"), value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_getCachedValueOrLock_CacheError(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetErr(errors.New("redis error"))

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.Error(t, err)
	assert.Equal(t, LockStatusError, locked)
	assert.Nil(t, value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_getCachedValueOrLock_CacheMiss(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetErr(redis.Nil)
	mock.ExpectSetNX(key, "", c.opts.LockTTL).SetVal(true)

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.NoError(t, err)
	assert.Equal(t, LockStatusLockAcquired, locked)
	assert.Nil(t, value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_getCachedValueOrLock_LockedToVal(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetVal("")
	mock.ExpectGet(key).SetVal("valid-value")

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.NoError(t, err)
	assert.Equal(t, LockStatusUnlocked, locked)
	assert.Equal(t, []byte("valid-value"), value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_getCachedValueOrLock_LockWaitExpired(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Millisecond * 100,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetVal("")

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.Error(t, err)
	assert.Equal(t, LockStatusLockFailed, locked)
	assert.Nil(t, value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_getCachedValueOrLock_LockContextExpired(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Millisecond*100))
	defer cancel()

	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectGet(key).SetVal("")

	value, locked, err := c.getCachedValueOrLock(ctx, key, c.opts, false, LockModeDefault)

	assert.Error(t, err)
	assert.Equal(t, LockStatusLockFailed, locked)
	assert.Nil(t, value)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_UnlockCache_ValidCacheExist(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectWatch(key)
	mock.ExpectGet(key).SetVal("valid-value")

	err := c.unlockCache(ctx, key)

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_UnlockCache_KeyDoesNotExist(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectWatch(key)
	mock.ExpectGet(key).SetErr(redis.Nil)

	err := c.unlockCache(ctx, key)

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_UnlockCache_EmptyValue(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectWatch(key)
	mock.ExpectGet(key).SetVal("")
	mock.ExpectDel(key).SetVal(1)

	err := c.unlockCache(ctx, key)

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_UnlockCache_RedisError(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"

	mock.ExpectWatch(key)
	mock.ExpectGet(key).SetErr(errors.New("redis error"))

	err := c.unlockCache(ctx, key)

	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_SaveToCache_Success(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"
	value := []byte("test-value")
	opts := CacheOptions{TTL: time.Minute}

	mock.ExpectSet(key, value, opts.TTL).SetVal("OK")

	err := c.saveToCache(ctx, key, value, opts)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_SaveToCache_Error(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	c := &RedisCache{
		connection: mockRedis,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	key := "test-key"
	value := []byte("test-value")
	opts := CacheOptions{TTL: time.Minute}

	mock.ExpectSet(key, value, opts.TTL).SetErr(errors.New("redis error"))

	err := c.saveToCache(ctx, key, value, opts)
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}
