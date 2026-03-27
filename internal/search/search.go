package search

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	ignore "github.com/sabhiram/go-gitignore"
)

// Result represents a single file match and snippet
type Result struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

// Workspace searches the given workspace directory for the query string.
func Workspace(workspace, query string, includeContent bool) ([]Result, error) {
	var results []Result
	if query == "" {
		return results, nil
	}

	queryBytes := []byte(query)
	
	// If the user's string doesn't look like a glob but includeContent is false, allow fallback fallback matching.
	globQuery := query
	if !strings.Contains(globQuery, "*") && !strings.Contains(globQuery, "?") {
		globQuery = "**/*" + globQuery + "*"
	}

	var ignoreParser *ignore.GitIgnore
	if gi, err := ignore.CompileIgnoreFile(filepath.Join(workspace, ".gitignore")); err == nil {
		ignoreParser = gi
	}

	err := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		rel, relErr := filepath.Rel(workspace, path)
		if relErr != nil {
			return nil
		}

		// Check gitignore rules
		if ignoreParser != nil && ignoreParser.MatchesPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Ignore hidden files and directories (starting with .)
		if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			// Ignore common massive/binary directories (fallback just in case)
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "build" || d.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		matchedFilename, _ := doublestar.Match(globQuery, rel)

		if !includeContent {
			if matchedFilename {
				results = append(results, Result{
					Path:    strings.ReplaceAll(rel, string(filepath.Separator), "/"),
					Snippet: "Filename match",
				})
			}
			// Cap results
			if len(results) >= 50 {
				return filepath.SkipAll
			}
			return nil
		}

		// Fast-path: skip known binary extensions before reading data
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico", ".mp4", ".webm", ".ogg", ".mp3", ".wav", ".pdf", ".zip", ".tar", ".gz", ".bin", ".exe", ".dll", ".so", ".dylib":
			if matchedFilename {
				results = append(results, Result{
					Path:    strings.ReplaceAll(rel, string(filepath.Separator), "/"),
					Snippet: "Filename match",
				})
				if len(results) >= 50 {
					return filepath.SkipAll
				}
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Sniff content type (up to 512 bytes)
		contentType := http.DetectContentType(data)
		isText := strings.HasPrefix(contentType, "text/") || contentType == "application/json"
		if !isText {
			if matchedFilename {
				results = append(results, Result{
					Path:    strings.ReplaceAll(rel, string(filepath.Separator), "/"),
					Snippet: "Filename match",
				})
				if len(results) >= 50 {
					return filepath.SkipAll
				}
			}
			return nil // Skip garbled binaries in search results
		}

		matchedContent := false
		if idx := strings.Index(string(data), query); idx != -1 {
			matchedContent = true
			start := idx - 30
			if start < 0 {
				start = 0
			}
			end := idx + len(queryBytes) + 30
			if end > len(data) {
				end = len(data)
			}
			snippet := string(data[start:end])
			if start > 0 {
				snippet = "..." + snippet
			}
			if end < len(data) {
				snippet = snippet + "..."
			}

			results = append(results, Result{
				Path:    strings.ReplaceAll(rel, string(filepath.Separator), "/"),
				Snippet: snippet,
			})
		}
		
		// If content didn't match, check if filename matches query natively
		if !matchedContent && matchedFilename {
			results = append(results, Result{
				Path:    strings.ReplaceAll(rel, string(filepath.Separator), "/"),
				Snippet: "Filename match",
			})
		}

		// Cap results to prevent memory blowout
		if len(results) >= 50 {
			return filepath.SkipAll
		}
		return nil
	})

	if results == nil {
		results = []Result{}
	}
	return results, err
}
