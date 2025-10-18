package openapikit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oliverkofoed/gokit/logkit"
)

// GenerateClient generates a client using openapi-generator-cli in the target folder
// if the API endpoints checksum has changed compared to the .apichecksum file
// generator and additionalArgs are passed directly to openapi-generator-cli
func (e *ApiMethods) GenerateClient(context context.Context, generator string, folder string, additionalArgs ...string) {
	// Calculate current API checksum
	currentChecksum, err := e.calculateChecksum()
	if err != nil {
		panic(err)
	}

	// Check if checksum file exists and read it
	checksumFile := filepath.Join(folder, ".apichecksum")
	storedChecksum, err := readChecksumFile(checksumFile)
	if err != nil && !os.IsNotExist(err) {
		panic(err)
	}

	// If checksums match, no need to regenerate
	if storedChecksum == currentChecksum {
		return
	}

	// Generate client using openapi-generator-cli
	logkit.Info(context, fmt.Sprintf("Generating OpenAPI %v client in %v", generator, folder))
	if err := e.generateClientWithOpenAPIGenerator(generator, folder, additionalArgs...); err != nil {
		logkit.Error(context, "failed to generate client", logkit.Err(err), logkit.String("generator", generator), logkit.String("folder", folder))
		return
	}

	// Write new checksum
	if err := writeChecksumFile(checksumFile, currentChecksum); err != nil {
		logkit.Error(context, "failed to write checksum file", logkit.Err(err))
	}
}

// calculateChecksum creates a SHA256 hash of the API endpoints structure
func (e *ApiMethods) calculateChecksum() (string, error) {
	// Return cached checksum if available
	if e.schemaChecksumCache != "" {
		return e.schemaChecksumCache, nil
	}

	// Generate schema (this will cache it if not already cached)
	schema := e.GenerateOpenAPISchema()

	// Convert to JSON for hashing
	data, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}

	// Calculate SHA256
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])
	e.schemaChecksumCache = checksum
	return checksum, nil
}

// readChecksumFile reads the stored checksum from file
func readChecksumFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeChecksumFile writes the checksum to file
func writeChecksumFile(path string, checksum string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(checksum), 0644)
}

// generateClientWithOpenAPIGenerator generates a client using openapi-generator JAR
func (e *ApiMethods) generateClientWithOpenAPIGenerator(generator string, folder string, additionalArgs ...string) error {
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

	// Add any additional arguments provided by the caller
	if len(additionalArgs) > 0 {
		cmd.Args = append(cmd.Args, additionalArgs...)
	}

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openapi-generator failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}
