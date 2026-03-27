package stdlib

import (
	"strings"
	"testing"
)

func TestList_ReturnsWebJS(t *testing.T) {
	modules := List()
	if len(modules) == 0 {
		t.Fatal("expected at least one module")
	}

	found := false
	for _, m := range modules {
		if m.Filename == "web.js" {
			found = true
			if m.Name == "" {
				t.Error("web.js should have a name")
			}
		}
	}
	if !found {
		t.Error("web.js not found in module list")
	}
}

func TestList_ModuleCount(t *testing.T) {
	modules := List()
	if len(modules) != 4 {
		t.Errorf("expected 4 modules (web, mcp, pkg, servertest), got %d", len(modules))
		for _, m := range modules {
			t.Logf("  found: %s (%s)", m.Filename, m.Name)
		}
	}
}

func TestLoad_ExistingModule(t *testing.T) {
	src, ok := Load("web")
	if !ok {
		t.Fatal("expected web module to load")
	}
	if src == "" {
		t.Error("expected non-empty source")
	}
}

func TestLoad_ExistingModuleByFilename(t *testing.T) {
	src, ok := Load("web.js")
	if !ok {
		t.Fatal("expected web.js to load by filename")
	}
	if src == "" {
		t.Error("expected non-empty source")
	}
}

func TestDoc_ExistingDoc(t *testing.T) {
	doc := Doc("fs")
	if doc == "" {
		t.Fatal("expected non-empty fs doc")
	}
	if !strings.Contains(doc, "fs.read") {
		t.Error("expected fs doc to contain 'fs.read'")
	}
}

func TestDoc_NonExistent(t *testing.T) {
	doc := Doc("nonexistent")
	if doc != "" {
		t.Errorf("expected empty doc for nonexistent, got %q", doc)
	}
}

func TestDocList(t *testing.T) {
	names := DocList()
	if len(names) == 0 {
		t.Fatal("expected at least one doc")
	}
	found := false
	for _, n := range names {
		if n == "fs" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'fs' in doc list")
	}
}

func TestLoad_NonExistent(t *testing.T) {
	_, ok := Load("nonexistent")
	if ok {
		t.Fatal("expected nonexistent module to not load")
	}
}

func TestList_HasMetadata(t *testing.T) {
	modules := List()
	for _, m := range modules {
		if m.Filename == "" {
			t.Error("module has empty filename")
		}
		if m.Name == "" {
			t.Errorf("module %s has empty name", m.Filename)
		}
	}
}

func TestFind_WebScrape(t *testing.T) {
	src, ok := Find("web scrape headless chrome")
	if !ok {
		t.Fatal("expected Find to match web module")
	}
	if !strings.Contains(src, "scrape") {
		t.Error("expected web module source containing 'scrape'")
	}
}

func TestFind_NoMatch(t *testing.T) {
	_, ok := Find("xyzzy gibberish nonexistent")
	if ok {
		t.Error("expected no match for gibberish query")
	}
}

func TestFind_EmptyQuery(t *testing.T) {
	_, ok := Find("")
	if ok {
		t.Error("expected no match for empty query")
	}
}

func TestSignatures_Web(t *testing.T) {
	sigs := Signatures("web")
	if sigs == "" {
		t.Fatal("expected non-empty signatures for web module")
	}
	if !strings.Contains(sigs, "scrape") {
		t.Error("expected signatures to contain 'scrape'")
	}
	if !strings.Contains(sigs, "snap") {
		t.Error("expected signatures to contain 'snap'")
	}
}

func TestSignatures_NonExistent(t *testing.T) {
	sigs := Signatures("nonexistent")
	if sigs != "" {
		t.Errorf("expected empty signatures for nonexistent module, got %q", sigs)
	}
}
