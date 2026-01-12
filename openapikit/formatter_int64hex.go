package openapikit

import (
	"fmt"
	"reflect"
	"unsafe"

	jsoniter "github.com/json-iterator/go"
)

type Int64HexFormatter struct{}

func (f Int64HexFormatter) Type() reflect.Type {
	return reflect.TypeOf(int64(0))
}

func (f Int64HexFormatter) JsonEncoder() jsoniter.ValEncoder {
	return &int64HexCodec{}
}

func (f Int64HexFormatter) JsonDecoder() jsoniter.ValDecoder {
	return &int64HexCodec{}
}

func (f Int64HexFormatter) UpdateSchema(schema map[string]any) {
	schema["type"] = "string"
	schema["format"] = "hex"
}

type int64HexCodec struct{}

func (c *int64HexCodec) IsEmpty(ptr unsafe.Pointer) bool {
	return *((*int64)(ptr)) == 0
}

func (c *int64HexCodec) Encode(ptr unsafe.Pointer, stream *jsoniter.Stream) {
	val := *((*int64)(ptr))
	stream.WriteString(fmt.Sprintf("%x", val))
}

func (c *int64HexCodec) Decode(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
	s := iter.ReadString()
	if s == "" {
		*((*int64)(ptr)) = 0
		return
	}
	var val int64
	_, err := fmt.Sscanf(s, "%x", &val)
	if err != nil {
		iter.ReportError("int64HexCodec.Decode", err.Error())
		return
	}
	*((*int64)(ptr)) = val
}
