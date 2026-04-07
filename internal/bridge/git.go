package bridge

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	ignore "github.com/sabhiram/go-gitignore"
)

// isBinaryContent returns true if data looks like binary content.
func isBinaryContent(data []byte) bool {
	ct := http.DetectContentType(data)
	return !strings.HasPrefix(ct, "text/") && ct != "application/json" && ct != "application/javascript"
}

// gitFileEntry holds a workspace file for snapshotting.
type gitFileEntry struct {
	relPath string
	content []byte
}

// snapshotAndCommit walks the workspace and creates a commit in the bare repo.
// Returns the short commit hash, changed file list, or error.
// If nothing changed since the last commit, returns ("", nil, nil).
func snapshotAndCommit(repo *git.Repository, workspace, message string) (string, []string, error) {
	shouldIgnore := buildIgnoreCheckerForPath(workspace)

	// Collect all workspace files as (relPath → content)
	const maxSnapshotFiles = 10_000 // guard against extremely large workspaces
	var files []gitFileEntry
	var errTooMany = fmt.Errorf("snapshot: workspace exceeds %d files, skipping", maxSnapshotFiles)

	err := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(workspace, path)
		if relErr != nil || rel == "." {
			return nil
		}

		if shouldIgnore(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Skip large files (>2MB) and binary files
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 2*1024*1024 {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		if isBinaryContent(data) {
			return nil
		}

		// Normalize path separators to forward slashes for git
		files = append(files, gitFileEntry{
			relPath: strings.ReplaceAll(rel, string(filepath.Separator), "/"),
			content: data,
		})

		if len(files) >= maxSnapshotFiles {
			return errTooMany
		}
		return nil
	})
	if err != nil && err != errTooMany {
		return "", nil, fmt.Errorf("git: walk: %w", err)
	}

	// Build tree using go-git's object storer
	storer := repo.Storer

	// Create blob objects for each file and build tree entries
	var treeEntries []object.TreeEntry
	for _, f := range files {
		blob := &plumbing.MemoryObject{}
		blob.SetType(plumbing.BlobObject)
		blob.SetSize(int64(len(f.content)))
		w, _ := blob.Writer()
		w.Write(f.content)
		w.Close()

		h, storeErr := storer.SetEncodedObject(blob)
		if storeErr != nil {
			continue
		}

		treeEntries = append(treeEntries, object.TreeEntry{
			Name: f.relPath,
			Mode: filemode.Regular,
			Hash: h,
		})
	}

	rootHash, treeErr := buildNestedTree(storer, treeEntries)
	if treeErr != nil {
		return "", nil, fmt.Errorf("git: tree: %w", treeErr)
	}

	// Get parent commit (HEAD) if it exists
	var parentHashes []plumbing.Hash
	headRef, headErr := repo.Head()
	if headErr == nil {
		parentHashes = append(parentHashes, headRef.Hash())
	}

	// Check if tree changed from parent (skip no-op commits)
	if len(parentHashes) > 0 {
		parentCommit, err := repo.CommitObject(parentHashes[0])
		if err == nil && parentCommit.TreeHash == rootHash {
			return "", nil, nil // No changes
		}
	}

	// Determine changed files by comparing trees
	var changedFiles []string
	if len(parentHashes) > 0 {
		parentCommit, err := repo.CommitObject(parentHashes[0])
		if err == nil {
			changedFiles = diffTrees(repo, parentCommit.TreeHash, rootHash, "")
		}
	} else {
		// First commit: all files are new
		for _, f := range files {
			changedFiles = append(changedFiles, "A "+f.relPath)
		}
	}

	// Create commit
	commit := &object.Commit{
		Author: object.Signature{
			Name:  "Altclaw Agent",
			Email: "agent@altclaw.ai",
			When:  time.Now(),
		},
		Committer: object.Signature{
			Name:  "Altclaw Agent",
			Email: "agent@altclaw.ai",
			When:  time.Now(),
		},
		Message:  message,
		TreeHash: rootHash,
	}
	if len(parentHashes) > 0 {
		commit.ParentHashes = parentHashes
	}

	commitObj := &plumbing.MemoryObject{}
	commitObj.SetType(plumbing.CommitObject)
	if err := commit.Encode(commitObj); err != nil {
		return "", nil, fmt.Errorf("git: encode commit: %w", err)
	}
	commitHash, err := storer.SetEncodedObject(commitObj)
	if err != nil {
		return "", nil, fmt.Errorf("git: store commit: %w", err)
	}

	// Update HEAD ref
	refName := plumbing.ReferenceName("refs/heads/main")
	ref := plumbing.NewHashReference(refName, commitHash)
	if err := storer.SetReference(ref); err != nil {
		return "", nil, fmt.Errorf("git: update ref: %w", err)
	}

	// Also set HEAD to point to main if not already
	headSymRef := plumbing.NewSymbolicReference(plumbing.HEAD, refName)
	_ = storer.SetReference(headSymRef)

	return commitHash.String()[:7], changedFiles, nil
}

// RegisterGit adds the git namespace to the runtime.
// The agent's history repo is a bare repo at configDir/wsID/.git.
// All path operations are jailed to the workspace directory.
func RegisterGit(vm *goja.Runtime, workspace, configDir string, wsID string, store *config.Store) {
	g := vm.NewObject()

	repoDir := filepath.Join(configDir, wsID, "git")

	// openOrInit opens the agent's bare history repo, initializing it if needed.
	openOrInit := func() (*git.Repository, error) {
		if err := os.MkdirAll(repoDir, 0755); err != nil {
			return nil, fmt.Errorf("git: mkdir: %w", err)
		}
		repo, err := git.PlainOpen(repoDir)
		if err != nil {
			// Initialize bare-like repo (we use PlainInit with isBare=false,
			// but set the workspace as worktree path so object storage
			// is in configDir while worktree points to workspace)
			repo, err = git.PlainInitWithOptions(repoDir, &git.PlainInitOptions{
				Bare: true,
			})
			if err != nil {
				return nil, fmt.Errorf("git: init: %w", err)
			}
		}
		return repo, nil
	}

	// snapshotWorkspace delegates to the shared snapshotAndCommit.
	snapshotWorkspace := func(repo *git.Repository, message string) (string, []string, error) {
		return snapshotAndCommit(repo, workspace, message)
	}


	// git.commit(msg?) — manual snapshot
	g.Set("commit", func(call goja.FunctionCall) goja.Value {
		msg := "manual snapshot"
		if len(call.Arguments) >= 1 {
			msg = call.Arguments[0].String()
		}

		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.commit", err)
		}

		hash, changed, err := snapshotWorkspace(repo, msg)
		if err != nil {
			logErr(vm, "git.commit", err)
		}
		if hash == "" {
			return vm.ToValue("no changes")
		}

		obj := vm.NewObject()
		obj.Set("hash", hash)
		obj.Set("files", changed)
		return obj
	})

	// git.log(n?) — list recent commits
	g.Set("log", func(call goja.FunctionCall) goja.Value {
		n := 10
		if len(call.Arguments) >= 1 {
			n = int(call.Arguments[0].ToInteger())
			if n < 1 {
				n = 1
			}
		}

		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.log", err)
		}

		headRef, err := repo.Head()
		if err != nil {
			// No commits yet
			return vm.ToValue([]interface{}{})
		}

		iter, err := repo.Log(&git.LogOptions{From: headRef.Hash()})
		if err != nil {
			logErr(vm, "git.log", err)
		}

		var results []interface{}
		count := 0
		_ = iter.ForEach(func(c *object.Commit) error {
			if count >= n {
				return fmt.Errorf("done")
			}
			count++

			entry := vm.NewObject()
			entry.Set("hash", c.Hash.String()[:7])
			entry.Set("message", strings.TrimSpace(c.Message))
			entry.Set("date", c.Author.When.Format(time.RFC3339))

			// Get changed files by diffing against parent
			var fileList []string
			if c.NumParents() > 0 {
				parent, pErr := c.Parent(0)
				if pErr == nil {
					fileList = diffTrees(repo, parent.TreeHash, c.TreeHash, "")
				}
			} else {
				// First commit — list all files
				tree, tErr := c.Tree()
				if tErr == nil {
					tree.Files().ForEach(func(f *object.File) error {
						fileList = append(fileList, "A "+f.Name)
						return nil
					})
				}
			}
			entry.Set("files", fileList)
			results = append(results, entry)
			return nil
		})

		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// git.status() — show what changed since last commit
	g.Set("status", func(call goja.FunctionCall) goja.Value {
		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.status", err)
		}

		shouldIgnore := buildIgnoreCheckerForPath(workspace)

		// Get the tree from HEAD
		lastTree := make(map[string]plumbing.Hash) // relPath → blobHash
		headRef, headErr := repo.Head()
		if headErr == nil {
			commit, cErr := repo.CommitObject(headRef.Hash())
			if cErr == nil {
				tree, tErr := commit.Tree()
				if tErr == nil {
					tree.Files().ForEach(func(f *object.File) error {
						lastTree[f.Name] = f.Hash
						return nil
					})
				}
			}
		}

		// Walk current workspace
		currentFiles := make(map[string]bool)
		var results []interface{}

		filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(workspace, path)
			if relErr != nil || rel == "." {
				return nil
			}
			if shouldIgnore(rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}

			info, infoErr := d.Info()
			if infoErr != nil || info.Size() > 2*1024*1024 {
				return nil
			}

			relNorm := strings.ReplaceAll(rel, string(filepath.Separator), "/")
			currentFiles[relNorm] = true

			oldHash, existed := lastTree[relNorm]
			if !existed {
				obj := vm.NewObject()
				obj.Set("path", relNorm)
				obj.Set("status", "added")
				results = append(results, obj)
				return nil
			}

			// Check if content changed by hashing
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			if isBinaryContent(data) {
				return nil
			}

			blob := &plumbing.MemoryObject{}
			blob.SetType(plumbing.BlobObject)
			blob.SetSize(int64(len(data)))
			w, _ := blob.Writer()
			w.Write(data)
			w.Close()
			newHash := blob.Hash()

			if newHash != oldHash {
				obj := vm.NewObject()
				obj.Set("path", relNorm)
				obj.Set("status", "modified")
				results = append(results, obj)
			}
			return nil
		})

		// Check for deleted files
		for relPath := range lastTree {
			if !currentFiles[relPath] {
				obj := vm.NewObject()
				obj.Set("path", relPath)
				obj.Set("status", "deleted")
				results = append(results, obj)
			}
		}

		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// git.diff(path?, ref?) — unified diff
	g.Set("diff", func(call goja.FunctionCall) goja.Value {
		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.diff", err)
		}

		// Determine ref to diff against (default: HEAD)
		refStr := ""
		filePath := ""
		if len(call.Arguments) >= 1 {
			filePath = call.Arguments[0].String()
		}
		if len(call.Arguments) >= 2 {
			refStr = call.Arguments[1].String()
		}

		commitObj, err := resolveCommit(repo, refStr)
		if err != nil {
			logErr(vm, "git.diff", err)
		}

		tree, err := commitObj.Tree()
		if err != nil {
			logErr(vm, "git.diff", err)
		}

		if filePath != "" {
			// Diff single file
			result := diffSingleFile(workspace, tree, filePath)
			return vm.ToValue(result)
		}

		// Diff all files
		var diffs []string
		shouldIgnore := buildIgnoreCheckerForPath(workspace)

		// Check modified/added
		filepath.WalkDir(workspace, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				if d != nil && d.IsDir() && shouldIgnore(filepath.Base(path), true) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(workspace, path)
			if rel == "." {
				return nil
			}
			relNorm := strings.ReplaceAll(rel, string(filepath.Separator), "/")
			if shouldIgnore(relNorm, false) {
				return nil
			}

			info, _ := d.Info()
			if info == nil || info.Size() > 2*1024*1024 {
				return nil
			}

			d2 := diffSingleFile(workspace, tree, relNorm)
			if d2 != "" {
				diffs = append(diffs, d2)
			}
			return nil
		})

		return vm.ToValue(strings.Join(diffs, "\n"))
	})

	// git.show(path, ref?) — file content at a specific commit
	g.Set("show", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "git.show requires a path argument")
		}
		filePath := call.Arguments[0].String()
		refStr := ""
		if len(call.Arguments) >= 2 {
			refStr = call.Arguments[1].String()
		}

		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.show", err)
		}

		commitObj, err := resolveCommit(repo, refStr)
		if err != nil {
			logErr(vm, "git.show", err)
		}

		tree, err := commitObj.Tree()
		if err != nil {
			logErr(vm, "git.show", err)
		}

		f, err := tree.File(filePath)
		if err != nil {
			Throwf(vm, "git.show: file %q not found at revision", filePath)
		}

		content, err := f.Contents()
		if err != nil {
			logErr(vm, "git.show", err)
		}

		return vm.ToValue(content)
	})

	// git.restore(path, ref?) — restore file from history
	g.Set("restore", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "git.restore requires a path argument")
		}
		filePath := call.Arguments[0].String()
		refStr := ""
		if len(call.Arguments) >= 2 {
			refStr = call.Arguments[1].String()
		}

		// Jail check
		absPath, err := SanitizePath(workspace, filePath)
		if err != nil {
			logErr(vm, "git.restore", err)
		}

		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.restore", err)
		}

		commitObj, err := resolveCommit(repo, refStr)
		if err != nil {
			logErr(vm, "git.restore", err)
		}

		tree, err := commitObj.Tree()
		if err != nil {
			logErr(vm, "git.restore", err)
		}

		f, err := tree.File(filePath)
		if err != nil {
			Throwf(vm, "git.restore: file %q not found at revision", filePath)
		}

		content, err := f.Contents()
		if err != nil {
			logErr(vm, "git.restore", err)
		}

		// Create parent directories if needed
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "git.restore", err)
		}

		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			logErr(vm, "git.restore", err)
		}

		return vm.ToValue("restored")
	})

	// git.compact(n?) — keep only last N commits
	g.Set("compact", func(call goja.FunctionCall) goja.Value {
		n := 50
		if len(call.Arguments) >= 1 {
			n = int(call.Arguments[0].ToInteger())
			if n < 5 {
				n = 5
			}
		}

		repo, err := openOrInit()
		if err != nil {
			logErr(vm, "git.compact", err)
		}

		headRef, err := repo.Head()
		if err != nil {
			return vm.ToValue("no commits")
		}

		// Walk the commit chain to find the Nth commit
		iter, err := repo.Log(&git.LogOptions{From: headRef.Hash()})
		if err != nil {
			logErr(vm, "git.compact", err)
		}

		var commits []*object.Commit
		_ = iter.ForEach(func(c *object.Commit) error {
			commits = append(commits, c)
			return nil
		})

		if len(commits) <= n {
			return vm.ToValue(fmt.Sprintf("only %d commits, no compaction needed", len(commits)))
		}

		// The Nth commit becomes the new root (orphan).
		// We re-create commits 1..N with the Nth having no parent.
		newRootIdx := n - 1
		storer := repo.Storer

		// Create a new root commit using the tree of commits[newRootIdx]
		// but with no parent
		var prevHash plumbing.Hash
		for i := newRootIdx; i >= 0; i-- {
			c := commits[i]
			newCommit := &object.Commit{
				Author:    c.Author,
				Committer: c.Committer,
				Message:   c.Message,
				TreeHash:  c.TreeHash,
			}
			if i < newRootIdx {
				newCommit.ParentHashes = []plumbing.Hash{prevHash}
			}
			// Else: no parent (orphan root)

			commitObj := &plumbing.MemoryObject{}
			commitObj.SetType(plumbing.CommitObject)
			newCommit.Encode(commitObj)
			h, _ := storer.SetEncodedObject(commitObj)
			prevHash = h
		}

		// Update HEAD to the new tip
		refName := plumbing.ReferenceName("refs/heads/main")
		ref := plumbing.NewHashReference(refName, prevHash)
		storer.SetReference(ref)

		return vm.ToValue(fmt.Sprintf("compacted: %d → %d commits", len(commits), n))
	})

	// ── Workspace .git management ─────────────────────────────────────

	// git.init(path?, origin?) — initialize a new .git repo in the workspace
	g.Set("init", func(call goja.FunctionCall) goja.Value {
		targetDir := workspace
		originURL := ""

		if len(call.Arguments) >= 1 {
			p := call.Arguments[0].String()
			if p != "" {
				abs, err := SanitizePath(workspace, p)
				if err != nil {
					logErr(vm, "git.init", err)
				}
				targetDir = abs
			}
		}
		if len(call.Arguments) >= 2 {
			originURL = call.Arguments[1].String()
		}

		if err := os.MkdirAll(targetDir, 0755); err != nil {
			logErr(vm, "git.init", err)
		}

		repo, err := git.PlainInit(targetDir, false)
		if err != nil {
			logErr(vm, "git.init", err)
		}

		if originURL != "" {
			_, err := repo.CreateRemote(&gitconfig.RemoteConfig{
				Name: "origin",
				URLs: []string{originURL},
			})
			if err != nil {
				logErr(vm, "git.init", err)
			}
		}

		return buildRepoHandle(vm, repo, targetDir, store)
	})

	// git.clone(url, pathOrOpts?) — clone a remote repo into the workspace
	g.Set("clone", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "git.clone requires a URL argument")
		}
		cloneURL := call.Arguments[0].String()

		// Derive default target path from URL basename
		urlBase := filepath.Base(cloneURL)
		urlBase = strings.TrimSuffix(urlBase, ".git")
		targetPath := urlBase
		var cloneBranch string
		var cloneDepth int
		var authObj *goja.Object

		if len(call.Arguments) >= 2 {
			arg := call.Arguments[1]
			// Check if it's a string shorthand or options object
			if arg.ExportType() != nil && arg.ExportType().Kind().String() == "string" {
				targetPath = arg.String()
			} else {
				optObj := arg.ToObject(vm)
				if optObj != nil {
					if p := optObj.Get("path"); p != nil && !goja.IsUndefined(p) {
						targetPath = p.String()
					}
					if b := optObj.Get("branch"); b != nil && !goja.IsUndefined(b) {
						cloneBranch = b.String()
					}
					if d := optObj.Get("depth"); d != nil && !goja.IsUndefined(d) {
						cloneDepth = int(d.ToInteger())
					}
					if a := optObj.Get("auth"); a != nil && !goja.IsUndefined(a) {
						authObj = a.ToObject(vm)
					}
				}
			}
		}

		// Jail target to workspace
		absTarget, err := SanitizePath(workspace, targetPath)
		if err != nil {
			logErr(vm, "git.clone", err)
		}

		cloneOpts := &git.CloneOptions{
			URL: cloneURL,
		}
		if cloneBranch != "" {
			cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(cloneBranch)
			cloneOpts.SingleBranch = true
		}
		if cloneDepth > 0 {
			cloneOpts.Depth = cloneDepth
		}

		// Resolve auth
		auth := resolveGitAuth(store, cloneURL, authObj, vm)
		if auth != nil {
			cloneOpts.Auth = auth
		}

		repo, err := git.PlainClone(absTarget, false, cloneOpts)
		if err != nil {
			logErr(vm, "git.clone", err)
		}

		return buildRepoHandle(vm, repo, absTarget, store)
	})

	// git.repo(path?) — open an existing .git repo in the workspace
	g.Set("repo", func(call goja.FunctionCall) goja.Value {
		targetDir := workspace
		if len(call.Arguments) >= 1 {
			p := call.Arguments[0].String()
			if p != "" {
				abs, err := SanitizePath(workspace, p)
				if err != nil {
					logErr(vm, "git.repo", err)
				}
				targetDir = abs
			}
		}

		repo, err := git.PlainOpen(targetDir)
		if err != nil {
			Throwf(vm, "git.repo: no .git repository found at %q", targetDir)
		}

		return buildRepoHandle(vm, repo, targetDir, store)
	})

	vm.Set(NameGit, g)
}

// AutoCommit is called from the agent loop to snapshot workspace after a turn.
// It's a no-op if nothing changed. Returns silently on any error.
func AutoCommit(workspace, configDir string, wsID string, turnID, providerName string) {
	repoDir := filepath.Join(configDir, wsID, "git")

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return
	}

	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		repo, err = git.PlainInitWithOptions(repoDir, &git.PlainInitOptions{
			Bare: true,
		})
		if err != nil {
			return
		}
	}

	msg := fmt.Sprintf("turn %s [%s]", turnID, providerName)
	_, _, _ = snapshotAndCommit(repo, workspace, msg)
}

// buildIgnoreCheckerForPath builds a gitignore checker for the given workspace path.
func buildIgnoreCheckerForPath(workspace string) func(rel string, isDir bool) bool {
	var gi *ignore.GitIgnore
	if parsed, err := ignore.CompileIgnoreFile(filepath.Join(workspace, ".gitignore")); err == nil {
		gi = parsed
	}
	return func(rel string, isDir bool) bool {
		name := filepath.Base(rel)
		relSlash := strings.ReplaceAll(rel, string(filepath.Separator), "/")
		// Always skip .git directories
		if name == ".git" {
			return true
		}
		// Skip .agent/tmp (temp data, not tracked) but allow .agent itself
		if relSlash == ".agent/tmp" || strings.HasPrefix(relSlash, ".agent/tmp/") {
			return true
		}
		// Skip hidden files/dirs (but allow .agent through)
		if strings.HasPrefix(name, ".") && name != "." && name != ".agent" {
			return true
		}
		if isDir && (name == "node_modules" || name == "build" || name == "dist" || name == "__pycache__") {
			return true
		}
		if gi != nil && gi.MatchesPath(rel) {
			return true
		}
		return false
	}
}

// resolveCommit resolves a ref string to a commit object.
// Empty refStr defaults to HEAD.
func resolveCommit(repo *git.Repository, refStr string) (*object.Commit, error) {
	if refStr == "" {
		headRef, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("no commits yet")
		}
		return repo.CommitObject(headRef.Hash())
	}

	// Try as branch name first
	ref, err := repo.Reference(plumbing.ReferenceName("refs/heads/"+refStr), true)
	if err == nil {
		return repo.CommitObject(ref.Hash())
	}

	// Try as partial hash
	h := plumbing.NewHash(refStr)
	c, err := repo.CommitObject(h)
	if err == nil {
		return c, nil
	}

	// Try iterating commits for partial match
	headRef, headErr := repo.Head()
	if headErr != nil {
		return nil, fmt.Errorf("ref %q not found", refStr)
	}
	iter, _ := repo.Log(&git.LogOptions{From: headRef.Hash()})
	var found *object.Commit
	iter.ForEach(func(c *object.Commit) error {
		if strings.HasPrefix(c.Hash.String(), refStr) {
			found = c
			return fmt.Errorf("found")
		}
		return nil
	})
	if found != nil {
		return found, nil
	}

	return nil, fmt.Errorf("ref %q not found", refStr)
}

// buildNestedTree builds a proper nested git tree from flat file entries.
// Input entries have forward-slash-separated paths like "src/app.js".
func buildNestedTree(storer interface {
	SetEncodedObject(plumbing.EncodedObject) (plumbing.Hash, error)
}, entries []object.TreeEntry) (plumbing.Hash, error) {
	// Group entries by top-level directory
	type dirGroup struct {
		entries []object.TreeEntry
	}
	dirs := make(map[string]*dirGroup)
	var rootEntries []object.TreeEntry

	for _, e := range entries {
		parts := strings.SplitN(e.Name, "/", 2)
		if len(parts) == 1 {
			// Root-level file
			rootEntries = append(rootEntries, e)
		} else {
			dirName := parts[0]
			if dirs[dirName] == nil {
				dirs[dirName] = &dirGroup{}
			}
			dirs[dirName].entries = append(dirs[dirName].entries, object.TreeEntry{
				Name: parts[1],
				Mode: e.Mode,
				Hash: e.Hash,
			})
		}
	}

	// Recursively build subtrees
	for dirName, dg := range dirs {
		subHash, err := buildNestedTree(storer, dg.entries)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		rootEntries = append(rootEntries, object.TreeEntry{
			Name: dirName,
			Mode: filemode.Dir,
			Hash: subHash,
		})
	}

	// Git requires tree entries to be sorted lexicographically.
	// Directories sort as if their name has a trailing "/".
	sort.Slice(rootEntries, func(i, j int) bool {
		ni, nj := rootEntries[i].Name, rootEntries[j].Name
		if rootEntries[i].Mode == filemode.Dir {
			ni += "/"
		}
		if rootEntries[j].Mode == filemode.Dir {
			nj += "/"
		}
		return ni < nj
	})

	// Create tree object
	tree := &object.Tree{Entries: rootEntries}
	treeObj := &plumbing.MemoryObject{}
	treeObj.SetType(plumbing.TreeObject)
	if err := tree.Encode(treeObj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encode tree: %w", err)
	}
	return storer.SetEncodedObject(treeObj)
}

// diffTrees compares two trees and returns a list of changed files as "M path", "A path", "D path".
func diffTrees(repo *git.Repository, oldTreeHash, newTreeHash plumbing.Hash, prefix string) []string {
	var changes []string

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

	// Check for modified/added
	for name, newEntry := range newMap {
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "/" + name
		}

		oldEntry, existed := oldMap[name]
		if !existed {
			if newEntry.Mode == filemode.Dir {
				// Recurse: all files in new subtree are added
				changes = append(changes, diffTrees(repo, plumbing.ZeroHash, newEntry.Hash, fullPath)...)
			} else {
				changes = append(changes, "A "+fullPath)
			}
		} else if oldEntry.Hash != newEntry.Hash {
			if newEntry.Mode == filemode.Dir && oldEntry.Mode == filemode.Dir {
				changes = append(changes, diffTrees(repo, oldEntry.Hash, newEntry.Hash, fullPath)...)
			} else {
				changes = append(changes, "M "+fullPath)
			}
		}
	}

	// Check for deleted
	for name, oldEntry := range oldMap {
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "/" + name
		}
		if _, exists := newMap[name]; !exists {
			if oldEntry.Mode == filemode.Dir {
				changes = append(changes, diffTrees(repo, oldEntry.Hash, plumbing.ZeroHash, fullPath)...)
			} else {
				changes = append(changes, "D "+fullPath)
			}
		}
	}

	return changes
}

// diffSingleFile produces a unified diff of a single file between the tree and workspace.
func diffSingleFile(workspace string, tree *object.Tree, relPath string) string {
	// Get old content from tree
	var oldContent string
	f, err := tree.File(relPath)
	if err == nil {
		oldContent, _ = f.Contents()
	}

	// Get new content from workspace
	absPath := filepath.Join(workspace, filepath.FromSlash(relPath))
	data, readErr := os.ReadFile(absPath)
	var newContent string
	if readErr == nil {
		newContent = string(data)
	}

	if oldContent == newContent {
		return ""
	}

	if oldContent == "" && newContent != "" {
		return fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n%s",
			relPath, len(strings.Split(newContent, "\n")),
			prefixLines(newContent, "+"))
	}
	if oldContent != "" && newContent == "" {
		return fmt.Sprintf("--- a/%s\n+++ /dev/null\n@@ -1,%d +0,0 @@\n%s",
			relPath, len(strings.Split(oldContent, "\n")),
			prefixLines(oldContent, "-"))
	}

	// Simple line-level diff
	return simpleDiff(relPath, oldContent, newContent)
}

// prefixLines adds a prefix to each line of text.
func prefixLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	var result []string
	for _, l := range lines {
		result = append(result, prefix+l)
	}
	return strings.Join(result, "\n")
}

// simpleDiff produces a basic unified diff for two strings.
func simpleDiff(path, old, new string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))

	// Simple context diff: show removed and added lines
	// For brevity use a basic approach rather than full Myers diff
	oldSet := make(map[string]int)
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int)
	for _, l := range newLines {
		newSet[l]++
	}

	diff.WriteString(fmt.Sprintf("@@ -%d +%d @@\n", len(oldLines), len(newLines)))

	// Show removed lines
	for _, l := range oldLines {
		if newSet[l] > 0 {
			newSet[l]--
			diff.WriteString(" " + l + "\n")
		} else {
			diff.WriteString("-" + l + "\n")
		}
	}
	// Show added lines that weren't in old
	newSet2 := make(map[string]int)
	for _, l := range newLines {
		newSet2[l]++
	}
	for _, l := range oldLines {
		if newSet2[l] > 0 {
			newSet2[l]--
		}
	}
	for _, l := range newLines {
		if newSet2[l] > 0 {
			newSet2[l]--
			diff.WriteString("+" + l + "\n")
		}
	}

	return diff.String()
}
