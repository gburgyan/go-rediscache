package go_rediscache

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
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
