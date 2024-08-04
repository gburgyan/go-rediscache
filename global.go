package rediscache

func Cache[F any](c *RedisCache, f F) F {
	return c.Cached(f).(F)
}

func CacheOpts[F any](c *RedisCache, f F, funcOpts CacheOptions) F {
	return c.CachedOpts(f, funcOpts).(F)
}
