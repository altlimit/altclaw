### [ git ] - Workspace Version History & Repository Management

Auto-snapshots workspace after every agent turn. History stored separately from user's own .git.

[ History ]
* git.log(n?) → [{hash, message, date, files}] (Recent snapshots, default 10)
* git.status() → [{path, status}] (Changed files since last snapshot. status: "modified"|"added"|"deleted")
* git.diff(path?) → string (Unified diff: working tree vs last snapshot)
* git.diff(path, ref) → string (Diff against a specific commit hash)
* git.show(path, ref?) → string (File content at a specific commit. Default: last commit)

[ Snapshot & Restore ]
* git.commit(msg?) → {hash, files} | "no changes" (Manual snapshot with optional message)
* git.restore(path, ref?) → "restored" (Restore a file to its state at a commit. Default: last commit)

[ Maintenance ]
* git.compact(n?) → string (Keep only last N commits, default 50. Min 5)

[ Workspace .git — Repository Management ]
Manage real .git repositories inside the workspace. No git CLI needed.

  Opening / Creating:
  * git.repo(path?) → repoHandle (Open existing .git repo. Default: workspace root)
  * git.init(path?, origin?) → repoHandle (Init new repo. Optional remote origin URL)
  * git.clone(url, pathOrOpts?) → repoHandle (Clone remote repo into workspace)
    - String shorthand: git.clone(url, "mydir")
    - Options object: git.clone(url, {path, branch, depth, auth})

  Status & Staging:
  * repo.status() → [{path, status, staging}] (status: "modified"|"added"|"deleted"|"untracked"|"renamed". staging: "staged"|"unstaged"|"both")
  * repo.add(path) → "staged" (Stage a file. Use "." to stage all)
  * repo.reset(path?) → "reset" (Unstage file(s). Soft reset only — never touches working tree)

  Commits & Log:
  * repo.commit(msg) → {hash, branch} (Commit staged changes)
  * repo.log(n?) → [{hash, message, author, date}] (Recent commits, default 10)

  Diffs:
  * repo.diff(path?) → string (Unstaged changes vs HEAD)
  * repo.diffStaged(path?) → string (Staged changes vs HEAD)

  Branches:
  * repo.branch() → {current, list} (List branches + current)
  * repo.branch(name) → "created" (Create new branch at HEAD)
  * repo.deleteBranch(name) → "deleted" (Remove branch. Cannot delete current branch)
  * repo.checkout(ref, opts?) → "checked out" (Switch branch or detach to commit. opts: {force: true})

  Tags:
  * repo.tag(name, opts?) → "tagged" (Create lightweight tag at HEAD. opts: {ref: "abc123"})
  * repo.tags() → ["v1.0", "v1.1", ...] (List all tags)
  * repo.deleteTag(name) → "deleted" (Remove a tag)

  Remotes:
  * repo.remote() → [{name, urls}] (List configured remotes)
  * repo.addRemote(name, url) → "added" (Add a new remote)
  * repo.removeRemote(name) → "removed" (Remove a remote)

  Push & Pull:
  * repo.pull(opts?) → "pulled" | "already up to date" (opts: {remote, auth})
  * repo.push(opts?) → "pushed" | "already up to date" (opts: {remote, auth})
    Auth resolves automatically: 1) explicit opts.auth 2) auto-detect from secrets (GITHUB_TOKEN, GITLAB_TOKEN, GIT_TOKEN, SSH_KEY) 3) unauthenticated
    HTTPS auth: {auth: {token: "{{secrets.GITHUB_TOKEN}}"}} or {auth: {user: "...", pass: "..."}}
    SSH auth: {auth: {key: "{{secrets.SSH_KEY}}"}} (PEM private key. user defaults to "git", override with {key: "...", user: "deploy"})

  Stash (single-slot):
  * repo.stash() → "stashed" | "nothing to stash" (Saves working changes. Only one stash supported)
  * repo.stashPop() → "popped" (Restores stashed changes. Throws if no stash exists)
