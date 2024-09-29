package rediscache

import (
	"context"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"github.com/gburgyan/go-timing"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

type resultSerializable struct {
	Value string
}

func (r resultSerializable) Serialize() ([]byte, error) {
	return []byte(r.Value), nil
}

func (r resultSerializable) Deserialize(data []byte) (any, error) {
	return resultSerializable{string(data)}, nil
}

type nullEncryptor struct{}

func (n nullEncryptor) Encrypt(data []byte) ([]byte, error) {
	return data, nil
}

func (n nullEncryptor) Decrypt(data []byte) ([]byte, error) {
	return data, nil
}

func TestCache_HappyCase(t *testing.T) {
	mini := miniredis.RunT(t)

	ctx := context.Background()
	timingCtx := timing.Root(ctx)

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: mini.Addr(),
	})

	c := NewRedisCache(ctx, redisConnection, CacheOptions{
		TTL:          time.Minute,
		LockTTL:      time.Minute,
		LockWait:     time.Second * 10,
		LockRetry:    time.Millisecond * 200,
		KeyPrefix:    "GoCache-",
		EnableTiming: true,
	})

	f := func(ctx context.Context, s string) (resultSerializable, error) {
		return resultSerializable{s}, nil
	}

	cf := Cache(c, f)
	s, err := cf(timingCtx, "test")

	time.Sleep(time.Millisecond * 500)

	assert.NoError(t, err)
	assert.Equal(t, "test", s.Value)

	fmt.Println(timingCtx.String())
}

func TestCache_MultipleCalls(t *testing.T) {
	mini := miniredis.RunT(t)

	ctx := context.Background()
	timingCtx := timing.Root(ctx)

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: mini.Addr(),
	})

	c := NewRedisCache(ctx, redisConnection, CacheOptions{
		TTL:          time.Minute,
		LockTTL:      time.Minute,
		LockWait:     time.Second * 10,
		LockRetry:    time.Millisecond * 200,
		KeyPrefix:    "GoCache-",
		EnableTiming: true,
	})

	callCount := 0
	f := func(ctx context.Context, s string) (resultSerializable, error) {
		callCount++
		time.Sleep(time.Millisecond * 500)
		return resultSerializable{s}, nil
	}

	cf := Cache(c, f)

	startTime := time.Now()

	runAllAndWait(
		func() {
			fTiming, complete := timing.Start(timingCtx, "func1")
			defer complete()

			s, err := cf(fTiming, "test")
			assert.NoError(t, err)
			assert.Equal(t, "test", s.Value)
		},
		func() {
			time.Sleep(time.Millisecond * 100)
			fTiming, complete := timing.Start(timingCtx, "func2")
			defer complete()

			s, err := cf(fTiming, "test")
			assert.NoError(t, err)
			assert.Equal(t, "test", s.Value)
		},
	)

	elapsed := time.Since(startTime)

	fmt.Println(timingCtx.String())
	assert.Equal(t, 1, callCount)

	// This is to ensure that the lock waiter channel is working correctly. The save from the first
	// call should immediately be available to the second call via the waiter channel. Without this
	// working correctly, it would take an extra spin and take at least 600 ms.
	assert.InDelta(t, 500, elapsed.Milliseconds(), 50)
}

func TestFullIntegration(t *testing.T) {
	ctx := context.Background()

	funcCallCount := 0
	f := func(ctx context.Context, s string) (string, error) {
		funcCallCount++
		if s == "error" {
			return "", assert.AnError
		}
		return "func " + s, nil
	}

	// Mock redis
	mockRedis, mock := redismock.NewClientMock()

	c := NewRedisCache(ctx, mockRedis, CacheOptions{
		TTL:               time.Minute,
		LockTTL:           time.Minute,
		LockWait:          time.Second * 10,
		LockRetry:         time.Millisecond * 200,
		KeyPrefix:         "GoCache-",
		EnableTiming:      true,
		EncryptionHandler: nullEncryptor{},
		now:               func() time.Time { return time.Date(2024, 8, 21, 23, 22, 0, 0, time.UTC) },
	})

	cf := Cache(c, f)
	key := "GoCache-1a1ef8b8c32dca4c95120e68a223b5d58f7aa85af557f08baa19e2d20ce0c592"

	// First call -- nothing in cache
	mock.ExpectGet(key).SetErr(redis.Nil)
	mock.ExpectSetNX(key, "", time.Minute).SetVal(true)
	mock.ExpectSet(key, []byte{9, 0, 0, 0, 102, 117, 110, 99, 32, 116, 101, 115, 116, 0, 0, 0, 0, 15, 0, 0, 0, 1, 0, 0, 0, 14, 222, 88, 109, 152, 0, 0, 0, 0, 255, 255}, time.Minute).SetVal("OK")
	timingCtx := timing.Root(ctx)
	s, err := cf(timingCtx, "test")
	time.Sleep(time.Millisecond * 10)

	assert.NoError(t, err)
	assert.Equal(t, "func test", s)
	assert.Equal(t, 1, funcCallCount)
	assert.NoError(t, mock.ExpectationsWereMet())

	assert.Contains(t, timingCtx.Children, "redis-cache:string")
	assert.True(t, timingCtx.Children["redis-cache:string"].Children["redis"].Details["cache-miss"].(bool))

	fmt.Println(timingCtx.String())
	fmt.Println()

	mock.ClearExpect()

	// Second call -- cache hit
	mock.ExpectGet(key).SetVal(string([]byte{9, 0, 0, 0, 102, 117, 110, 99, 32, 116, 101, 115, 116, 0, 0, 0, 0, 15, 0, 0, 0, 1, 0, 0, 0, 14, 222, 88, 109, 152, 0, 0, 0, 0, 255, 255}))

	timingCtx = timing.Root(ctx)
	s, err = cf(timingCtx, "test")
	fmt.Println(timingCtx.String())
	fmt.Println()

	time.Sleep(time.Millisecond * 10)

	assert.NoError(t, err)
	assert.Equal(t, "func test", s)
	assert.Equal(t, 1, funcCallCount)
	assert.NoError(t, mock.ExpectationsWereMet())

	assert.True(t, timingCtx.Children["redis-cache:string"].Children["redis"].Details["cache-hit"].(bool))

	mock.ClearExpect()

	// Third call -- cache hit after a lock
	mock.ExpectGet(key).SetVal(string([]byte{}))
	mock.ExpectGet(key).SetVal(string([]byte{9, 0, 0, 0, 102, 117, 110, 99, 32, 116, 101, 115, 116, 0, 0, 0, 0, 15, 0, 0, 0, 1, 0, 0, 0, 14, 222, 88, 109, 152, 0, 0, 0, 0, 255, 255}))

	timingCtx = timing.Root(ctx)
	s, err = cf(timingCtx, "test")
	fmt.Println(timingCtx.String())
	fmt.Println()

	time.Sleep(time.Millisecond * 10)

	assert.NoError(t, err)
	assert.Equal(t, "func test", s)
	assert.Equal(t, 1, funcCallCount)
	assert.NoError(t, mock.ExpectationsWereMet())

	assert.True(t, timingCtx.Children["redis-cache:string"].Children["redis"].Details["cache-hit"].(bool))
	assert.Equal(t, 1, timingCtx.Children["redis-cache:string"].Children["redis"].Details["spins"].(int))

	mock.ClearExpect()

	// Fourth call -- error
	errorKey := "GoCache-7a7ad968290894a75a20f6ffaa17bdb0b77578651509ba7d7eb2a052d4880f99" // Different inputs, hence different key.

	mock.ExpectGet(errorKey).SetErr(redis.Nil)
	mock.ExpectSetNX(errorKey, "", time.Minute).SetVal(true)
	mock.ExpectWatch(errorKey)
	mock.ExpectGet(errorKey).SetVal("")
	mock.ExpectDel(errorKey).SetVal(1)

	timingCtx = timing.Root(ctx)
	s, err = cf(timingCtx, "error")
	fmt.Println(timingCtx.String())
	fmt.Println()

	time.Sleep(time.Millisecond * 10)

	assert.Error(t, err)
	assert.Equal(t, "", s)
	assert.Equal(t, 2, funcCallCount)
	assert.NoError(t, mock.ExpectationsWereMet())

	mock.ClearExpect()

	// Fifth call -- a hit, but with a custom timing name.
	cfName := CacheOpts(c, f, CacheOptions{CustomTimingName: "custom-timing-name"})
	mock.ExpectGet(key).SetVal(string([]byte{9, 0, 0, 0, 102, 117, 110, 99, 32, 116, 101, 115, 116, 0, 0, 0, 0, 15, 0, 0, 0, 1, 0, 0, 0, 14, 222, 88, 109, 152, 0, 0, 0, 0, 255, 255}))

	timingCtx = timing.Root(ctx)
	s, err = cfName(timingCtx, "test")
	fmt.Println(timingCtx.String())
	fmt.Println()

	time.Sleep(time.Millisecond * 10)

	assert.NoError(t, err)
	assert.Equal(t, "func test", s)
	assert.NoError(t, mock.ExpectationsWereMet())

	assert.True(t, timingCtx.Children["custom-timing-name"].Children["redis"].Details["cache-hit"].(bool))

	mock.ClearExpect()

}
