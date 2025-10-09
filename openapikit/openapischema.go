package openapikit

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type OpenAPISchema struct {
	OpenAPI    string                            `json:"openapi"`
	Info       OpenAPIInfo                       `json:"info"`
	Paths      map[string]map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents                 `json:"components"`
}

type OpenAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type OpenAPIPath struct {
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	RequestBody OpenAPIRequestBody         `json:"requestBody"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
}

type OpenAPIRequestBody struct {
	Required bool                        `json:"required"`
	Content  map[string]OpenAPIMediaType `json:"content"`
}

type OpenAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
}

type OpenAPIMediaType struct {
	Schema *Schema `json:"schema"`
}

type OpenAPIComponents struct {
	Schemas map[string]*Schema `json:"schemas"`
}

// Minimal, expressive OpenAPI Schema node (can be a $ref or inline)
type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
}

// ---------- Generator ----------

func (e *ApiMethods) AddSchemaRoute(path string) {
	e.site.AddRoute(web.Route{
		Path: path,
		Action: func(c *web.Context) {
			schema := e.GenerateOpenAPISchema()
			c.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(c).Encode(schema)
		},
	})
}

// internal: store the chosen error type here
// (You can set it via SetErrorType; if nil, we try to use local Error{})
func (e *ApiMethods) ensureErrorType() reflect.Type {
	return reflect.TypeOf(Error{})
}

func (e *ApiMethods) GenerateOpenAPISchema() OpenAPISchema {
	// Return cached schema if available
	if e.schemaCache != nil {
		return *e.schemaCache
	}

	// Generate fresh schema and cache it
	schema := e.generateFreshSchema()
	e.schemaCache = &schema

	return schema
}

func (e *ApiMethods) generateFreshSchema() OpenAPISchema {
	comps := OpenAPIComponents{Schemas: make(map[string]*Schema)}
	seen := map[reflect.Type]string{} // type -> component name

	s := OpenAPISchema{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:   "API",
			Version: "1.0.0",
		},
		Paths:      make(map[string]map[string]OpenAPIPath),
		Components: comps,
	}

	// Pre-register the error schema if we have one
	var errRef *Schema
	if et := e.ensureErrorType(); et != nil && et.Kind() == reflect.Struct {
		name := e.addComponentSchema(et, comps.Schemas, seen)
		errRef = &Schema{Ref: "#/components/schemas/" + name}
	}

	for _, ep := range e.endpoints {
		// Register args/result schemas
		argName := e.addComponentSchema(ep.Action.ArgsType, comps.Schemas, seen)
		resName := e.addComponentSchema(ep.Action.ResultType, comps.Schemas, seen)

		path := OpenAPIPath{
			Summary:     ep.Description,
			Description: ep.Description,
			RequestBody: OpenAPIRequestBody{
				Required: true,
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &Schema{Ref: "#/components/schemas/" + argName}},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Success",
					Content: map[string]OpenAPIMediaType{
						"application/json": {Schema: &Schema{Ref: "#/components/schemas/" + resName}},
					},
				},
			},
		}

		// Add standard error responses if we know the error envelope
		if errRef != nil {
			path.Responses["400"] = OpenAPIResponse{
				Description: "Bad Request",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: errRef},
				},
			}
			path.Responses["500"] = OpenAPIResponse{
				Description: "Internal Server Error",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: errRef},
				},
			}
		}

		if s.Paths[ep.Path] == nil {
			s.Paths[ep.Path] = make(map[string]OpenAPIPath)
		}
		s.Paths[ep.Path]["post"] = path
	}

	return s
}

// addComponentSchema registers (and returns) the component name for t.
func (e *ApiMethods) addComponentSchema(t reflect.Type, registry map[string]*Schema, seen map[reflect.Type]string) string {
	if t == nil {
		// represent "any" as object
		return e.ensureNamedComponent(t, registry, seen, "Any", &Schema{Type: "object"})
	}

	// Unwrap aliases like type MyInt int
	ut := underlying(t)

	// If we already created a component for this type, reuse it.
	if name, ok := seen[ut]; ok {
		return name
	}

	// Decide a stable component name
	name := e.typeName(ut)
	seen[ut] = name // mark early to break recursive cycles

	// Build schema
	schema := e.schemaFor(ut, registry, seen)
	registry[name] = schema
	return name
}

func (e *ApiMethods) schemaFor(t reflect.Type, registry map[string]*Schema, seen map[reflect.Type]string) *Schema {
	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return &Schema{Type: "integer", Format: "int32"}
	case reflect.Int64:
		return &Schema{Type: "integer", Format: "int64"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return &Schema{Type: "integer", Format: "int32"}
	case reflect.Uint64:
		return &Schema{Type: "integer", Format: "int64"}
	case reflect.Float32:
		return &Schema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &Schema{Type: "number", Format: "double"}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Slice, reflect.Array:
		return &Schema{
			Type:  "array",
			Items: e.schemaNode(t.Elem(), registry, seen),
		}
	case reflect.Map:
		// object with additionalProperties of the value type
		return &Schema{
			Type:                 "object",
			AdditionalProperties: e.schemaNode(t.Elem(), registry, seen),
		}
	case reflect.Ptr:
		// pointers become nullable; schema is the element
		elem := e.schemaNode(t.Elem(), registry, seen)
		cp := *elem
		cp.Nullable = true
		return &cp
	case reflect.Struct:
		// well-known: time.Time
		if t.PkgPath() == "time" && t.Name() == "Time" {
			return &Schema{Type: "string", Format: "date-time"}
		}
		// Create an object and walk fields
		props := map[string]*Schema{}
		required := make([]string, 0)

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			// Skip explicitly ignored
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}

			name, omitEmpty := parseJSONName(tag, f.Name)
			fs := e.schemaNode(f.Type, registry, seen)

			// copy to avoid mutating shared node
			cp := *fs

			// descriptions & enums from tags
			if desc := f.Tag.Get("desc"); desc != "" {
				cp.Description = desc
			} else if desc := f.Tag.Get("description"); desc != "" {
				cp.Description = desc
			}
			if enumTag := f.Tag.Get("enum"); enumTag != "" {
				cp.Enum = splitEnum(enumTag)
			}

			// required if not omitempty and not a pointer
			if !omitEmpty && f.Type.Kind() != reflect.Ptr {
				required = append(required, name)
			}

			props[name] = &cp
		}

		return &Schema{
			Type:       "object",
			Properties: props,
			Required:   required,
		}
	default:
		// Fallback: treat as string
		return &Schema{Type: "string"}
	}
}

// schemaNode returns either an inline node or a $ref for named structs.
func (e *ApiMethods) schemaNode(t reflect.Type, registry map[string]*Schema, seen map[reflect.Type]string) *Schema {
	ut := underlying(t)

	// For named structs (and their pointers), use component refs for reuse & recursion safety
	if ut.Kind() == reflect.Struct && ut.Name() != "" && !(ut.PkgPath() == "time" && ut.Name() == "Time") {
		name := e.addComponentSchema(ut, registry, seen)
		return &Schema{Ref: "#/components/schemas/" + name}
	}
	// Inline otherwise (primitives, slices, maps, anonymous structs)
	return e.schemaFor(ut, registry, seen)
}

func (e *ApiMethods) typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct:
		if t.Name() != "" {
			return sanitizeName(t.PkgPath() + "." + t.Name())
		}
		// anonymous struct: generate from full string
		return sanitizeName(t.String())
	default:
		if t.Name() != "" {
			return sanitizeName(t.PkgPath() + "." + t.Name())
		}
		return sanitizeName(t.String())
	}
}

// ensureNamedComponent registers a literal under a fixed name if not present.
func (e *ApiMethods) ensureNamedComponent(_ reflect.Type, registry map[string]*Schema, _ map[reflect.Type]string, name string, s *Schema) string {
	if _, ok := registry[name]; !ok {
		registry[name] = s
	}
	return name
}

func underlying(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// Resolve defined types (aliases) down to their underlying kind for schema decisions,
	// but KEEP struct names (handled by schemaNode with components).
	if t.Kind() != reflect.Struct && t.Name() != "" {
		// Non-struct named types behave like their underlying primitives in shape.
	}
	return t
}

// ---------- helpers ----------

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.TrimSpace(s)
	s = nonWord.ReplaceAllString(s, "_")
	return s
}

func parseJSONName(tag, fallback string) (name string, omitEmpty bool) {
	name = fallback
	if tag == "" {
		return name, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] != "" {
		name = parts[0]
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			return name, true
		}
	}
	return name, false
}

func splitEnum(v string) []string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "|") {
		return splitAndTrim(v, "|")
	}
	if strings.Contains(v, ",") {
		return splitAndTrim(v, ",")
	}
	return []string{v}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
