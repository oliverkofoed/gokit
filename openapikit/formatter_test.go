package openapikit

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	jsoniter "github.com/json-iterator/go"
	"github.com/oliverkofoed/gokit/sitekit/web"
)

type FormatterArgs struct {
	ID    int64  `json:"id"`
	PtrID *int64 `json:"ptr_id"`
	Value string `json:"value"`
}

type FormatterResult struct {
	ID      int64  `json:"id"`
	PtrID   *int64 `json:"ptr_id"`
	Success bool   `json:"success"`
}

func FormatterHandler(c *web.Context, args FormatterArgs) (*FormatterResult, error) {
	res := &FormatterResult{
		ID:      args.ID + 1,
		Success: true,
	}
	if args.PtrID != nil {
		v := *args.PtrID + 1
		res.PtrID = &v
	}
	return res, nil
}

func TestInt64HexFormatter(t *testing.T) {
	apis := New()
	apis.RegisterJsonFormatter(Int64HexFormatter{})

	apis.Add(Method{
		Path:   "/test/formatter",
		Action: Action(FormatterHandler),
	})

	// 1. Check Schema
	schema := apis.GenerateOpenAPISchema()

	// b, _ := json.MarshalIndent(schema, "", "  ")
	// t.Logf("Generated Schema: %s", string(b))

	// Check if FormatterArgs.ID is a hex string in the schema
	argsSchema, ok := schema.Components.Schemas["FormatterArgs"].(map[string]any)
	if !ok {
		t.Fatal("FormatterArgs schema not found")
	}
	properties := argsSchema["properties"].(map[string]any)

	idProp := properties["id"].(map[string]any)
	if idProp["type"] != "string" || idProp["format"] != "hex" {
		t.Errorf("Expected id property to be string/hex, got %v/%v", idProp["type"], idProp["format"])
	}

	ptrIdProp := properties["ptr_id"].(map[string]any)
	ptrIdType := ptrIdProp["type"]
	isStringHex := false
	if ptrIdType == "string" {
		isStringHex = true
	} else if types, ok := ptrIdType.([]any); ok {
		for _, ty := range types {
			if ty == "string" {
				isStringHex = true
				break
			}
		}
	}

	if !isStringHex || ptrIdProp["format"] != "hex" {
		t.Errorf("Expected ptr_id property to be string/hex, got %v/%v", ptrIdType, ptrIdProp["format"])
	}

	// 2. Check Encoding/Decoding
	site := web.NewSite(true, "/assets/")
	apis.InstallInto(site, true)

	// Test request with hex ID
	reqBody := `{"id": "a", "ptr_id": "ff", "value": "test"}` // 10 and 255
	req := httptest.NewRequest(http.MethodPost, "/test/formatter", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	site.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	// Result ID should be 11 (b in hex)
	if res["id"] != "b" {
		t.Errorf("Expected resulting id to be 'b', got %v", res["id"])
	}

	// PtrID should be 256 (100 in hex)
	if res["ptr_id"] != "100" {
		t.Errorf("Expected resulting ptr_id to be '100', got %v", res["ptr_id"])
	}
}

type FormatterUploadArgs struct {
	ID     int64     `json:"id"`
	Name   string    `json:"name"`
	Avatar io.Reader `json:"avatar"`
}

type FormatterUploadResult struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// A formatted field has to survive multipart, not only json.
//
// An io.Reader in the args makes the endpoint multipart, and a form field is a
// bare string: `1f4`, never `"1f4"`. The hex codec reads a json string, so
// without the quoted retry in the decoder the id silently stays 0 — and an id of
// 0 reads downstream as "no such row" rather than as "never parsed", which is a
// permission error on a resource that exists.
func TestInt64HexFormatterMultipart(t *testing.T) {
	apis := New()
	apis.RegisterJsonFormatter(Int64HexFormatter{})
	apis.Add(Method{
		Path: "/test/upload",
		Action: Action(func(c *web.Context, args FormatterUploadArgs) (*FormatterUploadResult, error) {
			return &FormatterUploadResult{ID: args.ID, Name: args.Name}, nil
		}),
	})

	site := web.NewSite(true, "/assets/")
	apis.InstallInto(site, true)

	post := func(t *testing.T, id string) map[string]any {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("id", id); err != nil {
			t.Fatal(err)
		}
		if err := form.WriteField("name", "renamed"); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/test/upload", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		site.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		var res map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	// The shape the generated clients send: hex, unquoted, and not valid json.
	res := post(t, "10a80f0d81cf8001")
	if res["id"] != "10a80f0d81cf8001" {
		t.Errorf("Expected id to survive the round trip, got %v", res["id"])
	}
	if res["name"] != "renamed" {
		t.Errorf("Expected name 'renamed', got %v", res["name"])
	}

	// A value that is also valid json on its own is still hex, because that is
	// what the registered codec says the field is. `10` is 16, not 10.
	if res := post(t, "10"); res["id"] != "10" {
		t.Errorf("Expected '10' to be read as hex and echoed as '10', got %v", res["id"])
	}
}

// A string-kinded named type with its own codec, to prove the multipart path
// asks the codec rather than assigning the raw text past it. `slug:` is the
// marker: it can only appear if the codec ran.
type Slug string

type SlugFormatter struct{}

func (f SlugFormatter) Type() reflect.Type               { return reflect.TypeOf(Slug("")) }
func (f SlugFormatter) JsonEncoder() jsoniter.ValEncoder { return &slugCodec{} }
func (f SlugFormatter) JsonDecoder() jsoniter.ValDecoder { return &slugCodec{} }
func (f SlugFormatter) UpdateSchema(schema map[string]any) {
	schema["type"] = "string"
	schema["format"] = "slug"
}

type slugCodec struct{}

func (c *slugCodec) IsEmpty(ptr unsafe.Pointer) bool { return *((*Slug)(ptr)) == "" }

func (c *slugCodec) Encode(ptr unsafe.Pointer, stream *jsoniter.Stream) {
	stream.WriteString(string(*((*Slug)(ptr))))
}

func (c *slugCodec) Decode(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
	*((*Slug)(ptr)) = Slug("slug:" + iter.ReadString())
}

type StringFormArgs struct {
	Name   string    `json:"name"`
	Slug   Slug      `json:"slug"`
	Avatar io.Reader `json:"avatar"`
}

type StringFormResult struct {
	Name string `json:"name"`
	Slug Slug   `json:"slug"`
}

// Strings in multipart: decoded by their codec, and never re-read as json.
//
// The second half is the trap the first half could have walked into. A form
// value is text, so `123`, `null` and `"quoted"` are three names — not a
// number, an absence and a string with its quotes stripped. Decoding the raw
// value first would have silently rewritten all three.
func TestStringFieldsFromMultipart(t *testing.T) {
	apis := New()
	apis.RegisterJsonFormatter(SlugFormatter{})
	apis.Add(Method{
		Path: "/test/strings",
		Action: Action(func(c *web.Context, args StringFormArgs) (*StringFormResult, error) {
			return &StringFormResult{Name: args.Name, Slug: args.Slug}, nil
		}),
	})

	site := web.NewSite(true, "/assets/")
	apis.InstallInto(site, true)

	post := func(t *testing.T, fields map[string]string) map[string]any {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for k, v := range fields {
			if err := form.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/test/strings", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		site.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		var res map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	if res := post(t, map[string]string{"slug": "my-project"}); res["slug"] != "slug:my-project" {
		t.Errorf("Expected the registered codec to decode the slug, got %v", res["slug"])
	}

	// Text that happens to look like json is still text.
	for _, name := range []string{"123", "null", "true", `"quoted"`, `{"a":1}`, "renamed"} {
		if res := post(t, map[string]string{"name": name}); res["name"] != name {
			t.Errorf("Expected name %q to arrive verbatim, got %v", name, res["name"])
		}
	}
}
