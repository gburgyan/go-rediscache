package rediscache

import (
	"context"
	"fmt"
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
	ctx := context.Background()
	timingCtx := timing.Root(ctx)

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
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

	f := func(ctx context.Context, in []string) ([]sliceResults, error) {
		var results []sliceResults
		for _, s := range in {
			results = append(results, sliceResults{"processed:" + s})
		}
		return results, nil
	}

	cf := CacheBulkSlice(c, f)

	s, err := cf(timingCtx, []string{"test1", "test2", "test3", "test4"})

	time.Sleep(time.Millisecond * 500)

	assert.NoError(t, err)
	assert.Equal(t, "processed:test1", s[0].Result.Value)

	fmt.Println(timingCtx.String())
}
