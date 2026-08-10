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

func (e *ApiMethods) fixSchema(schema any, registry map[string]any) {
	if m, ok := schema.(map[string]any); ok {
		// Fix $ref
		if ref, ok := m["$ref"].(string); ok {
			if strings.HasPrefix(ref, "#/definitions/") {
				m["$ref"] = strings.Replace(ref, "#/definitions/", "#/components/schemas/", 1)
			}

			// Check if this is a reference to IoReader
			if strings.HasSuffix(ref, "/IoReader") {
				delete(m, "$ref")
				m["type"] = "string"
				m["format"] = "binary"
				return
			}
		}

		// Check for definitions (swaggest/jsonschema-go uses this for nested types)
		if defs, ok := m["definitions"].(map[string]any); ok {
			for k, v := range defs {
				if k == "IoReader" {
					continue
				}
				// Move to global registry if not already there
				if _, exists := registry[k]; !exists {
					registry[k] = v
					// Recursively fix the moved schema
					e.fixSchema(v, registry)
				}
			}
			delete(m, "definitions")
		}

		// Recurse
		for _, v := range m {
			e.fixSchema(v, registry)
		}
	} else if s, ok := schema.([]any); ok {
		for _, v := range s {
			e.fixSchema(v, registry)
		}
	}
}

// buildOperationIdCollisionMap scans all endpoints and returns a set of base operationIds that have collisions
func (e *ApiMethods) buildOperationIdCollisionMap() map[string]bool {
	// Map from base operationId -> set of unique services with that operationId
	nameToServices := make(map[string]map[string]bool)

	for _, ep := range e.endpoints {
		// Determine base operationId using the same logic as generateFreshSchema
		baseOpId := ep.Name
		if baseOpId == "" && !strings.HasPrefix(ep.Action.Name, "func") {
			baseOpId = ep.Action.Name
		}
		if baseOpId == "" {
			if idx := strings.LastIndex(ep.Path, "/"); idx >= 0 {
				baseOpId = ep.Path[idx+1:]
			} else {
				baseOpId = ep.Path
			}
		}

		if nameToServices[baseOpId] == nil {
			nameToServices[baseOpId] = make(map[string]bool)
		}
		nameToServices[baseOpId][ep.Service] = true
	}

	// Build collision set - operationIds that appear in more than one service
	collisions := make(map[string]bool)
	for name, services := range nameToServices {
		if len(services) > 1 {
			collisions[name] = true
		}
	}

	return collisions
}

// buildCollisionMap scans all types and returns a set of base names that have collisions
func (e *ApiMethods) buildCollisionMap() map[string]bool {
	// Map from base name -> set of unique types with that name
	nameToTypes := make(map[string]map[reflect.Type]bool)

	collectType := func(t reflect.Type) {
		if t == nil {
			return
		}
		ut := underlying(t)
		if ut.Name() == "" {
			return // anonymous types don't collide by name
		}
		baseName := e.baseTypeName(ut)
		if nameToTypes[baseName] == nil {
			nameToTypes[baseName] = make(map[reflect.Type]bool)
		}
		nameToTypes[baseName][ut] = true
	}

	// Error type
	if et := e.ensureErrorType(); et != nil && et.Kind() == reflect.Struct {
		collectType(et)
	}

	// All endpoint types
	for _, ep := range e.endpoints {
		collectType(ep.Action.ArgsType)
		collectType(ep.Action.ResultType)
	}

	// Build collision set - names that have more than one unique type
	collisions := make(map[string]bool)
	for name, types := range nameToTypes {
		if len(types) > 1 {
			collisions[name] = true
		}
	}

	return collisions
}

func (e *ApiMethods) generateFreshSchema() OpenAPISchema {
	comps := OpenAPIComponents{Schemas: make(map[string]any)}
	seen := map[reflect.Type]string{} // type -> component name
	collisions := e.buildCollisionMap()
	opIdCollisions := e.buildOperationIdCollisionMap()
	ref := jsonschema.Reflector{}

	// Register formatters with the reflector using TypeMapping
	for _, f := range e.formatters {
		var s jsonschema.Schema
		m := make(map[string]any)
		f.UpdateSchema(m)
		b, _ := json.Marshal(m)
		_ = s.UnmarshalJSON(b)

		ref.AddTypeMapping(reflect.New(f.Type()).Elem().Interface(), s)
	}

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
		name := e.addComponentSchemaWithReflector(&ref, et, comps.Schemas, seen, collisions)
		errRef = map[string]any{"$ref": "#/components/schemas/" + name}

		// Fix error schema
		e.fixSchema(comps.Schemas[name], comps.Schemas)
	}

	for _, ep := range e.endpoints {
		// Register args/result schemas
		argName := e.addComponentSchemaWithReflector(&ref, ep.Action.ArgsType, comps.Schemas, seen, collisions)
		resName := e.addComponentSchemaWithReflector(&ref, ep.Action.ResultType, comps.Schemas, seen, collisions)

		// Fix schema (binary handling, flatten definitions, fix $ref prefixes)
		e.fixSchema(comps.Schemas[argName], comps.Schemas)
		e.fixSchema(comps.Schemas[resName], comps.Schemas)

		// Determine operationId with priority:
		// a) Method.Name if provided
		// b) Action.Name if it doesn't start with "func"
		// c) Last part of path (e.g., "signup" from "/auth/signup")
		operationId := ep.Name
		if operationId == "" && !strings.HasPrefix(ep.Action.Name, "func") {
			operationId = ep.Action.Name
		}
		if operationId == "" {
			// Extract last part of path
			if idx := strings.LastIndex(ep.Path, "/"); idx >= 0 {
				operationId = ep.Path[idx+1:]
			} else {
				operationId = ep.Path
			}
		}

		// Prefix with service name if there's a collision
		if opIdCollisions[operationId] && ep.Service != "" {
			operationId = ep.Service + operationId
		}

		// Check if args has io.Reader
		hasFile := false
		argsType := ep.Action.ArgsType
		if argsType.Kind() == reflect.Ptr {
			argsType = argsType.Elem()
		}
		if argsType.Kind() == reflect.Struct {
			for i := 0; i < argsType.NumField(); i++ {
				if argsType.Field(i).Type == readerType {
					hasFile = true
					break
				}
			}
		}

		contentType := "application/json"
		if hasFile {
			contentType = "multipart/form-data"
		}

		path := OpenAPIPath{
			OperationId: operationId,
			Summary:     ep.Description,
			Description: ep.Description,
			RequestBody: OpenAPIRequestBody{
				Required: true,
				Content: map[string]OpenAPIMediaType{
					contentType: {Schema: map[string]any{"$ref": "#/components/schemas/" + argName}},
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
func (e *ApiMethods) addComponentSchemaWithReflector(r *jsonschema.Reflector, t reflect.Type, registry map[string]any, seen map[reflect.Type]string, collisions map[string]bool) string {
	if t == nil {
		// Represent "any" as an unconstrained schema.
		return e.ensureNamedComponentAny(registry, "Any", map[string]any{})
	}

	ut := underlying(t)

	if name, ok := seen[ut]; ok {
		return name
	}

	name := e.typeName(ut, collisions)

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

	// Apply formatters if any
	for _, f := range e.formatters {
		if f.Type() == ut {
			f.UpdateSchema(node)
		}
	}

	registry[name] = node
	return name
}

// baseTypeName returns just the type name without any package prefix
func (e *ApiMethods) baseTypeName(t reflect.Type) string {
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

// typeName returns the OpenAPI component name, with package prefix if there's a collision
func (e *ApiMethods) typeName(t reflect.Type, collisions map[string]bool) string {
	baseName := e.baseTypeName(t)

	if collisions[baseName] {
		// Add package prefix to disambiguate
		pkgPath := t.PkgPath()
		if pkgPath != "" {
			// Get last segment of package path and capitalize it
			pkgName := pkgPath
			if idx := strings.LastIndex(pkgPath, "/"); idx >= 0 {
				pkgName = pkgPath[idx+1:]
			}
			// Capitalize first letter
			if len(pkgName) > 0 {
				pkgName = strings.ToUpper(pkgName[:1]) + pkgName[1:]
			}
			return pkgName + baseName
		}
	}

	return baseName
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

type formatterExposer struct {
	f TypeFormatter
}

func (fe formatterExposer) JSONSchema() (jsonschema.Schema, error) {
	var s jsonschema.Schema
	m := make(map[string]any)
	fe.f.UpdateSchema(m)

	// Convert map back to Schema using JSON marshal/unmarshal
	// This is slightly inefficient but ensures we correctly map the generic map to the Schema struct
	b, err := json.Marshal(m)
	if err != nil {
		return s, err
	}
	err = s.UnmarshalJSON(b)
	return s, err
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.TrimSpace(s)
	s = nonWord.ReplaceAllString(s, "_")
	return s
}
