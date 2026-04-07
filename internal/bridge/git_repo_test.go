package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// --- git.repo / buildRepoHandle tests ---

func TestRepoInit_CreatesGitDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myproject")

	_, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		t.Error(".git directory was not created")
	}
}

func TestRepoOpen_FailsWithoutGit(t *testing.T) {
	workspace := t.TempDir()

	_, err := git.PlainOpen(workspace)
	if err == nil {
		t.Error("expected error opening dir without .git")
	}
}

func TestRepoStatusAddCommit(t *testing.T) {
	workspace := t.TempDir()
	repo, err := git.PlainInit(workspace, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}

	// Create a file
	os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello world"), 0644)

	wt, _ := repo.Worktree()

	// Check status — should show untracked
	st, err := wt.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, ok := st["hello.txt"]; !ok {
		t.Error("hello.txt should appear in status")
	}

	// Stage file
	_, err = wt.Add("hello.txt")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Commit
	hash, err := wt.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if hash.IsZero() {
		t.Error("commit hash should not be zero")
	}

	// Status should be clean now
	st, _ = wt.Status()
	if !st.IsClean() {
		t.Error("status should be clean after commit")
	}
}

func TestRepoBranchCreateAndList(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)
	wt, _ := repo.Worktree()

	// Need at least one commit to create branches
	os.WriteFile(filepath.Join(workspace, "init.txt"), []byte("init"), 0644)
	wt.Add("init.txt")
	wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	// Check HEAD
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if !headRef.Name().IsBranch() {
		t.Error("HEAD should be on a branch")
	}

	// Create a new branch ref
	branchRef := plumbing.NewBranchReferenceName("feature")
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("SetReference: %v", err)
	}

	// List branches
	branches, _ := repo.Branches()
	var names []string
	branches.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	})

	if len(names) < 2 {
		t.Errorf("expected at least 2 branches, got %v", names)
	}
}

func TestRepoCheckout(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)
	wt, _ := repo.Worktree()

	// Initial commit
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("v1"), 0644)
	wt.Add("file.txt")
	wt.Commit("v1", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	// Create a branch by creating a ref
	headRef, _ := repo.Head()
	branchRef := plumbing.NewBranchReferenceName("feature")
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())
	repo.Storer.SetReference(ref)

	// Checkout feature
	err := wt.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	newHead, _ := repo.Head()
	if newHead.Name().Short() != "feature" {
		t.Errorf("expected branch 'feature', got %q", newHead.Name().Short())
	}
}

func TestRepoTagCreateAndList(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)
	wt, _ := repo.Worktree()

	// Need a commit to tag
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("content"), 0644)
	wt.Add("file.txt")
	hash, _ := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	// Create tag
	_, err := repo.CreateTag("v1.0", hash, nil)
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	// List tags
	tags, err := repo.Tags()
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	var tagNames []string
	tags.ForEach(func(ref *plumbing.Reference) error {
		tagNames = append(tagNames, ref.Name().Short())
		return nil
	})

	if len(tagNames) != 1 || tagNames[0] != "v1.0" {
		t.Errorf("expected [v1.0], got %v", tagNames)
	}

	// Delete tag
	err = repo.DeleteTag("v1.0")
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	tags2, _ := repo.Tags()
	count := 0
	tags2.ForEach(func(ref *plumbing.Reference) error {
		count++
		return nil
	})
	if count != 0 {
		t.Errorf("expected 0 tags after delete, got %d", count)
	}
}

func TestRepoStashAndPop(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)
	wt, _ := repo.Worktree()

	// Initial commit
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("original"), 0644)
	wt.Add("file.txt")
	wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	// Modify file
	os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("modified"), 0644)

	// Verify dirty
	st, _ := wt.Status()
	if st.IsClean() {
		t.Fatal("status should be dirty")
	}

	// Stash: stage all, commit to refs/stash, hard reset back
	wt.AddWithOptions(&git.AddOptions{All: true})
	headRef, _ := repo.Head()
	stashHash, _ := wt.Commit("stash", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	stashRefName := plumbing.ReferenceName("refs/stash")
	stashRef := plumbing.NewHashReference(stashRefName, stashHash)
	repo.Storer.SetReference(stashRef)

	wt.Reset(&git.ResetOptions{
		Commit: headRef.Hash(),
		Mode:   git.HardReset,
	})

	// File should be back to original
	data, _ := os.ReadFile(filepath.Join(workspace, "file.txt"))
	if string(data) != "original" {
		t.Errorf("expected 'original' after stash, got %q", string(data))
	}

	// Pop: read stash, write files back
	savedRef, err := repo.Reference(stashRefName, true)
	if err != nil {
		t.Fatalf("stash ref missing: %v", err)
	}
	stashCommit, _ := repo.CommitObject(savedRef.Hash())
	stashTree, _ := stashCommit.Tree()

	stashTree.Files().ForEach(func(f *object.File) error {
		content, _ := f.Contents()
		absPath := filepath.Join(workspace, f.Name)
		os.MkdirAll(filepath.Dir(absPath), 0755)
		os.WriteFile(absPath, []byte(content), 0644)
		return nil
	})

	repo.Storer.RemoveReference(stashRefName)

	// File should be modified again
	data, _ = os.ReadFile(filepath.Join(workspace, "file.txt"))
	if string(data) != "modified" {
		t.Errorf("expected 'modified' after pop, got %q", string(data))
	}
}

func TestResolveGitAuth_NilStore(t *testing.T) {
	auth := resolveGitAuth(nil, "https://github.com/user/repo.git", nil, nil)
	if auth != nil {
		t.Error("expected nil auth with nil store")
	}
}

func TestGetAuthorSignature_Default(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)

	sig := getAuthorSignature(repo)
	if sig.Name == "" {
		t.Error("expected non-empty author name")
	}
	if sig.Email == "" {
		t.Error("expected non-empty author email")
	}
}

func TestRepoRemoteAddRemove(t *testing.T) {
	workspace := t.TempDir()
	repo, _ := git.PlainInit(workspace, false)

	// No remotes initially
	remotes, _ := repo.Remotes()
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes, got %d", len(remotes))
	}

	// Add remote
	repo.CreateRemote(&gitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://github.com/user/repo.git"},
	})

	remotes, _ = repo.Remotes()
	if len(remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(remotes))
	}
	if remotes[0].Config().Name != "origin" {
		t.Errorf("expected remote name 'origin', got %q", remotes[0].Config().Name)
	}

	// Remove remote
	repo.DeleteRemote("origin")
	remotes, _ = repo.Remotes()
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes after delete, got %d", len(remotes))
	}
}
