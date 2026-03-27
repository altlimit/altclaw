package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitize_RelativePath(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "foo.txt"), []byte("x"), 0644)

	result, err := SanitizePath(workspace, "foo.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(workspace, "foo.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSanitize_TraversalBlocked(t *testing.T) {
	workspace := t.TempDir()
	_, err := SanitizePath(workspace, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
}

func TestSanitize_AbsoluteOutside(t *testing.T) {
	workspace := t.TempDir()
	// Use a path that is definitely outside the workspace and absolute on the current OS
	outsidePath := "/etc/passwd"
	if filepath.Separator == '\\' {
		outsidePath = `C:\Windows\System32\cmd.exe`
	}
	_, err := SanitizePath(workspace, outsidePath)
	if err == nil {
		t.Fatal("expected error for absolute path outside workspace")
	}
}

func TestSanitize_AbsoluteInside(t *testing.T) {
	workspace := t.TempDir()
	// Create a file inside workspace
	target := filepath.Join(workspace, "inner.txt")
	os.WriteFile(target, []byte("data"), 0644)

	result, err := SanitizePath(workspace, target)
	if err != nil {
		t.Fatalf("absolute path inside workspace should be allowed: %v", err)
	}
	if result != target {
		t.Errorf("expected %q, got %q", target, result)
	}
}

func TestSanitize_NewFileInExistingDir(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "subdir"), 0755)

	// File doesn't exist yet, but parent dir does
	result, err := SanitizePath(workspace, "subdir/newfile.txt")
	if err != nil {
		t.Fatalf("new file in existing dir should be allowed: %v", err)
	}
	expected := filepath.Join(workspace, "subdir", "newfile.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSanitize_DotSlash(t *testing.T) {
	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("x"), 0644)

	result, err := SanitizePath(workspace, "./test.txt")
	if err != nil {
		t.Fatalf("./test.txt should work: %v", err)
	}
	expected := filepath.Join(workspace, "test.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSanitize_DotDotInMiddle(t *testing.T) {
	workspace := t.TempDir()
	os.MkdirAll(filepath.Join(workspace, "a", "b"), 0755)
	os.WriteFile(filepath.Join(workspace, "a", "target.txt"), []byte("x"), 0644)

	// a/b/../target.txt should resolve to a/target.txt, which is inside workspace
	result, err := SanitizePath(workspace, "a/b/../target.txt")
	if err != nil {
		t.Fatalf("a/b/../target.txt should resolve inside workspace: %v", err)
	}
	expected := filepath.Join(workspace, "a", "target.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
