package bridge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestStringify_String(t *testing.T) {
	vm := goja.New()
	result := Stringify(vm, vm.ToValue("hello"))
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestStringify_Number(t *testing.T) {
	vm := goja.New()
	result := Stringify(vm, vm.ToValue(42))
	if result != "42" {
		t.Errorf("expected '42', got %q", result)
	}
}

func TestStringify_Object(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	obj.Set("key", "value")
	result := Stringify(vm, obj)
	expected := "{\n  \"key\": \"value\"\n}"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStringify_Array(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString("[1, 2, 3]")
	result := Stringify(vm, val)
	expected := "[\n  1,\n  2,\n  3\n]"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStringify_Null(t *testing.T) {
	vm := goja.New()
	result := Stringify(vm, goja.Null())
	if result != "null" {
		t.Errorf("expected 'null', got %q", result)
	}
}

func TestStringify_Undefined(t *testing.T) {
	vm := goja.New()
	result := Stringify(vm, goja.Undefined())
	if result != "undefined" {
		t.Errorf("expected 'undefined', got %q", result)
	}
}

func TestCheckOpts_ValidKeys(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	obj.Set("host", "smtp.gmail.com")
	obj.Set("body", "hello")
	// Should not panic
	CheckOpts(vm, "mail.send", obj, "host", "body", "to")
}

func TestCheckOpts_UnknownKey(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	obj.Set("host", "smtp.gmail.com")
	obj.Set("text", "hello") // invalid — should be "body"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unknown key 'text'")
		}
		// The error message should contain the key, suggestion, and valid list
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "text") {
			t.Errorf("error should mention the unknown key 'text': %s", msg)
		}
		if !strings.Contains(msg, "Valid parameters") {
			t.Errorf("error should list valid parameters: %s", msg)
		}
	}()

	CheckOpts(vm, "mail.send", obj, "host", "body", "to")
}

func TestCheckOpts_DidYouMean(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	obj.Set("boddy", "hello") // typo of "body"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unknown key 'boddy'")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "did you mean") {
			t.Errorf("error should include 'did you mean' suggestion: %s", msg)
		}
		if !strings.Contains(msg, "body") {
			t.Errorf("error should suggest 'body': %s", msg)
		}
	}()

	CheckOpts(vm, "mail.send", obj, "host", "body", "to")
}

func TestLevenshtein(t *testing.T) {
	tests := []struct{ a, b string; want int }{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"body", "body", 0},
		{"text", "body", 4},
		{"boddy", "body", 1},
		{"bdy", "body", 1},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
