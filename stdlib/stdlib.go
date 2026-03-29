// Package stdlib provides embedded JavaScript module files
// that are compiled into the altclaw binary.
package stdlib

import (
	"embed"
	"math"
	"regexp"
	"strings"
)

//go:embed *.js
var FS embed.FS

//go:embed docs/*.md
var docsFS embed.FS

// Doc returns the documentation for a given name (e.g., "fs", "browser").
// Returns empty string if not found.
func Doc(name string) string {
	data, err := docsFS.ReadFile("docs/" + name + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DocList returns all available doc names (without .md extension).
func DocList() []string {
	entries, err := docsFS.ReadDir("docs")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	return names
}

// ModuleInfo describes an embedded stdlib module file.
type ModuleInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
	Filename    string `json:"filename"`
}

var metaRe = regexp.MustCompile(`@(\w+)\s+(.+)`)

// List returns metadata for all embedded stdlib module files,
// followed by any dynamic modules from the registered provider.
func List() []ModuleInfo {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil
	}

	var modules []ModuleInfo
	embeddedNames := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		data, err := FS.ReadFile(e.Name())
		if err != nil {
			continue
		}
		info := ModuleInfo{Filename: e.Name()}
		src := string(data)
		// Parse metadata from /** ... */ block at top of file
		if idx := strings.Index(src, "/**"); idx >= 0 {
			if end := strings.Index(src[idx:], "*/"); end >= 0 {
				block := src[idx : idx+end+2]
				for _, match := range metaRe.FindAllStringSubmatch(block, -1) {
					switch match[1] {
					case "name":
						info.Name = strings.TrimRight(match[2], "\r")
					case "description":
						info.Description = strings.TrimRight(match[2], "\r")
					case "example":
						info.Example = strings.TrimRight(match[2], "\r")
					}
				}
			}
		}
		if info.Name == "" {
			info.Name = strings.TrimSuffix(e.Name(), ".js")
		}
		embeddedNames[info.Name] = true
		modules = append(modules, info)
	}

	return modules
}

// Load returns the JavaScript source for a given module name or filename (stdlib only).
func Load(name string) (string, bool) {
	// Try name.js first
	data, err := FS.ReadFile(name + ".js")
	if err == nil {
		return string(data), true
	}
	// Try exact filename
	data, err = FS.ReadFile(name)
	if err == nil {
		return string(data), true
	}
	return "", false
}

// Summary returns a compact string listing all stdlib modules
// for inclusion in the system prompt.
func Summary() string {
	modules := List()
	var lines []string
	for _, m := range modules {
		if m.Name == "help" {
			continue
		}
		line := "  " + m.Name + " — " + m.Description
		if m.Example != "" {
			line += " (e.g. " + m.Example + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Available built-in modules via require(\"{name}\"). Use doc.read(name) before first use:\n" + strings.Join(lines, "\n")
}

// Signatures extracts function signatures from a module's JS source.
func Signatures(name string) string {
	src, ok := Load(name)
	if !ok {
		return ""
	}

	var sigs []string
	lines := strings.Split(src, "\n")
	var commentBuf []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "// @") {
			cleanLine := strings.TrimPrefix(trimmed, "//")
			if strings.HasPrefix(cleanLine, " ") {
				cleanLine = cleanLine[1:]
			}
			commentBuf = append(commentBuf, cleanLine)
		} else if strings.Contains(trimmed, ": function") || strings.Contains(trimmed, "module.exports = function") {
			if len(commentBuf) > 0 {
				sigs = append(sigs, strings.Join(commentBuf, "\n  "))
			}
			commentBuf = nil
		} else if trimmed != "" {
			if !strings.HasPrefix(trimmed, "/**") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "*/") {
				commentBuf = nil
			}
		}
	}

	if len(sigs) == 0 {
		return ""
	}
	return strings.Join(sigs, "\n\n")
}

// docSigRe matches signature lines in doc files: "* prefix.method(args) → returnType"
var docSigRe = regexp.MustCompile(`^\*\s+\w+\.(\w+)\(([^)]*)\)\s*→\s*(.+)$`)

// DocMethodSignatures extracts method signatures from a doc file for a given
// object prefix (e.g. "client", "handle", "conn"). Returns a map of method
// name → short signature string like "list(mailbox?, opts?) → [{uid,...}]".
func DocMethodSignatures(docName string) map[string]string {
	doc := Doc(docName)
	if doc == "" {
		return nil
	}
	result := make(map[string]string)
	for _, line := range strings.Split(doc, "\n") {
		m := docSigRe.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil {
			// m[1] = method name, m[2] = args, m[3] = return type
			result[m[1]] = m[1] + "(" + m[2] + ") → " + m[3]
		}
	}
	return result
}

// Find performs a keyword-overlap search across all modules (embedded + dynamic).
// Returns the source of the best matching module, or false if no match.
func Find(query string) (string, bool) {
	name, ok := FindName(query)
	if !ok {
		return "", false
	}
	return Load(name)
}

// FindName performs a keyword-overlap search across all modules (embedded + dynamic).
// Returns the name of the best matching module, or false if no match.
func FindName(query string) (string, bool) {
	if query == "" {
		return "", false
	}

	queryWords := tokenize(query)
	if len(queryWords) == 0 {
		return "", false
	}

	bestScore := 0.0
	bestName := ""

	// 1. Search embedded JS modules by name + description + example
	modules := List()
	for _, m := range modules {
		corpus := m.Name + " " + m.Description + " " + m.Example
		corpusWords := tokenize(corpus)
		if len(corpusWords) == 0 {
			continue
		}
		score := overlapScore(queryWords, corpusWords)
		if score > bestScore {
			bestScore = score
			bestName = m.Name
		}
	}

	// 2. Search docs/*.md by name + content (bridges, globals, etc.)
	for _, docName := range DocList() {
		content := Doc(docName)
		corpus := docName + " " + content
		corpusWords := tokenize(corpus)
		if len(corpusWords) == 0 {
			continue
		}
		score := overlapScore(queryWords, corpusWords)
		if score > bestScore {
			bestScore = score
			bestName = docName
		}
	}

	if bestName == "" || bestScore < 0.1 {
		return "", false
	}
	return bestName, true
}

// tokenize splits text into lowercase words, removing punctuation.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := make(map[string]bool)
	var result []string
	for _, w := range words {
		if len(w) > 1 && !seen[w] {
			seen[w] = true
			result = append(result, w)
		}
	}
	return result
}

// overlapScore calculates a cosine-like similarity between query and corpus word sets.
func overlapScore(queryWords, corpusWords []string) float64 {
	corpusSet := make(map[string]bool, len(corpusWords))
	for _, w := range corpusWords {
		corpusSet[w] = true
	}
	matches := 0
	for _, qw := range queryWords {
		if corpusSet[qw] {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return float64(matches) / math.Sqrt(float64(len(queryWords))*float64(len(corpusWords)))
}
