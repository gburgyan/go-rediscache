package rediscache

import (
	"context"
	"github.com/stretchr/testify/assert"
	"reflect"
	"testing"
)

func Test_KeyForArgs_ContextFirstArgument(t *testing.T) {
	r := &RedisCache{}
	args := []reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf("arg1")}
	returnTypes := "string"
	opts := CacheOptions{}

	key := r.keyForArgs(args, returnTypes, opts)

	assert.Equal(t, "arg1/string", key)
}

type paramStringer struct {
	Value string
}

func (p paramStringer) String() string {
	return p.Value
}

func Test_KeyForArgs_StringerArgument(t *testing.T) {
	r := &RedisCache{}
	stringer := paramStringer{Value: "stringerValue"}
	args := []reflect.Value{reflect.ValueOf(stringer)}
	returnTypes := "string"
	opts := CacheOptions{}

	key := r.keyForArgs(args, returnTypes, opts)

	assert.Equal(t, "stringerValue/string", key)
}

type testKeyable struct {
	Value string
}

func (t testKeyable) CacheKey() string {
	return t.Value
}

func Test_KeyForArgs_KeyableArgument(t *testing.T) {
	r := &RedisCache{}
	keyable := testKeyable{Value: "keyableValue"}
	args := []reflect.Value{reflect.ValueOf(keyable)}
	returnTypes := "string"
	opts := CacheOptions{}

	key := r.keyForArgs(args, returnTypes, opts)

	assert.Equal(t, "keyableValue/string", key)
}

func Test_KeyForArgs_StringArgument(t *testing.T) {
	r := &RedisCache{}
	args := []reflect.Value{reflect.ValueOf("stringValue")}
	returnTypes := "string"
	opts := CacheOptions{}

	key := r.keyForArgs(args, returnTypes, opts)

	assert.Equal(t, "stringValue/string", key)
}

func Test_KeyForArgs_InvalidArgumentType(t *testing.T) {
	r := &RedisCache{}
	args := []reflect.Value{reflect.ValueOf(123)}
	returnTypes := "string"
	opts := CacheOptions{}

	assert.Panics(t, func() {
		r.keyForArgs(args, returnTypes, opts)
	})
}

func Test_KeyForArgs_MultipleArguments(t *testing.T) {
	r := &RedisCache{}
	args := []reflect.Value{reflect.ValueOf("arg1"), reflect.ValueOf("arg2")}
	returnTypes := "string"
	opts := CacheOptions{}

	key := r.keyForArgs(args, returnTypes, opts)

	assert.Equal(t, "arg1:arg2/string", key)
}
