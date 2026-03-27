package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Patch applies non-zero fields from src onto dst using JSON round-trip.
// Unknown fields (not matching any json tag on dst) cause an error.
// If the unknown field looks like camelCase, the error suggests the snake_case equivalent.
func Patch(src any, dst any) error {
	srcData, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("failed to marshal src: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(srcData))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		errMsg := err.Error()
		// Extract field name from json error like `json: unknown field "rateLimit"`
		if after, ok := strings.CutPrefix(errMsg, "json: unknown field "); ok {
			field := strings.Trim(after, "\"")
			snake := toSnakeCase(field)
			if snake != field {
				return fmt.Errorf("unknown field %q, did you mean %q?", field, snake)
			}
			return fmt.Errorf("unknown field %q", field)
		}
		return fmt.Errorf("failed to patch: %w", err)
	}
	return nil
}

// toSnakeCase converts camelCase to snake_case for error suggestions.
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
