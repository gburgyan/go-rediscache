package rediscache

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"time"
)

func combineBytes(input [][]byte) ([]byte, error) {
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

func splitBytes(data []byte) ([][]byte, error) {
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

func serializeResultsToCache(opts CacheOptions, results []reflect.Value, handlers []valueHandler) ([]byte, error) {
	parts := make([][]byte, len(handlers)+1)

	for i := 0; i < len(handlers); i++ {
		if handlers[i].serializer != nil {
			serialized, err := handlers[i].serializer(results[i].Interface())
			if err != nil {
				return nil, err
			}
			parts[i] = serialized
			continue
		}
		return nil, errors.New("invalid return type " + handlers[i].typ.String())
	}

	now := opts.now()
	marshalBinary, err := now.MarshalBinary()
	if err != nil {
		return nil, err
	}
	parts[len(handlers)] = marshalBinary

	plaintext, err := combineBytes(parts)
	if err != nil {
		return nil, err
	}
	return handleEncryption(opts, plaintext)
}

func deserializeCacheToResults(ctx context.Context, opts CacheOptions, value []byte, out []valueHandler) ([]reflect.Value, time.Time, error) {
	handleDecryption(ctx, opts, value)

	parts, err := splitBytes(value)
	if err != nil {
		return nil, time.Time{}, err
	}

	if len(parts) != len(out)+1 {
		return nil, time.Time{}, errors.New("invalid number of parts")
	}
	results := make([]reflect.Value, len(out))

	for i := 0; i < len(out); i++ {
		desVal, err := out[i].deserializer(out[i].typ, parts[i])
		if err != nil {
			return nil, time.Time{}, err
		}
		if reflect.TypeOf(desVal) == valueType {
			results[i] = desVal.(reflect.Value)
		} else {
			results[i] = reflect.ValueOf(desVal)
		}
	}

	var saveTime time.Time
	if err := saveTime.UnmarshalBinary(parts[len(parts)-1]); err != nil {
		return nil, time.Time{}, err
	}

	return results, saveTime, nil
}
