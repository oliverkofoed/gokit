package openapikit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
