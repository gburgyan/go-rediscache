package go_rediscache

import "context"

type CacheGetter[K any, T any] func(context.Context, K) (T, error)
type CacheGetter2[K1 any, K2 any, T any] func(context.Context, K1, K2) (T, error)

func Cache1[K1 any, T any](c *RedisCache, getter CacheGetter[K1, T]) CacheGetter[K1, T] {
	ret := c.Cached(getter)
	return ret.(func(context.Context, K1) (T, error))
}

func Cache2[K1 any, K2 any, T Serializable](c *RedisCache, getter CacheGetter2[K1, K2, T]) CacheGetter2[K1, K2, T] {
	return c.Cached(getter).(CacheGetter2[K1, K2, T])
}
