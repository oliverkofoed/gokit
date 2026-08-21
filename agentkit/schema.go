package agentkit

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// schemaNode is the internal representation of the JSON Schema subset the
// tool layer generates (R28) and validates against (R29).
type schemaNode struct {
	Type        string                 `json:"type,omitempty"`
	Description string                 `json:"description,omitempty"`
	Enum        []string               `json:"enum,omitempty"`
	Default     any                    `json:"default,omitempty"`
	Minimum     *float64               `json:"minimum,omitempty"`
	Maximum     *float64               `json:"maximum,omitempty"`
	Properties  map[string]*schemaNode `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Items       *schemaNode            `json:"items,omitempty"`
	// AdditionalProperties is false for structs, a *schemaNode for maps,
	// nil (omitted) otherwise.
	AdditionalProperties any `json:"additionalProperties,omitempty"`
}

const maxSchemaDepth = 16

// buildSchema derives a schema from a struct type by reflection (R28). It
// panics on unsupported kinds and malformed tags — tool definition bugs are
// programmer errors caught at construction.
func buildSchema(t reflect.Type) *schemaNode {
	node, err := schemaForType(t, 0)
	if err != nil {
		panic(fmt.Sprintf("agentkit: schema for %s: %v", t, err))
	}
	if node.Type != "object" {
		panic(fmt.Sprintf("agentkit: tool Args must be a struct, got %s", t))
	}
	return node
}

func schemaForType(t reflect.Type, depth int) (*schemaNode, error) {
	if depth > maxSchemaDepth {
		return nil, fmt.Errorf("recursion deeper than %d levels", maxSchemaDepth)
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaForType(t.Elem(), depth+1)
	case reflect.String:
		return &schemaNode{Type: "string"}, nil
	case reflect.Bool:
		return &schemaNode{Type: "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &schemaNode{Type: "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return &schemaNode{Type: "number"}, nil
	case reflect.Slice, reflect.Array:
		items, err := schemaForType(t.Elem(), depth+1)
		if err != nil {
			return nil, err
		}
		return &schemaNode{Type: "array", Items: items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map keys must be strings, got %s", t.Key())
		}
		values, err := schemaForType(t.Elem(), depth+1)
		if err != nil {
			return nil, err
		}
		return &schemaNode{Type: "object", AdditionalProperties: values}, nil
	case reflect.Struct:
		return schemaForStruct(t, depth)
	default:
		return nil, fmt.Errorf("unsupported kind %s", t.Kind())
	}
}

func schemaForStruct(t reflect.Type, depth int) (*schemaNode, error) {
	node := &schemaNode{
		Type:                 "object",
		Properties:           map[string]*schemaNode{},
		AdditionalProperties: false,
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, omitempty := jsonFieldName(f)
		if name == "-" {
			continue
		}
		child, err := schemaForType(f.Type, depth+1)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		if tag, ok := f.Tag.Lookup("jsonschema"); ok {
			if err := applyDirectives(child, tag); err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name, err)
			}
		}
		node.Properties[name] = child
		// Required unless the field is a pointer or has ,omitempty (R28).
		if f.Type.Kind() != reflect.Pointer && !omitempty {
			node.Required = append(node.Required, name)
		}
	}
	return node, nil
}

func jsonFieldName(f reflect.StructField) (name string, omitempty bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

// applyDirectives parses the jsonschema tag: comma-separated k=v pairs. A
// segment without '=' is appended to the previous pair's value with ", " —
// so descriptions may contain commas as long as no segment contains '='
// (R28).
func applyDirectives(node *schemaNode, tag string) error {
	segments := strings.Split(tag, ",")
	type pair struct{ key, value string }
	var pairs []pair
	for _, seg := range segments {
		key, value, found := strings.Cut(seg, "=")
		if !found {
			if len(pairs) == 0 {
				return fmt.Errorf("jsonschema tag segment %q has no directive", seg)
			}
			pairs[len(pairs)-1].value += ", " + strings.TrimLeft(seg, " ")
			continue
		}
		pairs = append(pairs, pair{key: strings.TrimSpace(key), value: value})
	}
	for _, p := range pairs {
		switch p.key {
		case "description":
			node.Description = p.value
		case "enum":
			if node.Type != "string" {
				return fmt.Errorf("enum= is only supported on string fields")
			}
			node.Enum = strings.Split(p.value, "|")
		case "default":
			node.Default = parseDefault(node.Type, p.value)
		case "minimum":
			v, err := strconv.ParseFloat(p.value, 64)
			if err != nil {
				return fmt.Errorf("minimum=%q: %v", p.value, err)
			}
			node.Minimum = &v
		case "maximum":
			v, err := strconv.ParseFloat(p.value, 64)
			if err != nil {
				return fmt.Errorf("maximum=%q: %v", p.value, err)
			}
			node.Maximum = &v
		default:
			return fmt.Errorf("unknown jsonschema directive %q", p.key)
		}
	}
	return nil
}

func parseDefault(schemaType, raw string) any {
	switch schemaType {
	case "integer":
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v
		}
	case "number":
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	case "boolean":
		if v, err := strconv.ParseBool(raw); err == nil {
			return v
		}
	}
	return raw
}

// validateSchema checks a decoded JSON value against a node (R29), returning
// one message per failing path, e.g. "args.path: expected string, got number".
func validateSchema(node *schemaNode, value any, path string) []string {
	switch node.Type {
	case "object":
		m, ok := value.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: expected object, got %s", path, jsonTypeName(value))}
		}
		var errs []string
		for _, req := range node.Required {
			if _, present := m[req]; !present {
				errs = append(errs, fmt.Sprintf("%s.%s: required property missing", path, req))
			}
		}
		valueSchema, isMap := node.AdditionalProperties.(*schemaNode)
		for key, v := range m {
			if child, ok := node.Properties[key]; ok {
				errs = append(errs, validateSchema(child, v, path+"."+key)...)
				continue
			}
			if isMap {
				errs = append(errs, validateSchema(valueSchema, v, path+"."+key)...)
				continue
			}
			if add, ok := node.AdditionalProperties.(bool); ok && !add {
				errs = append(errs, fmt.Sprintf("%s.%s: unknown property", path, key))
			}
		}
		return errs

	case "string":
		s, ok := value.(string)
		if !ok {
			return []string{fmt.Sprintf("%s: expected string, got %s", path, jsonTypeName(value))}
		}
		if len(node.Enum) > 0 {
			for _, e := range node.Enum {
				if s == e {
					return nil
				}
			}
			return []string{fmt.Sprintf("%s: %q is not one of [%s]", path, s, strings.Join(node.Enum, ", "))}
		}
		return nil

	case "integer":
		f, ok := value.(float64)
		if !ok {
			return []string{fmt.Sprintf("%s: expected integer, got %s", path, jsonTypeName(value))}
		}
		if f != math.Trunc(f) {
			return []string{fmt.Sprintf("%s: expected integer, got non-integral number %v", path, f)}
		}
		return validateRange(node, f, path)

	case "number":
		f, ok := value.(float64)
		if !ok {
			return []string{fmt.Sprintf("%s: expected number, got %s", path, jsonTypeName(value))}
		}
		return validateRange(node, f, path)

	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s: expected boolean, got %s", path, jsonTypeName(value))}
		}
		return nil

	case "array":
		arr, ok := value.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: expected array, got %s", path, jsonTypeName(value))}
		}
		var errs []string
		if node.Items != nil {
			for i, v := range arr {
				errs = append(errs, validateSchema(node.Items, v, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
		return errs
	}
	return nil
}

func validateRange(node *schemaNode, f float64, path string) []string {
	var errs []string
	if node.Minimum != nil && f < *node.Minimum {
		errs = append(errs, fmt.Sprintf("%s: %v is below minimum %v", path, f, *node.Minimum))
	}
	if node.Maximum != nil && f > *node.Maximum {
		errs = append(errs, fmt.Sprintf("%s: %v is above maximum %v", path, f, *node.Maximum))
	}
	return errs
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// mustMarshalSchema renders a node as JSON Schema bytes.
func mustMarshalSchema(node *schemaNode) json.RawMessage {
	b, err := json.Marshal(node)
	if err != nil {
		panic(fmt.Sprintf("agentkit: marshal schema: %v", err))
	}
	return b
}
