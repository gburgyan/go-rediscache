package rediscache

import (
	"context"
	"github.com/go-redis/redis/v8"
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

func TestCache1(t *testing.T) {
	ctx := context.Background()

	// Open a connection to Redis locally
	redisConnection := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	c := &RedisCache{
		connection: redisConnection,
		opts: CacheOptions{
			TTL:       time.Minute,
			LockTTL:   time.Minute,
			LockWait:  time.Second * 10,
			LockRetry: time.Millisecond * 200,
			KeyPrefix: "GoCache-",
		},
	}

	f := func(ctx context.Context, s string) (resultSerializable, error) {
		return resultSerializable{s}, nil
	}

	cf := Cache(c, f)
	s, err := cf(ctx, "test")

	time.Sleep(time.Millisecond * 500)

	assert.NoError(t, err)
	assert.Equal(t, "test", s.Value)
}
