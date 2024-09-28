package rediscache

import (
	"context"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"github.com/gburgyan/go-timing"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"reflect"
	"testing"
	"time"
)

type sliceResults struct {
	Value string
}

var sliceResultsType = reflect.TypeOf((*sliceResults)(nil)).Elem()

func TestSliceReal(t *testing.T) {
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
		now:          time.Now,
	})
	c.RegisterTypeHandler(sliceResultsType, JsonSerializer, JsonDeserializer)

	callcount := 0
	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		callcount++
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
		}
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	s, err := cf(timingCtx, []string{"test1", "test2", "test3", "test4"})

	assert.NoError(t, err)
	assert.Equal(t, "processed:test1", s[0].Result.Value)
	assert.Equal(t, 1, callcount)

	fmt.Println(timingCtx.String())
	timingCtx = timing.Root(ctx)

	time.Sleep(time.Millisecond * 100)

	s, err = cf(timingCtx, []string{"test1", "test2", "test3", "test4"})
	assert.Equal(t, "processed:test1", s[0].Result.Value)
	assert.Equal(t, 1, callcount)

	fmt.Println(timingCtx.String())
}

func runAllAndWait(funcs ...func()) {
	done := make(chan struct{})
	for _, f := range funcs {
		go func(f func()) {
			f()
			done <- struct{}{}
		}(f)
	}
	for range funcs {
		<-done
	}
}

func TestSliceMultiAccess_LockWait(t *testing.T) {
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
		LockRetry:    time.Millisecond * 100,
		KeyPrefix:    "GoCache-",
		EnableTiming: true,
		now:          time.Now,
	})
	c.RegisterTypeHandler(sliceResultsType, JsonSerializer, JsonDeserializer)

	callcount := 0
	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		callcount++
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
		}
		time.Sleep(time.Millisecond * 500)
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	runAllAndWait(
		func() {
			fctx, complete := timing.Start(timingCtx, "f1")
			s, err := cf(fctx, []string{"test1", "test2", "test3", "test4"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
		func() {
			time.Sleep(time.Millisecond * 100)
			fctx, complete := timing.Start(timingCtx, "f2")
			s, err := cf(fctx, []string{"test1", "test2", "test3", "test4"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
	)

	fmt.Println(timingCtx.String())

	assert.Equal(t, 1, callcount)

}

func TestSliceMultiAccess_NewVal(t *testing.T) {
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
		LockRetry:    time.Millisecond * 100,
		KeyPrefix:    "GoCache-",
		EnableTiming: true,
		now:          time.Now,
	})
	c.RegisterTypeHandler(sliceResultsType, JsonSerializer, JsonDeserializer)

	callcount := 0
	processedItems := 0
	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		callcount++
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
			processedItems++
		}
		time.Sleep(time.Millisecond * 500)
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	f0ctx, complete := timing.Start(timingCtx, "f0")
	s, err := cf(f0ctx, []string{"test1"})
	assert.NoError(t, err)
	assert.Equal(t, "processed:test1", s[0].Result.Value)
	complete()
	time.Sleep(time.Millisecond * 100)

	runAllAndWait(
		func() {
			fctx, complete := timing.Start(timingCtx, "f1")
			s, err := cf(fctx, []string{"test1", "test2", "test3"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
		func() {
			time.Sleep(time.Millisecond * 100)
			fctx, complete := timing.Start(timingCtx, "f2")
			s, err := cf(fctx, []string{"test1", "test2", "test3", "test4"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			assert.Equal(t, "processed:test4", s[3].Result.Value)
			complete()
		},
	)

	fmt.Println(timingCtx.String())

	assert.Equal(t, 3, callcount)
	assert.Equal(t, 4, processedItems)
}

func TestSliceMultiAccess_NewValRefresh(t *testing.T) {
	mini := miniredis.RunT(t)

	ctx := context.Background()
	timingCtx := timing.Root(ctx)

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: mini.Addr(),
	})

	c := NewRedisCache(ctx, redisConnection, CacheOptions{
		TTL:                time.Minute,
		LockTTL:            time.Minute,
		LockWait:           time.Second * 10,
		LockRetry:          time.Millisecond * 100,
		KeyPrefix:          "GoCache-",
		EnableTiming:       true,
		RefreshEntireBatch: true,
		now:                time.Now,
	})
	c.RegisterTypeHandler(sliceResultsType, JsonSerializer, JsonDeserializer)

	callcount := 0
	itemCount := 0
	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		callcount++
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
			itemCount++
		}
		time.Sleep(time.Millisecond * 500)
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	runAllAndWait(
		func() {
			fctx, complete := timing.Start(timingCtx, "f1")
			s, err := cf(fctx, []string{"test1", "test2", "test3"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
		func() {
			time.Sleep(time.Millisecond * 100)
			fctx, complete := timing.Start(timingCtx, "f2")
			s, err := cf(fctx, []string{"test1", "test2", "test3", "test4"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			assert.Equal(t, "processed:test4", s[3].Result.Value)
			complete()
		},
	)

	fmt.Println(timingCtx.String())

	assert.Equal(t, 2, callcount)
	assert.Equal(t, 7, itemCount)
}

func TestSliceMultiAccess_Timeout(t *testing.T) {
	mini := miniredis.RunT(t)

	ctx := context.Background()
	timingCtx := timing.Root(ctx)

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: mini.Addr(),
	})

	c := NewRedisCache(ctx, redisConnection, CacheOptions{
		TTL:                time.Minute,
		LockTTL:            time.Minute,
		LockWait:           time.Millisecond * 500,
		LockRetry:          time.Millisecond * 100,
		KeyPrefix:          "GoCache-",
		EnableTiming:       true,
		RefreshEntireBatch: false,
		now:                time.Now,
	})
	c.RegisterTypeHandler(sliceResultsType, JsonSerializer, JsonDeserializer)

	callCount := 0
	itemCount := 0
	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		callCount++
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
			itemCount++
		}
		time.Sleep(time.Millisecond * 1000)
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	runAllAndWait(
		func() {
			fctx, complete := timing.Start(timingCtx, "f1")
			s, err := cf(fctx, []string{"test1", "test2"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
		func() {
			time.Sleep(time.Millisecond * 100)
			fctx, complete := timing.Start(timingCtx, "f2")
			s, err := cf(fctx, []string{"test1", "test2"})
			assert.NoError(t, err)
			assert.Equal(t, "processed:test1", s[0].Result.Value)
			complete()
		},
	)

	fmt.Println(timingCtx.String())

	assert.Equal(t, 3, callCount)
	assert.Equal(t, 4, itemCount)
}
