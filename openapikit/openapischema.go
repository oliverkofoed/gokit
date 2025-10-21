package openapikit

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	jsonschema "github.com/swaggest/jsonschema-go"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type OpenAPISchema struct {
	OpenAPI    string                            `json:"openapi"`
	Info       OpenAPIInfo                       `json:"info"`
	Paths      map[string]map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents                 `json:"components"`
	// Optional but recommended in OAS 3.1 when using JSON Schema 2020-12
	JsonSchemaDialect string `json:"jsonSchemaDialect,omitempty"`
}

type OpenAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type OpenAPIPath struct {
	OperationId string                     `json:"operationId,omitempty"`
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
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
	// Schema is a JSON Schema node (OAS 3.1 uses JSON Schema 2020-12)
	Schema any `json:"schema"`
}

type OpenAPIComponents struct {
	// Schemas holds JSON Schema nodes keyed by component name.
	Schemas map[string]any `json:"schemas"`
}

// ---------- Generator ----------

func (e *ApiMethods) AddSchemaRoute(site *web.Site, path string) {
	site.AddRoute(web.Route{
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
	return reflect.TypeOf(ApiError{})
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
	comps := OpenAPIComponents{Schemas: make(map[string]any)}
	seen := map[reflect.Type]string{} // type -> component name
	ref := jsonschema.Reflector{}

	s := OpenAPISchema{
		OpenAPI: "3.1.0",
		Info: OpenAPIInfo{
			Title:   "API",
			Version: "1.0.0",
		},
		Paths:             make(map[string]map[string]OpenAPIPath),
		Components:        comps,
		JsonSchemaDialect: "https://json-schema.org/draft/2020-12/schema",
	}

	// Pre-register the error schema if we have one
	var errRef any
	if et := e.ensureErrorType(); et != nil && et.Kind() == reflect.Struct {
		name := e.addComponentSchemaWithReflector(&ref, et, comps.Schemas, seen)
		errRef = map[string]any{"$ref": "#/components/schemas/" + name}
	}

	for _, ep := range e.endpoints {
		// Register args/result schemas
		argName := e.addComponentSchemaWithReflector(&ref, ep.Action.ArgsType, comps.Schemas, seen)
		resName := e.addComponentSchemaWithReflector(&ref, ep.Action.ResultType, comps.Schemas, seen)

		path := OpenAPIPath{
			OperationId: ep.Action.Name,
			Summary:     ep.Description,
			Description: ep.Description,
			RequestBody: OpenAPIRequestBody{
				Required: true,
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: map[string]any{"$ref": "#/components/schemas/" + argName}},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Success",
					Content: map[string]OpenAPIMediaType{
						"application/json": {Schema: map[string]any{"$ref": "#/components/schemas/" + resName}},
					},
				},
			},
		}

		// Add tags if service is specified
		if ep.Service != "" {
			path.Tags = []string{ep.Service}
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

// addComponentSchemaWithReflector registers (and returns) the component name for t using jsonschema-go.
func (e *ApiMethods) addComponentSchemaWithReflector(r *jsonschema.Reflector, t reflect.Type, registry map[string]any, seen map[reflect.Type]string) string {
	if t == nil {
		// Represent "any" as an unconstrained schema.
		return e.ensureNamedComponentAny(registry, "Any", map[string]any{})
	}

	ut := underlying(t)

	if name, ok := seen[ut]; ok {
		return name
	}

	name := e.typeName(ut)

	// Check for name collision with a different type
	for existingType, existingName := range seen {
		if existingName == name && existingType != ut {
			panic("OpenAPI type name collision: multiple types named '" + name + "' from different packages. " +
				"First: " + existingType.PkgPath() + "." + existingType.Name() + ", " +
				"Second: " + ut.PkgPath() + "." + ut.Name())
		}
	}

	seen[ut] = name // mark early

	// Reflect schema for the type using swaggest/jsonschema-go
	v := reflect.New(ut).Interface()
	// r.Reflect returns a schema struct; marshal and unmarshal it to generic map[string]any
	sch, _ := r.Reflect(v)
	// Convert to generic map for embedding
	var node map[string]any
	// Marshal regardless of pointer/value
	b, _ := json.Marshal(sch)
	_ = json.Unmarshal(b, &node)
	registry[name] = node
	return name
}

func (e *ApiMethods) typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct:
		if t.Name() != "" {
			return t.Name()
		}
		// anonymous struct: generate from full string
		return sanitizeName(t.String())
	default:
		if t.Name() != "" {
			return t.Name()
		}
		return sanitizeName(t.String())
	}
}

// ensureNamedComponentAny registers a literal under a fixed name if not present.
func (e *ApiMethods) ensureNamedComponentAny(registry map[string]any, name string, s map[string]any) string {
	if _, ok := registry[name]; !ok {
		registry[name] = s
	}
	return name
}

// (intentionally minimal) — prefer jsonschema tags directly on types for annotations.

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

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.TrimSpace(s)
	s = nonWord.ReplaceAllString(s, "_")
	return s
}
