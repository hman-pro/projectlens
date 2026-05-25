package projects

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	schemaPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// ValidateSlug enforces the project slug shape used in CLI flags and MCP URL paths.
func ValidateSlug(s string) error {
	if s == "" {
		return fmt.Errorf("slug is empty")
	}
	if !slugPattern.MatchString(s) {
		return fmt.Errorf("invalid slug %q: must match %s", s, slugPattern)
	}
	return nil
}

// ValidateStorageSchema enforces a safe Postgres identifier and rejects reserved names.
func ValidateStorageSchema(s string) error {
	if s == "" {
		return fmt.Errorf("storage_schema is empty")
	}
	if !schemaPattern.MatchString(s) {
		return fmt.Errorf("invalid storage_schema %q: must match %s", s, schemaPattern)
	}
	if s == "public" {
		return fmt.Errorf("storage_schema %q is reserved (used for legacy single-project installs)", s)
	}
	if strings.HasPrefix(s, "pg_") {
		return fmt.Errorf("storage_schema %q starts with reserved prefix pg_", s)
	}
	return nil
}
