package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

// resolveGitAuth resolves authentication for git remote operations.
// Priority: 1) explicit opts.auth  2) auto-detect from secrets by URL host  3) nil
func resolveGitAuth(store *config.Store, remoteURL string, authObj *goja.Object, vm *goja.Runtime) transport.AuthMethod {
	// 1. Explicit auth from options
	if authObj != nil {
		// SSH key: {auth: {key: "PEM contents or {{secrets.SSH_KEY}}"}}
		if k := authObj.Get("key"); k != nil && !goja.IsUndefined(k) {
			keyStr := k.String()
			if store != nil {
				keyStr = ExpandSecrets(context.Background(), store, keyStr)
			}
			// Determine SSH user from URL (default "git")
			sshUser := "git"
			if u := authObj.Get("user"); u != nil && !goja.IsUndefined(u) {
				sshUser = u.String()
			}
			slog.Debug("git ssh auth", "user", sshUser, "key_len", len(keyStr), "key_prefix", safePrefix(keyStr, 40))
			auth, err := gitssh.NewPublicKeys(sshUser, []byte(keyStr), "")
			if err != nil {
				Throwf(vm, "git: SSH key parse failed: %v (key length: %d, starts with: %q)", err, len(keyStr), safePrefix(keyStr, 30))
			}
			sshSetHostKeyCallback(auth)
			return auth
		}
		// HTTP token: {auth: {token: "..."}}
		if tok := authObj.Get("token"); tok != nil && !goja.IsUndefined(tok) {
			tokenStr := tok.String()
			if store != nil {
				tokenStr = ExpandSecrets(context.Background(), store, tokenStr)
			}
			return &http.BasicAuth{
				Username: "x-token-auth",
				Password: tokenStr,
			}
		}
		// HTTP basic: {auth: {user: "...", pass: "..."}}
		if user := authObj.Get("user"); user != nil && !goja.IsUndefined(user) {
			userStr := user.String()
			passStr := ""
			if pass := authObj.Get("pass"); pass != nil && !goja.IsUndefined(pass) {
				passStr = pass.String()
			}
			if store != nil {
				userStr = ExpandSecrets(context.Background(), store, userStr)
				passStr = ExpandSecrets(context.Background(), store, passStr)
			}
			return &http.BasicAuth{
				Username: userStr,
				Password: passStr,
			}
		}
	}

	// 2. Auto-detect from well-known secrets
	if store == nil {
		return nil
	}
	ctx := context.Background()
	ws := store.Workspace()
	wsID := ""
	if ws != nil {
		wsID = ws.ID
	}

	// Helper to try workspace then user-level secret
	trySecret := func(name string) string {
		if wsID != "" {
			sec, err := store.GetSecret(ctx, wsID, name)
			if err == nil && sec != nil {
				return sec.Value
			}
		}
		sec, err := store.GetSecret(ctx, "", name)
		if err == nil && sec != nil {
			return sec.Value
		}
		return ""
	}

	urlLower := strings.ToLower(remoteURL)

	// SSH key for git@ URLs
	if strings.HasPrefix(urlLower, "git@") || strings.Contains(urlLower, "ssh://") {
		if keyPEM := trySecret("SSH_KEY"); keyPEM != "" {
			auth, err := gitssh.NewPublicKeys("git", []byte(keyPEM), "")
			if err != nil {
				slog.Warn("git: SSH_KEY secret found but failed to parse — check key format",
					"error", err, "key_len", len(keyPEM), "key_prefix", safePrefix(keyPEM, 40))
			} else {
				sshSetHostKeyCallback(auth)
				return auth
			}
		}
	}

	// Match by host
	if strings.Contains(urlLower, "github.com") {
		if token := trySecret("GITHUB_TOKEN"); token != "" {
			return &http.BasicAuth{Username: "x-token-auth", Password: token}
		}
	}
	if strings.Contains(urlLower, "gitlab.com") {
		if token := trySecret("GITLAB_TOKEN"); token != "" {
			return &http.BasicAuth{Username: "oauth2", Password: token}
		}
	}

	// Generic fallback for HTTPS
	if strings.HasPrefix(urlLower, "https://") || strings.HasPrefix(urlLower, "http://") {
		if token := trySecret("GIT_TOKEN"); token != "" {
			return &http.BasicAuth{Username: "x-token-auth", Password: token}
		}
	}

	return nil
}

// sshSetHostKeyCallback configures the host key policy on SSH auth.
// In an agent runtime the user has explicitly provided their SSH key and
// intends to connect, so we skip known_hosts verification entirely.
// This avoids "key mismatch" errors caused by stale or missing entries
// in ~/.ssh/known_hosts (e.g. after GitHub host-key rotations).
func sshSetHostKeyCallback(auth *gitssh.PublicKeys) {
	auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
}

// safePrefix returns the first n characters of s, or the full string if shorter.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// getRemoteURL extracts the first URL of the given remote (default "origin").
func getRemoteURL(repo *git.Repository, remoteName string) string {
	if remoteName == "" {
		remoteName = "origin"
	}
	remote, err := repo.Remote(remoteName)
	if err != nil {
		return ""
	}
	cfg := remote.Config()
	if len(cfg.URLs) > 0 {
		return cfg.URLs[0]
	}
	return ""
}

// getAuthorSignature reads user name/email from repo config, falling back to defaults.
func getAuthorSignature(repo *git.Repository) object.Signature {
	sig := object.Signature{
		Name:  "Altclaw Agent",
		Email: "agent@altclaw.ai",
		When:  time.Now(),
	}
	cfg, err := repo.Config()
	if err == nil && cfg.User.Name != "" {
		sig.Name = cfg.User.Name
		if cfg.User.Email != "" {
			sig.Email = cfg.User.Email
		}
	}
	return sig
}

// buildRepoHandle creates a Goja object with methods for managing a real .git repository.
// repoRoot is the directory containing the .git folder (already jailed to workspace).
func buildRepoHandle(vm *goja.Runtime, repo *git.Repository, repoRoot string, store *config.Store) goja.Value {
	handle := vm.NewObject()

	wt, err := repo.Worktree()
	if err != nil {
		Throwf(vm, "git.repo: cannot get worktree: %v", err)
	}

	// --- repo.status() ---
	handle.Set("status", func(call goja.FunctionCall) goja.Value {
		st, err := wt.Status()
		if err != nil {
			logErr(vm, "repo.status", err)
		}

		var results []interface{}
		for path, fs := range st {
			entry := vm.NewObject()
			entry.Set("path", path)

			// Map status codes to human-readable strings
			statusStr := "unmodified"
			stagingStr := "unstaged"

			// Worktree status
			switch fs.Worktree {
			case git.Untracked:
				statusStr = "untracked"
			case git.Modified:
				statusStr = "modified"
			case git.Deleted:
				statusStr = "deleted"
			case git.Renamed:
				statusStr = "renamed"
			case git.Copied:
				statusStr = "copied"
			}

			// Staging status
			switch fs.Staging {
			case git.Added:
				stagingStr = "staged"
				if statusStr == "unmodified" {
					statusStr = "added"
				}
			case git.Modified:
				stagingStr = "staged"
				if statusStr == "modified" {
					stagingStr = "both"
				} else if statusStr == "unmodified" {
					statusStr = "modified"
				}
			case git.Deleted:
				stagingStr = "staged"
				if statusStr == "unmodified" {
					statusStr = "deleted"
				}
			case git.Renamed:
				stagingStr = "staged"
				if statusStr == "unmodified" {
					statusStr = "renamed"
				}
			}

			// Untracked is always unstaged
			if fs.Worktree == git.Untracked && fs.Staging == git.Untracked {
				statusStr = "untracked"
				stagingStr = "unstaged"
			}

			entry.Set("status", statusStr)
			entry.Set("staging", stagingStr)
			results = append(results, entry)
		}

		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// --- repo.add(path) ---
	handle.Set("add", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.add requires a path argument")
		}
		p := call.Arguments[0].String()

		if p == "." {
			// Stage all: use worktree.AddWithOptions for glob
			err := wt.AddWithOptions(&git.AddOptions{All: true})
			if err != nil {
				logErr(vm, "repo.add", err)
			}
			return vm.ToValue("staged all")
		}

		_, err := wt.Add(p)
		if err != nil {
			logErr(vm, "repo.add", err)
		}
		return vm.ToValue("staged")
	})

	// --- repo.reset(path?) ---
	handle.Set("reset", func(call goja.FunctionCall) goja.Value {
		headRef, err := repo.Head()
		if err != nil {
			logErr(vm, "repo.reset", err)
		}

		if len(call.Arguments) >= 1 {
			// Reset a specific file
			p := call.Arguments[0].String()
			err = wt.Reset(&git.ResetOptions{
				Commit: headRef.Hash(),
				Mode:   git.SoftReset,
				Files:  []string{p},
			})
		} else {
			err = wt.Reset(&git.ResetOptions{
				Commit: headRef.Hash(),
				Mode:   git.SoftReset,
			})
		}
		if err != nil {
			logErr(vm, "repo.reset", err)
		}
		return vm.ToValue("reset")
	})

	// --- repo.commit(msg) ---
	handle.Set("commit", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.commit requires a message argument")
		}
		msg := call.Arguments[0].String()

		author := getAuthorSignature(repo)
		hash, err := wt.Commit(msg, &git.CommitOptions{
			Author:    &author,
			Committer: &author,
		})
		if err != nil {
			logErr(vm, "repo.commit", err)
		}

		// Determine current branch name
		branchName := ""
		headRef, headErr := repo.Head()
		if headErr == nil && headRef.Name().IsBranch() {
			branchName = headRef.Name().Short()
		}

		obj := vm.NewObject()
		obj.Set("hash", hash.String()[:7])
		obj.Set("branch", branchName)
		return obj
	})

	// --- repo.log(n?) ---
	handle.Set("log", func(call goja.FunctionCall) goja.Value {
		n := 10
		if len(call.Arguments) >= 1 {
			n = int(call.Arguments[0].ToInteger())
			if n < 1 {
				n = 1
			}
		}

		headRef, err := repo.Head()
		if err != nil {
			return vm.ToValue([]interface{}{})
		}

		iter, err := repo.Log(&git.LogOptions{From: headRef.Hash()})
		if err != nil {
			logErr(vm, "repo.log", err)
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
			entry.Set("author", c.Author.Name)
			entry.Set("date", c.Author.When.Format(time.RFC3339))
			results = append(results, entry)
			return nil
		})

		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// --- repo.diff(path?) ---
	handle.Set("diff", func(call goja.FunctionCall) goja.Value {
		// Get HEAD tree
		headRef, err := repo.Head()
		if err != nil {
			return vm.ToValue("") // No commits yet
		}
		commitObj, err := repo.CommitObject(headRef.Hash())
		if err != nil {
			logErr(vm, "repo.diff", err)
		}
		tree, err := commitObj.Tree()
		if err != nil {
			logErr(vm, "repo.diff", err)
		}

		if len(call.Arguments) >= 1 {
			p := call.Arguments[0].String()
			return vm.ToValue(diffSingleFile(repoRoot, tree, p))
		}

		// Diff all files
		var diffs []string
		shouldIgnore := buildIgnoreCheckerForPath(repoRoot)

		filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				if d != nil && d.IsDir() && shouldIgnore(filepath.Base(path), true) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, path)
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
			d2 := diffSingleFile(repoRoot, tree, relNorm)
			if d2 != "" {
				diffs = append(diffs, d2)
			}
			return nil
		})

		return vm.ToValue(strings.Join(diffs, "\n"))
	})

	// --- repo.diffStaged(path?) ---
	handle.Set("diffStaged", func(call goja.FunctionCall) goja.Value {
		// Compare index (staging area) against HEAD
		headRef, err := repo.Head()
		if err != nil {
			return vm.ToValue("")
		}
		commitObj, err := repo.CommitObject(headRef.Hash())
		if err != nil {
			logErr(vm, "repo.diffStaged", err)
		}
		headTree, err := commitObj.Tree()
		if err != nil {
			logErr(vm, "repo.diffStaged", err)
		}

		st, err := wt.Status()
		if err != nil {
			logErr(vm, "repo.diffStaged", err)
		}

		var diffs []string
		for path, fs := range st {
			if fs.Staging == git.Unmodified || fs.Staging == git.Untracked {
				continue
			}
			if len(call.Arguments) >= 1 && call.Arguments[0].String() != path {
				continue
			}

			// Get old content from HEAD
			var oldContent string
			f, fErr := headTree.File(path)
			if fErr == nil {
				oldContent, _ = f.Contents()
			}

			// Get new content from working tree (current staging content = file on disk after add)
			absPath := filepath.Join(repoRoot, filepath.FromSlash(path))
			data, readErr := os.ReadFile(absPath)
			var newContent string
			if readErr == nil {
				newContent = string(data)
			}

			if fs.Staging == git.Deleted {
				newContent = ""
			}

			if oldContent != newContent {
				if oldContent == "" && newContent != "" {
					d := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n%s",
						path, len(strings.Split(newContent, "\n")),
						prefixLines(newContent, "+"))
					diffs = append(diffs, d)
				} else if oldContent != "" && newContent == "" {
					d := fmt.Sprintf("--- a/%s\n+++ /dev/null\n@@ -1,%d +0,0 @@\n%s",
						path, len(strings.Split(oldContent, "\n")),
						prefixLines(oldContent, "-"))
					diffs = append(diffs, d)
				} else {
					diffs = append(diffs, simpleDiff(path, oldContent, newContent))
				}
			}
		}

		return vm.ToValue(strings.Join(diffs, "\n"))
	})

	// --- repo.branch(name?) ---
	handle.Set("branch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 1 {
			// Create a new branch
			name := call.Arguments[0].String()
			headRef, err := repo.Head()
			if err != nil {
				logErr(vm, "repo.branch", err)
			}

			refName := plumbing.NewBranchReferenceName(name)
			ref := plumbing.NewHashReference(refName, headRef.Hash())
			if err := repo.Storer.SetReference(ref); err != nil {
				logErr(vm, "repo.branch", err)
			}

			// Also create the branch config entry
			err = repo.CreateBranch(&gitconfig.Branch{
				Name:   name,
				Remote: "origin",
				Merge:  refName,
			})
			if err != nil {
				// Not critical — branch ref was created, config entry is optional
			}

			return vm.ToValue("created")
		}

		// List branches
		current := ""
		headRef, headErr := repo.Head()
		if headErr == nil && headRef.Name().IsBranch() {
			current = headRef.Name().Short()
		}

		var list []string
		branches, err := repo.Branches()
		if err != nil {
			logErr(vm, "repo.branch", err)
		}
		_ = branches.ForEach(func(ref *plumbing.Reference) error {
			list = append(list, ref.Name().Short())
			return nil
		})

		obj := vm.NewObject()
		obj.Set("current", current)
		obj.Set("list", list)
		return obj
	})

	// --- repo.deleteBranch(name) ---
	handle.Set("deleteBranch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.deleteBranch requires a branch name")
		}
		name := call.Arguments[0].String()

		// Check not on this branch
		headRef, headErr := repo.Head()
		if headErr == nil && headRef.Name().IsBranch() && headRef.Name().Short() == name {
			Throwf(vm, "repo.deleteBranch: cannot delete current branch %q", name)
		}

		// Remove the branch config
		_ = repo.DeleteBranch(name)

		// Remove the reference
		refName := plumbing.NewBranchReferenceName(name)
		if err := repo.Storer.RemoveReference(refName); err != nil {
			logErr(vm, "repo.deleteBranch", err)
		}

		return vm.ToValue("deleted")
	})

	// --- repo.checkout(ref) ---
	handle.Set("checkout", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.checkout requires a branch or commit ref")
		}
		ref := call.Arguments[0].String()

		force := false
		if len(call.Arguments) >= 2 {
			optObj := call.Arguments[1].ToObject(vm)
			if optObj != nil {
				if f := optObj.Get("force"); f != nil && !goja.IsUndefined(f) {
					force = f.ToBoolean()
				}
			}
		}

		opts := &git.CheckoutOptions{
			Force: force,
		}

		// Try as branch first
		branchRef := plumbing.NewBranchReferenceName(ref)
		if _, err := repo.Reference(branchRef, true); err == nil {
			opts.Branch = branchRef
		} else {
			// Try as commit hash
			h := plumbing.NewHash(ref)
			if _, err := repo.CommitObject(h); err == nil {
				opts.Hash = h
			} else {
				// Try partial hash match
				headRef, headErr := repo.Head()
				if headErr != nil {
					Throwf(vm, "repo.checkout: ref %q not found", ref)
				}
				iter, _ := repo.Log(&git.LogOptions{From: headRef.Hash()})
				var found *object.Commit
				if iter != nil {
					iter.ForEach(func(c *object.Commit) error {
						if strings.HasPrefix(c.Hash.String(), ref) {
							found = c
							return fmt.Errorf("found")
						}
						return nil
					})
				}
				if found == nil {
					Throwf(vm, "repo.checkout: ref %q not found", ref)
				}
				opts.Hash = found.Hash
			}
		}

		if err := wt.Checkout(opts); err != nil {
			logErr(vm, "repo.checkout", err)
		}

		return vm.ToValue("checked out")
	})

	// --- repo.remote() ---
	handle.Set("remote", func(call goja.FunctionCall) goja.Value {
		remotes, err := repo.Remotes()
		if err != nil {
			logErr(vm, "repo.remote", err)
		}

		var results []interface{}
		for _, r := range remotes {
			entry := vm.NewObject()
			entry.Set("name", r.Config().Name)
			entry.Set("urls", r.Config().URLs)
			results = append(results, entry)
		}
		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// --- repo.addRemote(name, url) ---
	handle.Set("addRemote", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "repo.addRemote requires name and url arguments")
		}
		name := call.Arguments[0].String()
		url := call.Arguments[1].String()

		_, err := repo.CreateRemote(&gitconfig.RemoteConfig{
			Name: name,
			URLs: []string{url},
		})
		if err != nil {
			logErr(vm, "repo.addRemote", err)
		}
		return vm.ToValue("added")
	})

	// --- repo.removeRemote(name) ---
	handle.Set("removeRemote", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.removeRemote requires a remote name")
		}
		name := call.Arguments[0].String()

		if err := repo.DeleteRemote(name); err != nil {
			logErr(vm, "repo.removeRemote", err)
		}
		return vm.ToValue("removed")
	})

	// --- repo.pull(opts?) ---
	handle.Set("pull", func(call goja.FunctionCall) goja.Value {
		remoteName := "origin"
		var authObj *goja.Object

		if len(call.Arguments) >= 1 {
			optObj := call.Arguments[0].ToObject(vm)
			if optObj != nil {
				if r := optObj.Get("remote"); r != nil && !goja.IsUndefined(r) {
					remoteName = r.String()
				}
				if a := optObj.Get("auth"); a != nil && !goja.IsUndefined(a) {
					authObj = a.ToObject(vm)
				}
			}
		}

		remoteURL := getRemoteURL(repo, remoteName)
		auth := resolveGitAuth(store, remoteURL, authObj, vm)

		pullOpts := &git.PullOptions{
			RemoteName: remoteName,
		}
		if auth != nil {
			pullOpts.Auth = auth
		}

		err := wt.Pull(pullOpts)
		if err != nil {
			if err == git.NoErrAlreadyUpToDate {
				return vm.ToValue("already up to date")
			}
			logErr(vm, "repo.pull", err)
		}

		return vm.ToValue("pulled")
	})

	// --- repo.push(opts?) ---
	handle.Set("push", func(call goja.FunctionCall) goja.Value {
		remoteName := "origin"
		var authObj *goja.Object

		if len(call.Arguments) >= 1 {
			optObj := call.Arguments[0].ToObject(vm)
			if optObj != nil {
				if r := optObj.Get("remote"); r != nil && !goja.IsUndefined(r) {
					remoteName = r.String()
				}
				if a := optObj.Get("auth"); a != nil && !goja.IsUndefined(a) {
					authObj = a.ToObject(vm)
				}
			}
		}

		remoteURL := getRemoteURL(repo, remoteName)
		auth := resolveGitAuth(store, remoteURL, authObj, vm)

		pushOpts := &git.PushOptions{
			RemoteName: remoteName,
		}
		if auth != nil {
			pushOpts.Auth = auth
		}

		err := repo.Push(pushOpts)
		if err != nil {
			if err == git.NoErrAlreadyUpToDate {
				return vm.ToValue("already up to date")
			}
			logErr(vm, "repo.push", err)
		}

		return vm.ToValue("pushed")
	})

	// --- repo.tag(name, opts?) ---
	handle.Set("tag", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.tag requires a tag name")
		}
		name := call.Arguments[0].String()

		// Determine target hash
		var targetHash plumbing.Hash
		if len(call.Arguments) >= 2 {
			optObj := call.Arguments[1].ToObject(vm)
			if optObj != nil {
				if r := optObj.Get("ref"); r != nil && !goja.IsUndefined(r) {
					refStr := r.String()
					commit, err := resolveCommit(repo, refStr)
					if err != nil {
						logErr(vm, "repo.tag", err)
					}
					targetHash = commit.Hash
				}
			}
		}

		if targetHash.IsZero() {
			headRef, err := repo.Head()
			if err != nil {
				logErr(vm, "repo.tag", err)
			}
			targetHash = headRef.Hash()
		}

		// Create lightweight tag
		_, err := repo.CreateTag(name, targetHash, nil)
		if err != nil {
			logErr(vm, "repo.tag", err)
		}

		return vm.ToValue("tagged")
	})

	// --- repo.tags() ---
	handle.Set("tags", func(call goja.FunctionCall) goja.Value {
		tagsIter, err := repo.Tags()
		if err != nil {
			logErr(vm, "repo.tags", err)
		}

		var tags []string
		_ = tagsIter.ForEach(func(ref *plumbing.Reference) error {
			tags = append(tags, ref.Name().Short())
			return nil
		})
		if tags == nil {
			tags = []string{}
		}
		return vm.ToValue(tags)
	})

	// --- repo.deleteTag(name) ---
	handle.Set("deleteTag", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "repo.deleteTag requires a tag name")
		}
		name := call.Arguments[0].String()

		if err := repo.DeleteTag(name); err != nil {
			logErr(vm, "repo.deleteTag", err)
		}
		return vm.ToValue("deleted")
	})

	// --- repo.stash() — single-slot stash via hidden ref ---
	handle.Set("stash", func(call goja.FunctionCall) goja.Value {
		// Check there are actually changes to stash
		st, err := wt.Status()
		if err != nil {
			logErr(vm, "repo.stash", err)
		}
		if st.IsClean() {
			return vm.ToValue("nothing to stash")
		}

		// Check if stash already exists
		stashRefName := plumbing.ReferenceName("refs/stash")
		if _, err := repo.Reference(stashRefName, true); err == nil {
			Throw(vm, "repo.stash: stash already has saved changes — use stashPop() first")
		}

		// Stage everything
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			logErr(vm, "repo.stash", err)
		}

		// Create stash commit
		headRef, err := repo.Head()
		if err != nil {
			logErr(vm, "repo.stash", err)
		}

		author := getAuthorSignature(repo)
		stashHash, err := wt.Commit("stash", &git.CommitOptions{
			Author:    &author,
			Committer: &author,
		})
		if err != nil {
			logErr(vm, "repo.stash", err)
		}

		// Save stash ref pointing to this commit
		ref := plumbing.NewHashReference(stashRefName, stashHash)
		if err := repo.Storer.SetReference(ref); err != nil {
			logErr(vm, "repo.stash", err)
		}

		// Reset HEAD back to the parent commit to undo the stash commit from the branch
		if err := wt.Reset(&git.ResetOptions{
			Commit: headRef.Hash(),
			Mode:   git.HardReset,
		}); err != nil {
			logErr(vm, "repo.stash", err)
		}

		return vm.ToValue("stashed")
	})

	// --- repo.stashPop() ---
	handle.Set("stashPop", func(call goja.FunctionCall) goja.Value {
		stashRefName := plumbing.ReferenceName("refs/stash")
		stashRef, err := repo.Reference(stashRefName, true)
		if err != nil {
			Throw(vm, "repo.stashPop: no stash found")
		}

		stashCommit, err := repo.CommitObject(stashRef.Hash())
		if err != nil {
			logErr(vm, "repo.stashPop", err)
		}

		// Get the stash tree and apply its changes by checking out to the stash commit files
		stashTree, err := stashCommit.Tree()
		if err != nil {
			logErr(vm, "repo.stashPop", err)
		}

		// Write stashed files to working tree
		_ = stashTree.Files().ForEach(func(f *object.File) error {
			content, cErr := f.Contents()
			if cErr != nil {
				return nil
			}
			absPath := filepath.Join(repoRoot, filepath.FromSlash(f.Name))
			dir := filepath.Dir(absPath)
			os.MkdirAll(dir, 0755)
			os.WriteFile(absPath, []byte(content), 0644)
			return nil
		})

		// Check for files deleted in stash (present in parent but not in stash tree)
		if stashCommit.NumParents() > 0 {
			parent, pErr := stashCommit.Parent(0)
			if pErr == nil {
				parentTree, ptErr := parent.Tree()
				if ptErr == nil {
					_ = parentTree.Files().ForEach(func(f *object.File) error {
						if _, fErr := stashTree.File(f.Name); fErr != nil {
							// File was in parent but not in stash — was deleted
							absPath := filepath.Join(repoRoot, filepath.FromSlash(f.Name))
							os.Remove(absPath)
						}
						return nil
					})
				}
			}
		}

		// Remove the stash ref
		if err := repo.Storer.RemoveReference(stashRefName); err != nil {
			logErr(vm, "repo.stashPop", err)
		}

		return vm.ToValue("popped")
	})

	return handle
}
