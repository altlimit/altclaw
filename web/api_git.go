package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitLog returns the git history log for the workspace.
// GET /api/git-log?limit=20&skip=0
func (a *Api) GitLog(r *http.Request) any {
	n := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			n = parsed
		}
	}
	skip := 0
	if v := r.URL.Query().Get("skip"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			skip = parsed
		}
	}

	repo, err := openGitRepo(a.server.store.Workspace().ID)
	if err != nil {
		return map[string]any{"commits": []any{}, "has_more": false}
	}

	headRef, err := repo.Head()
	if err != nil {
		return map[string]any{"commits": []any{}, "has_more": false}
	}

	iter, err := repo.Log(&git.LogOptions{From: headRef.Hash()})
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "git log failed"}
	}

	type commitEntry struct {
		Hash    string        `json:"hash"`
		Message string        `json:"message"`
		Date    string        `json:"date"`
		Files   []webDiffFile `json:"files"`
	}

	var results []commitEntry
	index := 0
	hasMore := false
	_ = iter.ForEach(func(c *object.Commit) error {
		if index < skip {
			index++
			return nil
		}
		if len(results) >= n {
			hasMore = true
			return fmt.Errorf("done")
		}
		index++

		var files []webDiffFile
		if c.NumParents() > 0 {
			parent, pErr := c.Parent(0)
			if pErr == nil {
				files = diffTreesWeb(repo, parent.TreeHash, c.TreeHash, "")
			}
		} else {
			tree, tErr := c.Tree()
			if tErr == nil {
				tree.Files().ForEach(func(f *object.File) error {
					files = append(files, webDiffFile{Path: f.Name, Status: "added"})
					return nil
				})
			}
		}

		results = append(results, commitEntry{
			Hash:    c.Hash.String()[:7],
			Message: strings.TrimSpace(c.Message),
			Date:    c.Author.When.Format(time.RFC3339),
			Files:   files,
		})
		return nil
	})

	if results == nil {
		results = []commitEntry{}
	}
	return map[string]any{"commits": results, "has_more": hasMore}
}

// GitDiff returns the diff for a specific file at a specific commit.
// GET /api/git-diff?commit=abc1234&path=src/app.js
func (a *Api) GitDiff(r *http.Request) any {
	commitHash := r.URL.Query().Get("commit")
	filePath := r.URL.Query().Get("path")

	if commitHash == "" || filePath == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "commit and path required"}
	}

	repo, err := openGitRepo(a.server.store.Workspace().ID)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "no git history"}
	}

	// Resolve the commit
	commitObj, err := resolveCommitWeb(repo, commitHash)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "commit not found"}
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to read tree"}
	}

	// Get the file content at this commit
	var newContent string
	f, fErr := tree.File(filePath)
	if fErr == nil {
		newContent, _ = f.Contents()
	}

	// Get parent's version of the file
	var oldContent string
	if commitObj.NumParents() > 0 {
		parent, pErr := commitObj.Parent(0)
		if pErr == nil {
			parentTree, tErr := parent.Tree()
			if tErr == nil {
				pf, pfErr := parentTree.File(filePath)
				if pfErr == nil {
					oldContent, _ = pf.Contents()
				}
			}
		}
	}

	type diffResult struct {
		Path       string `json:"path"`
		OldContent string `json:"old_content"`
		NewContent string `json:"new_content"`
		Commit     string `json:"commit"`
	}

	return diffResult{
		Path:       filePath,
		OldContent: oldContent,
		NewContent: newContent,
		Commit:     commitHash,
	}
}

// GitRestore restores a file from a specific commit.
// POST /api/git-restore?commit=abc1234&path=src/app.js
func (a *Api) GitRestore(r *http.Request) any {
	if r.Method != http.MethodPost {
		return restruct.Error{Status: http.StatusMethodNotAllowed}
	}

	commitHash := r.URL.Query().Get("commit")
	filePath := r.URL.Query().Get("path")

	if commitHash == "" || filePath == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "commit and path required"}
	}

	repo, err := openGitRepo(a.server.store.Workspace().ID)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "no git history"}
	}

	commitObj, err := resolveCommitWeb(repo, commitHash)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "commit not found"}
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to read tree"}
	}

	f, err := tree.File(filePath)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "file not found at commit"}
	}

	content, err := f.Contents()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to read file"}
	}

	// Write to workspace
	absPath := filepath.Join(a.server.store.Workspace().Path, filepath.FromSlash(filePath))
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to create directory"}
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to write file"}
	}

	return map[string]string{"status": "restored", "path": filePath}
}

// GitRestoreCommit restores a commit snapshot to the workspace.
// POST /api/git-restore-commit?commit=abc1234
func (a *Api) GitRestoreCommit(r *http.Request) any {
	if r.Method != http.MethodPost {
		return restruct.Error{Status: http.StatusMethodNotAllowed}
	}

	commitHash := r.URL.Query().Get("commit")
	if commitHash == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "commit required"}
	}

	repo, err := openGitRepo(a.server.store.Workspace().ID)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "no git history"}
	}

	commitObj, err := resolveCommitWeb(repo, commitHash)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Message: "commit not found"}
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to read tree"}
	}

	err = tree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return nil // skip if we can't read it
		}
		absPath := filepath.Join(a.server.store.Workspace().Path, filepath.FromSlash(f.Name))
		dir := filepath.Dir(absPath)
		os.MkdirAll(dir, 0755)
		os.WriteFile(absPath, []byte(content), 0644)
		return nil
	})

	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: "failed to write snapshot files"}
	}

	return map[string]string{"status": "restored", "commit": commitHash}
}

// openGitRepo opens the bare git history repo for a workspace.
func openGitRepo(wsID string) (*git.Repository, error) {
	repoDir := filepath.Join(config.ConfigDir(), wsID, "git")
	return git.PlainOpen(repoDir)
}

// resolveCommitWeb resolves a partial hash to a commit.
func resolveCommitWeb(repo *git.Repository, ref string) (*object.Commit, error) {
	// Try full or partial hash via iteration
	headRef, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("no commits")
	}
	iter, _ := repo.Log(&git.LogOptions{From: headRef.Hash()})
	var found *object.Commit
	iter.ForEach(func(c *object.Commit) error {
		if strings.HasPrefix(c.Hash.String(), ref) {
			found = c
			return fmt.Errorf("found")
		}
		return nil
	})
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("commit %q not found", ref)
}

type webDiffFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// diffTreesWeb compares two trees recursively and returns changed files.
func diffTreesWeb(repo *git.Repository, oldTreeHash, newTreeHash plumbing.Hash, prefix string) []webDiffFile {
	var changes []webDiffFile

	oldTree, oldErr := repo.TreeObject(oldTreeHash)
	newTree, newErr := repo.TreeObject(newTreeHash)

	if oldErr != nil && newErr != nil {
		return nil
	}

	oldMap := make(map[string]object.TreeEntry)
	newMap := make(map[string]object.TreeEntry)

	if oldErr == nil {
		for _, e := range oldTree.Entries {
			oldMap[e.Name] = e
		}
	}
	if newErr == nil {
		for _, e := range newTree.Entries {
			newMap[e.Name] = e
		}
	}

	isDir := func(e object.TreeEntry) bool {
		return e.Mode == 0040000 // filemode.Dir
	}

	for name, newEntry := range newMap {
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "/" + name
		}

		oldEntry, existed := oldMap[name]
		if !existed {
			if isDir(newEntry) {
				changes = append(changes, diffTreesWeb(repo, plumbing.ZeroHash, newEntry.Hash, fullPath)...)
			} else {
				changes = append(changes, webDiffFile{Path: fullPath, Status: "added"})
			}
		} else if oldEntry.Hash != newEntry.Hash {
			if isDir(newEntry) && isDir(oldEntry) {
				changes = append(changes, diffTreesWeb(repo, oldEntry.Hash, newEntry.Hash, fullPath)...)
			} else {
				changes = append(changes, webDiffFile{Path: fullPath, Status: "modified"})
			}
		}
	}

	for name, oldEntry := range oldMap {
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "/" + name
		}
		if _, exists := newMap[name]; !exists {
			if isDir(oldEntry) {
				changes = append(changes, diffTreesWeb(repo, oldEntry.Hash, plumbing.ZeroHash, fullPath)...)
			} else {
				changes = append(changes, webDiffFile{Path: fullPath, Status: "deleted"})
			}
		}
	}

	return changes
}
