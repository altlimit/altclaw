package util

import (
	"testing"
)

func TestPatch_BasicFields(t *testing.T) {
	type Target struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
		Flag  bool   `json:"flag"`
	}

	dst := &Target{Name: "original", Count: 10, Flag: true}
	src := map[string]any{"name": "patched", "count": float64(42)}

	if err := Patch(src, dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "patched" {
		t.Errorf("expected name 'patched', got %q", dst.Name)
	}
	if dst.Count != 42 {
		t.Errorf("expected count 42, got %d", dst.Count)
	}
	// Flag was not in src, should remain unchanged
	if !dst.Flag {
		t.Error("expected flag to remain true (not in patch)")
	}
}

func TestPatch_SliceField(t *testing.T) {
	type Target struct {
		Tags []string `json:"tags"`
		Name string   `json:"name"`
	}

	dst := &Target{Tags: []string{"old"}, Name: "keep"}
	src := map[string]any{"tags": []any{"a", "b"}}

	if err := Patch(src, dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Tags) != 2 || dst.Tags[0] != "a" || dst.Tags[1] != "b" {
		t.Errorf("expected tags [a, b], got %v", dst.Tags)
	}
	if dst.Name != "keep" {
		t.Errorf("expected name 'keep', got %q", dst.Name)
	}
}

func TestPatch_EmptySlice(t *testing.T) {
	type Target struct {
		Items []string `json:"items"`
	}

	dst := &Target{Items: []string{"old"}}
	src := map[string]any{"items": []any{}}

	if err := Patch(src, dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Items) != 0 {
		t.Errorf("expected empty items, got %v", dst.Items)
	}
}

func TestPatch_ZeroValuesApplied(t *testing.T) {
	type Target struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	dst := &Target{Name: "original", Count: 10}
	// Explicitly setting to zero/empty — should overwrite
	src := map[string]any{"name": "", "count": float64(0)}

	if err := Patch(src, dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "" {
		t.Errorf("expected empty name, got %q", dst.Name)
	}
	if dst.Count != 0 {
		t.Errorf("expected count 0, got %d", dst.Count)
	}
}

func TestPatch_NilSrc(t *testing.T) {
	type Target struct {
		Name string `json:"name"`
	}

	dst := &Target{Name: "original"}
	if err := Patch(nil, dst); err != nil {
		t.Fatal(err)
	}
	// nil map marshals to "null", which should not clear dst
	if dst.Name != "original" {
		t.Errorf("expected name 'original' after nil patch, got %q", dst.Name)
	}
}

func TestPatch_EmptyMap(t *testing.T) {
	type Target struct {
		Name string `json:"name"`
	}

	dst := &Target{Name: "original"}
	if err := Patch(map[string]any{}, dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "original" {
		t.Errorf("expected name 'original' after empty patch, got %q", dst.Name)
	}
}

func TestPatch_UnknownFieldsError(t *testing.T) {
	type Target struct {
		Name string `json:"name"`
	}

	dst := &Target{Name: "original"}
	src := map[string]any{"name": "patched", "nonexistent_field": "value"}

	err := Patch(src, dst)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestPatch_DidYouMean(t *testing.T) {
	type Target struct {
		RateLimit int64 `json:"rate_limit"`
	}

	dst := &Target{RateLimit: 5}
	src := map[string]any{"rateLimit": float64(10)}

	err := Patch(src, dst)
	if err == nil {
		t.Fatal("expected error for camelCase field, got nil")
	}
	expected := `unknown field "rateLimit", did you mean "rate_limit"?`
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}
