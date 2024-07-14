package go_rediscache

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

type resultSerializable struct {
	Value string
}

func (r resultSerializable) Serialize() ([]byte, error) {
	return []byte(r.Value), nil
}

func (r resultSerializable) Deserialize(data []byte, v *any) error {
	// Make new string
	*v = string(data)
	return nil
}

func TestCache1(t *testing.T) {
	c := &RedisCache{}

	f := func(ctx context.Context, s string) (resultSerializable, error) {
		return resultSerializable{s}, nil
	}

	cf := Cache1(c, f)
	s, err := cf(context.Background(), "test")

	assert.NoError(t, err)
	assert.Equal(t, "test", s.Value)
}
