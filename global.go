package rediscache

// Cache wraps the provided function with caching logic using default cache options.
//
// Parameters:
// - c: A pointer to the RedisCache instance.
// - f: The function to be wrapped with caching logic. It should be of any type.
//
// Returns:
// - The wrapped function with caching logic.
func Cache[F any](c *RedisCache, f F) F {
	return c.Cached(f).(F)
}

// CacheOpts wraps the provided function with caching logic using the specified cache options.
//
// Parameters:
// - c: A pointer to the RedisCache instance.
// - f: The function to be wrapped with caching logic. It should be of any type.
// - funcOpts: CacheOptions containing the configuration for caching behavior.
//
// Returns:
// - The wrapped function with caching logic.
func CacheOpts[F any](c *RedisCache, f F, funcOpts CacheOptions) F {
	return c.CachedOpts(f, funcOpts).(F)
}
