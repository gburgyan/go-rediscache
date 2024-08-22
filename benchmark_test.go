package rediscache

import (
	"context"
	"github.com/gburgyan/go-timing"
	"github.com/go-redis/redismock/v8"
	"testing"
)

func Benchmark_CreateCacheFunction_StringString(b *testing.B) {
	fn := func(s string) string {
		return s
	}

	opts := CacheOptions{}
	ctx := context.Background()
	mockRedis, _ := redismock.NewClientMock()

	cache := NewRedisCache(ctx, mockRedis, opts)

	for i := 0; i < b.N; i++ {
		_ = Cache(cache, fn)
	}
}

func Benchmark_CacheHit(b *testing.B) {
	fn := func(s string) string {
		return s
	}

	opts := CacheOptions{}
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	cache := NewRedisCache(ctx, mockRedis, opts)

	cfn := Cache(cache, fn)
	key := "GoCache-2631e43346c3d8b4a341e560ec2b609b6db0250448ce49a048f4197e8558cc3b"
	serVal, _ := combineBytes([][]byte{[]byte("value")})
	val := string(serVal)

	for i := 0; i < b.N; i++ {
		mock.ClearExpect()
		mock.ExpectGet(key).SetVal(val)
		_ = cfn("test-key")
	}
}

func Benchmark_CacheHit_Timing(b *testing.B) {
	fn := func(_ context.Context, s string) string {
		return s
	}

	opts := CacheOptions{}
	ctx := context.Background()
	mockRedis, mock := redismock.NewClientMock()

	cache := NewRedisCache(ctx, mockRedis, opts)

	cfn := Cache(cache, fn)
	key := "GoCache-2631e43346c3d8b4a341e560ec2b609b6db0250448ce49a048f4197e8558cc3b"
	serVal, _ := combineBytes([][]byte{[]byte("value")})
	val := string(serVal)

	for i := 0; i < b.N; i++ {
		timingCtx := timing.Root(ctx)
		mock.ClearExpect()
		mock.ExpectGet(key).SetVal(val)
		_ = cfn(timingCtx, "test-key")
	}
}
