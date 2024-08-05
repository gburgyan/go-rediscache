package rediscache

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
)

func serialize(input [][]byte) ([]byte, error) {
	var buf bytes.Buffer

	for _, slice := range input {
		// Write the length of the slice
		length := int32(len(slice))
		if err := binary.Write(&buf, binary.LittleEndian, length); err != nil {
			return nil, err
		}
		// Write the slice data
		if _, err := buf.Write(slice); err != nil {
			return nil, err
		}
	}

	if buf.Len() == 0 {
		return []byte{}, nil
	}

	return buf.Bytes(), nil
}

func deserialize(data []byte) ([][]byte, error) {
	buf := bytes.NewReader(data)
	result := [][]byte{}

	for {
		var length int32
		// Read the length of the next slice
		if err := binary.Read(buf, binary.LittleEndian, &length); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}

		// Read the slice data
		slice := make([]byte, length)
		if length > 0 {
			if _, err := buf.Read(slice); err != nil {
				return nil, err
			}
		}
		result = append(result, slice)
	}

	return result, nil
}

func (r *RedisCache) serializeResultsToCache(results []reflect.Value, handlers []outputValueHandler, out []reflect.Type) ([]byte, error) {
	parts := make([][]byte, len(handlers))

	for i := 0; i < len(handlers); i++ {
		if handlers[i].serializer != nil {
			serialized, err := handlers[i].serializer(results[i].Interface())
			if err != nil {
				return nil, err
			}
			parts[i] = serialized
			continue
		}
		return nil, errors.New("invalid return type " + out[i].String())
	}
	return serialize(parts)
}

func (r *RedisCache) deserializeCacheToResults(value []byte, out []outputValueHandler) ([]reflect.Value, error) {
	parts, err := deserialize(value)
	if err != nil {
		return nil, err
	}
	if len(parts) != len(out) {
		return nil, errors.New("invalid number of parts")
	}
	results := make([]reflect.Value, len(parts))
	for i := 0; i < len(parts); i++ {
		desVal, err := out[i].deserializer(parts[i])
		if err != nil {
			return nil, err
		}
		if reflect.TypeOf(desVal) == valueType {
			results[i] = desVal.(reflect.Value)
		} else {
			results[i] = reflect.ValueOf(desVal)
		}
	}
	return results, nil
}
