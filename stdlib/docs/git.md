### [ git ] - Workspace Version History

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
