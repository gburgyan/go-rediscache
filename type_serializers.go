package rediscache

import (
	"encoding/json"
	"reflect"
)

func JsonSerializer(v any) ([]byte, error) {
	return json.Marshal(v)
}

func JsonDeserializer(typ reflect.Type, data []byte) (any, error) {
	ptr := reflect.New(typ)
	err := json.Unmarshal(data, ptr.Interface())
	return ptr.Elem().Interface(), err
}
