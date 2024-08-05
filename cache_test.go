package rediscache

import (
	"context"
	"github.com/stretchr/testify/assert"
	"reflect"
	"testing"
)

func typesForArgs(args []reflect.Value) []reflect.Type {
	types := make([]reflect.Type, len(args))
	for i, arg := range args {
		types[i] = arg.Type()
	}
	return types
}

func Test_KeyForArgs_ContextFirstArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf("arg1")}
	returnTypes := "string"
	opts := CacheOptions{}

	inputHandlers := r.validateInputParams(typesForArgs(args))
	key := r.keyForArgs(inputHandlers, args, returnTypes, opts)

	assert.Equal(t, "arg1/string", key)
}

type paramStringer struct {
	Value string
}

func (p paramStringer) String() string {
	return p.Value
}

func Test_KeyForArgs_StringerArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	stringer := paramStringer{Value: "stringerValue"}
	args := []reflect.Value{reflect.ValueOf(stringer)}
	returnTypes := "string"
	opts := CacheOptions{}

	inputHandlers := r.validateInputParams(typesForArgs(args))
	key := r.keyForArgs(inputHandlers, args, returnTypes, opts)

	assert.Equal(t, "stringerValue/string", key)
}

type testKeyable struct {
	Value string
}

func (t testKeyable) CacheKey() string {
	return t.Value
}

func Test_KeyForArgs_KeyableArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	keyable := testKeyable{Value: "keyableValue"}
	args := []reflect.Value{reflect.ValueOf(keyable)}
	returnTypes := "string"
	opts := CacheOptions{}

	inputHandlers := r.validateInputParams(typesForArgs(args))
	key := r.keyForArgs(inputHandlers, args, returnTypes, opts)

	assert.Equal(t, "keyableValue/string", key)
}

func Test_KeyForArgs_StringArgument(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf("stringValue")}
	returnTypes := "string"
	opts := CacheOptions{}

	inputHandlers := r.validateInputParams(typesForArgs(args))
	key := r.keyForArgs(inputHandlers, args, returnTypes, opts)

	assert.Equal(t, "stringValue/string", key)
}

func Test_KeyForArgs_InvalidArgumentType(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf(123)}
	returnTypes := "string"
	opts := CacheOptions{}

	assert.Panics(t, func() {
		inputHandlers := r.validateInputParams(typesForArgs(args))
		_ = r.keyForArgs(inputHandlers, args, returnTypes, opts)
	})
}

func Test_KeyForArgs_MultipleArguments(t *testing.T) {
	r := &RedisCache{typeHandlers: map[reflect.Type]outputValueHandler{}}
	args := []reflect.Value{reflect.ValueOf("arg1"), reflect.ValueOf("arg2")}
	returnTypes := "string"
	opts := CacheOptions{}

	inputHandlers := r.validateInputParams(typesForArgs(args))
	key := r.keyForArgs(inputHandlers, args, returnTypes, opts)

	assert.Equal(t, "arg1:arg2/string", key)
}
