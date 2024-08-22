package rediscache

import (
	"context"
	"errors"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"reflect"
	"testing"
	"time"
)

func typesForArgs(args []reflect.Value) []reflect.Type {
	types := make([]reflect.Type, len(args))
	for i, arg := range args {
		types[i] = arg.Type()
	}
	return types
}

func Test_KeyForArgs_ContextFirstArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf("arg1")}
	returnTypes := "string"

	inputHandlers := r.makeInputValueHandlers(typesForArgs(args))
	key := keyForArgs(inputHandlers, args, returnTypes)

	assert.Equal(t, "arg1/string", key)
}

type paramStringer struct {
	Value string
}

func (p paramStringer) String() string {
	return p.Value
}

type testKeyable struct {
	Value string
}

func (t testKeyable) CacheKey() string {
	return t.Value
}

func Test_KeyForArgs_KeyableArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	keyable := testKeyable{Value: "keyableValue"}
	args := []reflect.Value{reflect.ValueOf(keyable)}
	returnTypes := "string"

	inputHandlers := r.makeInputValueHandlers(typesForArgs(args))
	key := keyForArgs(inputHandlers, args, returnTypes)

	assert.Equal(t, "keyableValue/string", key)
}

func Test_KeyForArgs_StringArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf("stringValue")}
	returnTypes := "string"

	inputHandlers := r.makeInputValueHandlers(typesForArgs(args))
	key := keyForArgs(inputHandlers, args, returnTypes)

	assert.Equal(t, "stringValue/string", key)
}

func Test_KeyForArgs_InvalidArgumentType(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf(123)}
	returnTypes := "string"

	assert.Panics(t, func() {
		inputHandlers := r.makeInputValueHandlers(typesForArgs(args))
		_ = keyForArgs(inputHandlers, args, returnTypes)
	})
}

func Test_KeyForArgs_MultipleArguments(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf("arg1"), reflect.ValueOf("arg2")}
	returnTypes := "string"

	inputHandlers := r.makeInputValueHandlers(typesForArgs(args))
	key := keyForArgs(inputHandlers, args, returnTypes)

	assert.Equal(t, "arg1:arg2/string", key)
}

func Test_ShouldPreRefresh_RefreshTTLIsNoRefreshTTL(t *testing.T) {
	cfc := cacheFunctionConfig{
		funcOpts: CacheOptions{
			RefreshPercentage: 1,
		},
	}
	savedTime := time.Now()
	result := cfc.shouldPreRefresh(savedTime)
	assert.False(t, result)
}

func Test_ShouldPreRefresh_RefreshTTLIsZero(t *testing.T) {
	cfc := cacheFunctionConfig{
		funcOpts: CacheOptions{
			RefreshPercentage: 1,
		},
	}
	savedTime := time.Now()
	result := cfc.shouldPreRefresh(savedTime)
	assert.False(t, result)
}

func Test_ShouldPreRefresh_RefreshTimeIsAfterNow(t *testing.T) {
	cfc := cacheFunctionConfig{
		funcOpts: CacheOptions{
			TTL:               1 * time.Hour,
			RefreshPercentage: 1,
			now:               func() time.Time { return time.Now().Add(-2 * time.Hour) },
		},
	}
	savedTime := time.Now()
	result := cfc.shouldPreRefresh(savedTime)
	assert.False(t, result)
}

func Test_ShouldPreRefresh_RefreshTimeIsBeforeNow(t *testing.T) {
	cfc := cacheFunctionConfig{
		funcOpts: CacheOptions{
			TTL:               1 * time.Hour,
			RefreshPercentage: 0.8,
			now:               time.Now,
		},
	}
	savedTime := time.Now().Add(-2 * time.Hour)
	result := cfc.shouldPreRefresh(savedTime)
	assert.True(t, result)
}

func Test_Prefetch_Already_Locked(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	opts := CacheOptions{
		LockTTL: 10 * time.Second,
	}

	cache := NewRedisCache(ctx, mockRedis, opts)
	cfc := cache.setupCacheFunctionConfig(func(s string) (string, error) {
		return s, nil
	}, opts)

	key := "key-refresh"

	mock.ExpectSetNX(key, "", opts.LockTTL).SetErr(errors.New("already locked"))

	cfc.doBackgroundRefresh(ctx, "key", reflect.ValueOf("value"))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_Prefetch_Success(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	opts := CacheOptions{
		LockTTL: 10 * time.Second,
		TTL:     5 * time.Minute,
		now:     func() time.Time { return time.Time{} },
	}

	cache := NewRedisCache(ctx, mockRedis, opts)
	cfc := cache.setupCacheFunctionConfig(func(s string) (string, error) {
		return s, nil
	}, opts)

	key := "key"
	refreshKey := key + "-refresh"
	zeroTimeBytes, _ := time.Time{}.MarshalBinary()
	cacheVal, _ := combineBytes([][]byte{[]byte("value"), {}, zeroTimeBytes})

	mock.ExpectSetNX(refreshKey, "", opts.LockTTL).SetVal(true)
	mock.ExpectSet(key, cacheVal, opts.TTL).SetVal("OK")
	mock.ExpectDel(refreshKey).SetVal(int64(1))

	cfc.doBackgroundRefresh(ctx, "key", reflect.ValueOf("value"))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_Prefetch_FuncError(t *testing.T) {
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	opts := CacheOptions{
		LockTTL: 10 * time.Second,
		TTL:     5 * time.Minute,
		now:     func() time.Time { return time.Time{} },
	}

	cache := NewRedisCache(ctx, mockRedis, opts)
	cfc := cache.setupCacheFunctionConfig(func(s string) (string, error) {
		return "", errors.New("error")
	}, opts)

	key := "key"
	refreshKey := key + "-refresh"

	mock.ExpectSetNX(refreshKey, "", opts.LockTTL).SetVal(true)
	mock.ExpectDel(refreshKey).SetVal(int64(1))

	cfc.doBackgroundRefresh(ctx, "key", reflect.ValueOf("value"))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_preRefreshFactor_Zero(t *testing.T) {
	opts := CacheOptions{
		RefreshPercentage: .8, // 48 minutes for 1 hour TTL
		TTL:               time.Hour,
		LockTTL:           time.Minute,
		now: func() time.Time {
			return time.Time{}
		},
	}
	cfc := cacheFunctionConfig{
		funcOpts: opts,
	}
	cfc.slope, cfc.intercept = calculatePreRefreshCoefficients(opts)
	// Realistically, this test should be "is factor less than zero", but let's be explicit.
	assert.InDelta(t, -4.363636, cfc.preRefreshFactor(time.Time{}), 0.0001)
}

func Test_preRefreshFactor_AtRefreshInstant(t *testing.T) {
	opts := CacheOptions{
		RefreshPercentage: .8, // 48 minutes for 1 hour TTL
		TTL:               time.Hour,
		LockTTL:           time.Minute,
		now: func() time.Time {
			return time.Time{}.Add(48 * time.Minute)
		},
	}
	cfc := cacheFunctionConfig{
		funcOpts: opts,
	}
	cfc.slope, cfc.intercept = calculatePreRefreshCoefficients(opts)
	assert.InDelta(t, 0.0, cfc.preRefreshFactor(time.Time{}), 0.0001)
}

func Test_preRefreshFactor_AtEndTTL(t *testing.T) {
	opts := CacheOptions{
		RefreshPercentage: .8, // 48 minutes for 1 hour TTL
		TTL:               time.Hour,
		LockTTL:           time.Minute,
		now: func() time.Time {
			return time.Time{}.Add(59 * time.Minute)
		},
	}
	cfc := cacheFunctionConfig{
		funcOpts: opts,
	}
	cfc.slope, cfc.intercept = calculatePreRefreshCoefficients(opts)
	assert.InDelta(t, 1.0, cfc.preRefreshFactor(time.Time{}), 0.0001)
}
