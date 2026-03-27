package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

// --- isBinaryContent tests ---

func TestIsBinaryContent_Text(t *testing.T) {
	data := []byte("Hello, world! This is plain text.\nLine 2.\n")
	if isBinaryContent(data) {
		t.Error("plain text should not be detected as binary")
	}
}

func TestIsBinaryContent_JSON(t *testing.T) {
	data := []byte(`{"key": "value", "num": 42}`)
	if isBinaryContent(data) {
		t.Error("JSON should not be detected as binary")
	}
}

func TestIsBinaryContent_JavaScript(t *testing.T) {
	data := []byte(`function hello() { console.log("hi"); }`)
	if isBinaryContent(data) {
		t.Error("JavaScript should not be detected as binary")
	}
}

func TestIsBinaryContent_PNG(t *testing.T) {
	// PNG magic bytes
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
	if !isBinaryContent(data) {
		t.Error("PNG header should be detected as binary")
	}
}

func TestIsBinaryContent_GIF(t *testing.T) {
	data := []byte("GIF89a\x00\x00\x00\x00")
	if !isBinaryContent(data) {
		t.Error("GIF data should be detected as binary")
	}
}

func TestIsBinaryContent_Empty(t *testing.T) {
	// Empty data — http.DetectContentType returns "text/plain" for empty
	data := []byte{}
	if isBinaryContent(data) {
		t.Error("empty data should not be detected as binary")
	}
}

// --- buildIgnoreCheckerForPath tests ---

func TestIgnoreChecker_GitDir(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if !checker(".git", true) {
		t.Error(".git directory should be ignored")
	}
	if !checker(".git", false) {
		t.Error(".git as file should be ignored")
	}
}

func TestIgnoreChecker_NodeModules(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if !checker("node_modules", true) {
		t.Error("node_modules directory should be ignored")
	}
}

func TestIgnoreChecker_BuildDist(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if !checker("build", true) {
		t.Error("build directory should be ignored")
	}
	if !checker("dist", true) {
		t.Error("dist directory should be ignored")
	}
	if !checker("__pycache__", true) {
		t.Error("__pycache__ directory should be ignored")
	}
}

func TestIgnoreChecker_HiddenFiles(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if !checker(".env", false) {
		t.Error(".env should be ignored (hidden file)")
	}
	if !checker(".DS_Store", false) {
		t.Error(".DS_Store should be ignored (hidden file)")
	}
}

func TestIgnoreChecker_AgentAllowed(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if checker(".agent", true) {
		t.Error(".agent directory should NOT be ignored")
	}
	if checker(".agent/workflows", true) {
		t.Error(".agent/workflows should NOT be ignored")
	}
}

func TestIgnoreChecker_AgentTmpIgnored(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if !checker(".agent/tmp", true) {
		t.Error(".agent/tmp directory should be ignored")
	}
	if !checker(".agent/tmp/scratch.js", false) {
		t.Error(".agent/tmp/scratch.js should be ignored")
	}
}

func TestIgnoreChecker_NormalFilesAllowed(t *testing.T) {
	workspace := t.TempDir()
	checker := buildIgnoreCheckerForPath(workspace)

	if checker("main.go", false) {
		t.Error("main.go should NOT be ignored")
	}
	if checker("src", true) {
		t.Error("src directory should NOT be ignored")
	}
	if checker("package.json", false) {
		t.Error("package.json should NOT be ignored")
	}
}

func TestIgnoreChecker_GitignoreRules(t *testing.T) {
	workspace := t.TempDir()
	// Create a .gitignore that ignores *.log files and vendor dir
	os.WriteFile(filepath.Join(workspace, ".gitignore"), []byte("*.log\nvendor\n"), 0644)

	checker := buildIgnoreCheckerForPath(workspace)

	if !checker("error.log", false) {
		t.Error("*.log should be ignored per .gitignore")
	}
	if !checker("vendor", true) {
		t.Error("vendor should be ignored per .gitignore")
	}
	if checker("main.go", false) {
		t.Error("main.go should NOT be ignored")
	}
}
