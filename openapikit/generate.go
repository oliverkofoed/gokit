package openapikit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GenerateClient generates a client using openapi-generator-cli in the target folder
// if the API endpoints checksum has changed compared to the .apichecksum file
// generator and additionalArgs are passed directly to openapi-generator-cli
func (e *ApiMethods) GenerateClient(generator string, folder string, additionalArgs ...string) {
	// Calculate current API checksum
	currentChecksum, err := e.calculateChecksum()
	if err != nil {
		fmt.Println("failed to calculate API checksum:", err)
		return
	}

	// Check if checksum file exists and read it
	checksumFile := filepath.Join(folder, ".apichecksum")
	storedChecksum, err := readChecksumFile(checksumFile)
	if err != nil && !os.IsNotExist(err) {
		fmt.Println("failed to read checksum file:", err)
		return
	}

	// If checksums match, no need to regenerate
	if storedChecksum == currentChecksum {
		fmt.Println("checksums match, no need to regenerate")
		return
	}

	// Generate client using openapi-generator-cli
	if err := e.generateClientWithOpenAPIGenerator(generator, folder, additionalArgs...); err != nil {
		fmt.Printf("failed to generate %s client: %v\n", generator, err)
		return
	}

	// Write new checksum
	if err := writeChecksumFile(checksumFile, currentChecksum); err != nil {
		fmt.Println("failed to write checksum file: %w", err)
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

// generateClientWithOpenAPIGenerator generates a client using openapi-generator-cli
func (e *ApiMethods) generateClientWithOpenAPIGenerator(generator string, folder string, additionalArgs ...string) error {
	// Create temporary OpenAPI spec file
	schema := e.GenerateOpenAPISchema()
	specData, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OpenAPI schema: %w", err)
	}

	specFile := filepath.Join(folder, "openapi-spec.json")
	if err := os.WriteFile(specFile, specData, 0644); err != nil {
		return fmt.Errorf("failed to write OpenAPI spec: %w", err)
	}
	defer os.Remove(specFile) // Clean up temp file

	// Ensure output directory exists
	if err := os.MkdirAll(folder, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Run openapi-generator-cli
	cmd := exec.Command("openapi-generator-cli", "generate",
		"-i", specFile,
		"-g", generator,
		"-o", folder,
		"--skip-validate-spec")

	// Add any additional arguments provided by the caller
	if len(additionalArgs) > 0 {
		cmd.Args = append(cmd.Args, additionalArgs...)
	}

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openapi-generator-cli failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}
