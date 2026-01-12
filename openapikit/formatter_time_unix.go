package openapikit

import (
	"reflect"
	"time"
	"unsafe"

	jsoniter "github.com/json-iterator/go"
)

type TimeUnixFormatter struct{}

func (f TimeUnixFormatter) Type() reflect.Type {
	return reflect.TypeOf(time.Time{})
}

func (f TimeUnixFormatter) JsonEncoder() jsoniter.ValEncoder {
	return &timeUnixCodec{}
}

func (f TimeUnixFormatter) JsonDecoder() jsoniter.ValDecoder {
	return &timeUnixCodec{}
}

func (f TimeUnixFormatter) UpdateSchema(schema map[string]any) {
	schema["type"] = "integer"
	schema["format"] = "int64"
	schema["description"] = "Unix timestamp in seconds"
}

type timeUnixCodec struct{}

func (c *timeUnixCodec) IsEmpty(ptr unsafe.Pointer) bool {
	return (*((*time.Time)(ptr))).IsZero()
}

func (c *timeUnixCodec) Encode(ptr unsafe.Pointer, stream *jsoniter.Stream) {
	val := *((*time.Time)(ptr))
	if val.IsZero() {
		stream.WriteInt64(0)
		return
	}
	stream.WriteInt64(val.Unix())
}

func (c *timeUnixCodec) Decode(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
	val := iter.ReadInt64()
	if val == 0 {
		*((*time.Time)(ptr)) = time.Time{}
		return
	}
	*((*time.Time)(ptr)) = time.Unix(val, 0).UTC()
}
