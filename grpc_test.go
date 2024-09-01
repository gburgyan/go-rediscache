package rediscache

/*
import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	"google.golang.org/protobuf/proto"
	"reflect"
	"testing"
	"time"
)

func Test_GRPCSerializer(t *testing.T) {
	// Create a sample Address message
	address := &grpc_binarylog_v1.Address{
		Type:    grpc_binarylog_v1.Address_TYPE_IPV4,
		Address: "172.168.1.1",
		IpPort:  80,
	}

	// Serialize the Address message
	serializedData, err := GRPCSerializer(address)
	assert.NoError(t, err)
	assert.NotEmpty(t, serializedData)
}

func Test_GRPCDeserializer(t *testing.T) {
	// Create a sample Address message
	address := &grpc_binarylog_v1.Address{
		Type:    grpc_binarylog_v1.Address_TYPE_IPV4,
		Address: "172.168.1.1",
		IpPort:  80,
	}

	// Serialize the Address message
	serializedData, err := GRPCSerializer(address)
	assert.NoError(t, err)

	// Deserialize the serialized data
	deserializedData, err := GRPCDeserializer(reflect.TypeOf(address), serializedData)
	assert.NoError(t, err)

	// Verify that the deserialized message matches the original Address message
	deserializedAddress, ok := deserializedData.(*grpc_binarylog_v1.Address)
	assert.True(t, ok)
	assert.True(t, proto.Equal(address, deserializedAddress))
}

func Test_GRPC_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Create a sample Address message
	address := &grpc_binarylog_v1.Address{
		Type:    grpc_binarylog_v1.Address_TYPE_IPV4,
		Address: "127.0.0.1",
		IpPort:  80,
	}

	// Mock redis
	mockRedis, mock := redismock.NewClientMock()

	now := time.Now()

	c := NewRedisCache(ctx, mockRedis, CacheOptions{
		TTL:               time.Minute,
		LockTTL:           time.Second * 10,
		LockWait:          time.Second * 10,
		LockRetry:         time.Millisecond * 200,
		KeyPrefix:         "GoCache-",
		EnableTiming:      false,
		EncryptionHandler: nil,
		now:               func() time.Time { return now },
	})

	c.RegisterTypeHandler(reflect.TypeOf(address), GRPCSerializer, GRPCDeserializer)

	callCount := 0

	cachedFunc := Cache(c, func(a *grpc_binarylog_v1.Address) *grpc_binarylog_v1.Address {
		callCount++
		return a
	})

	key := "GoCache-471f14576ab72e82c6544ecace2f2c1da93aa16f4f7a893df27be63e0f2dd265"

	objMarshalled, _ := proto.Marshal(address)
	nowMarshalled, _ := now.MarshalBinary()
	bytes, _ := combineBytes([][]byte{objMarshalled, nowMarshalled})

	mock.ExpectGet(key).SetErr(redis.Nil)
	mock.ExpectSetNX(key, "", time.Second*10).SetVal(true)
	mock.ExpectSet(key, bytes, time.Minute).SetVal("OK")

	out := cachedFunc(address)
	time.Sleep(time.Millisecond * 100)

	assert.Equal(t, 1, callCount)
	assert.True(t, proto.Equal(address, out))
	assert.NoError(t, mock.ExpectationsWereMet())
	mock.ClearExpect()

	mock.ExpectGet(key).SetVal(string(bytes))

	out = cachedFunc(address)
	time.Sleep(time.Millisecond * 100)

	assert.Equal(t, 1, callCount)
	assert.True(t, proto.Equal(address, out))

	assert.NoError(t, mock.ExpectationsWereMet())
}

*/
