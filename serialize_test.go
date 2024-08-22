package rediscache

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_Serialize_ValidInput_ReturnsSerializedData(t *testing.T) {
	input := [][]byte{
		[]byte("hello"),
		[]byte("world"),
	}
	expected := []byte{
		5, 0, 0, 0, 'h', 'e', 'l', 'l', 'o',
		5, 0, 0, 0, 'w', 'o', 'r', 'l', 'd',
	}

	result, err := combineBytes(input)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_Serialize_EmptyInput_ReturnsEmptyData(t *testing.T) {
	input := [][]byte{}
	expected := []byte{}

	result, err := combineBytes(input)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_Deserialize_ValidData_ReturnsDeserializedSlices(t *testing.T) {
	data := []byte{
		5, 0, 0, 0, 'h', 'e', 'l', 'l', 'o',
		5, 0, 0, 0, 'w', 'o', 'r', 'l', 'd',
	}
	expected := [][]byte{
		[]byte("hello"),
		[]byte("world"),
	}

	result, err := splitBytes(data)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_Deserialize_EmptyData_ReturnsEmptySlices(t *testing.T) {
	data := []byte{}
	expected := [][]byte{}

	result, err := splitBytes(data)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_Deserialize_InvalidData_ReturnsError(t *testing.T) {
	data := []byte{1, 2, 3, 4}

	_, err := splitBytes(data)
	assert.Error(t, err)
}
