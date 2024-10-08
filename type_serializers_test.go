package rediscache

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"reflect"
	"testing"
	"time"
)

func Test_JsonSerializer(t *testing.T) {
	ctx := context.Background()

	type testStruct struct {
		Name string
		Age  int
	}
	testStructType := reflect.TypeOf((*testStruct)(nil)).Elem()

	structResultFunc := func(in testStruct) testStruct {
		return in
	}

	// Mock redis
	mockRedis, mock := redismock.NewClientMock()

	c := NewRedisCache(ctx, mockRedis, CacheOptions{
		TTL:               time.Minute,
		LockTTL:           time.Minute,
		LockWait:          time.Second * 10,
		LockRetry:         time.Millisecond * 200,
		KeyPrefix:         "GoCache-",
		EnableTiming:      false,
		EncryptionHandler: nil,
		now:               func() time.Time { return time.Time{} },
	})

	c.RegisterTypeHandler(testStructType, JsonSerializer, JsonDeserializer)

	cachedFunc := Cache(c, structResultFunc)

	in := testStruct{
		Name: "Bob Dobbs",
		Age:  42,
	}

	key := "GoCache-16a3c53e35dfc078f98d413eba6f9e1c2d275ea37e5dcb170cb78e8524ca620d"
	cacheContents := `{"Name":"Bob Dobbs","Age":42}`
	zeroTime := time.Time{}
	zeroTimeBytes, _ := zeroTime.MarshalBinary()
	cacheVal, _ := combineBytes([][]byte{[]byte(cacheContents), zeroTimeBytes})

	mock.ExpectGet(key).SetErr(redis.Nil)
	mock.ExpectSet(key, cacheVal, time.Minute).SetVal("OK")

	out := cachedFunc(in)
	time.Sleep(time.Millisecond * 10)

	assert.Equal(t, in, out)
	assert.NoError(t, mock.ExpectationsWereMet())

	mock.ClearExpect()

	mock.ExpectGet(key).SetVal(string(cacheVal))

	out = cachedFunc(in)
	time.Sleep(time.Millisecond * 10)

	assert.Equal(t, in, out)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_JsonSerializer_Pointers(t *testing.T) {
	ctx := context.Background()

	type testStruct struct {
		Name string
		Age  int
	}
	testStructPtrType := reflect.TypeOf((*testStruct)(nil))

	structResultFunc := func(in *testStruct) *testStruct {
		return in
	}

	// Mock redis
	mockRedis, mock := redismock.NewClientMock()

	c := NewRedisCache(ctx, mockRedis, CacheOptions{
		TTL:               time.Minute,
		LockTTL:           time.Minute,
		LockWait:          time.Second * 10,
		LockRetry:         time.Millisecond * 200,
		KeyPrefix:         "GoCache-",
		EnableTiming:      false,
		EncryptionHandler: nil,
		now:               func() time.Time { return time.Time{} },
	})

	c.RegisterTypeHandler(testStructPtrType, JsonSerializer, JsonDeserializer)

	cachedFunc := Cache(c, structResultFunc)

	in := testStruct{
		Name: "Bob Dobbs",
		Age:  42,
	}

	key := "GoCache-4a6f895bdcb69a054143f49c112caf763f0c85535b86e5202a7bfb38dfe29d43"
	cacheContents := `{"Name":"Bob Dobbs","Age":42}`
	zeroTime := time.Time{}
	zeroTimeBytes, _ := zeroTime.MarshalBinary()
	cacheVal, _ := combineBytes([][]byte{[]byte(cacheContents), zeroTimeBytes})

	mock.ExpectGet(key).SetErr(redis.Nil)
	mock.ExpectSet(key, cacheVal, time.Minute).SetVal("OK")

	out := cachedFunc(&in)
	time.Sleep(time.Millisecond * 10)

	assert.Equal(t, in, *out)
	assert.NoError(t, mock.ExpectationsWereMet())

	mock.ClearExpect()

	mock.ExpectGet(key).SetVal(string(cacheVal))

	out = cachedFunc(&in)
	time.Sleep(time.Millisecond * 10)

	assert.Equal(t, in, *out)
	assert.NoError(t, mock.ExpectationsWereMet())
}
