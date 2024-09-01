package rediscache

/*
import (
	"errors"
	"google.golang.org/protobuf/proto"
	"reflect"
)

// GRPCSerializer serializes a protobuf message to a byte slice.
func GRPCSerializer(data any) ([]byte, error) {
	msg, ok := data.(proto.Message)
	if !ok {
		return nil, errors.New("data does not implement proto.Message")
	}
	return proto.Marshal(msg)
}

// GRPCDeserializer deserializes a byte slice to a protobuf message.
func GRPCDeserializer(typ reflect.Type, data []byte) (any, error) {
	msg, ok := reflect.New(typ.Elem()).Interface().(proto.Message)
	if !ok {
		return nil, errors.New("type does not implement proto.Message")
	}
	err := proto.Unmarshal(data, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}
*/
