package openapikit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oliverkofoed/gokit/logkit"
)

// GenerateClient generates a client using openapi-generator-cli in the target folder
// if the API schema has changed compared to the stored .apischema.json file
// generator is the openapi-generator generator name (e.g. "typescript-fetch")
// additionalProperties is a map of additional properties to pass to the generator
func (e *ApiMethods) GenerateClient(context context.Context, generator string, folder string, additionalProperties map[string]string) {
	// Generate current schema
	schema := e.GenerateOpenAPISchema()
	currentSchema, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		panic(err)
	}

	// Check if schema file exists and read it
	schemaFile := filepath.Join(folder, "openapi.schema.json")
	storedSchema, err := os.ReadFile(schemaFile)
	if err != nil && !os.IsNotExist(err) {
		panic(err)
	}

	// If schemas match, no need to regenerate
	if bytes.Equal(storedSchema, currentSchema) {
		return
	}

	// Delete the target folder to ensure clean generation (no leftover files)
	if err := os.RemoveAll(folder); err != nil && !os.IsNotExist(err) {
		logkit.Error(context, "failed to remove target folder", logkit.Err(err), logkit.String("folder", folder))
		return
	}

	// Generate client using openapi-generator-cli
	logkit.Info(context, fmt.Sprintf("Generating OpenAPI %v client in %v", generator, folder))
	if err := e.generateClientWithOpenAPIGenerator(generator, folder, additionalProperties); err != nil {
		logkit.Error(context, "failed to generate client", logkit.Err(err), logkit.String("generator", generator), logkit.String("folder", folder))
		return
	}

	// Write new schema
	if err := os.MkdirAll(folder, 0755); err != nil {
		logkit.Error(context, "failed to create directory", logkit.Err(err))
		return
	}
	if err := os.WriteFile(schemaFile, currentSchema, 0644); err != nil {
		logkit.Error(context, "failed to write schema file", logkit.Err(err))
	}
}

// generateClientWithOpenAPIGenerator generates a client using openapi-generator JAR
func (e *ApiMethods) generateClientWithOpenAPIGenerator(generator string, folder string, additionalProperties map[string]string) error {
	// Create temporary OpenAPI spec file
	schema := e.GenerateOpenAPISchema()
	specData, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OpenAPI schema: %w", err)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(folder, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write OpenAPI spec file
	specFile := filepath.Join(folder, "openapi-spec.json")
	if err := os.WriteFile(specFile, specData, 0644); err != nil {
		return fmt.Errorf("failed to write OpenAPI spec: %w", err)
	}
	defer os.Remove(specFile) // Clean up temp file

	// Run openapi-generator using JAR directly
	cmd := exec.Command("java", "-jar", "/usr/local/lib/openapi-generator-cli.jar", "generate",
		"-i", specFile,
		"-g", generator,
		"-o", folder)

	// Add any additional properties provided by the caller
	if len(additionalProperties) > 0 {
		props := ""
		for key, value := range additionalProperties {
			if props != "" {
				props += ","
			}
			props += key + "=" + value
		}
		cmd.Args = append(cmd.Args, "--additional-properties="+props)
	}

	// print command
	fmt.Println("generate: " + strings.Join(cmd.Args, " "))

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openapi-generator failed: %w\nOutput: %s", err, string(output))
	}
	fmt.Println(string(output))

	return nil
}
