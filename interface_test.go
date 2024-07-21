package go_rediscache

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"testing"
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
		connection: redisConnection.Conn(ctx),
	}

	f := func(ctx context.Context, s string) (resultSerializable, error) {
		return resultSerializable{s}, nil
	}

	cf := Cache1(c, f)
	s, err := cf(ctx, "test")

	assert.NoError(t, err)
	assert.Equal(t, "test", s.Value)
}
