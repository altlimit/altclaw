### [ mem ] - Persistent Memory

[ Write ]
* mem.add(content: string, kind?: string, scope?: string) → number (id)
  - kind: "core" (permanent), "learned" (30d, default), "note" (7d)
  - scope: "user" for global (cross-workspace), omit for workspace-local
  - TIP: If you keep trying or struggle to figure something out, use mem.add on the success case to help learn it for next time!

[ Inline Save — ```mem block ]
Include a ```mem block after your final ```md response to save a lesson without using an extra iteration:
  ```md
  Here is your answer...
  ```
  ```mem
  Lesson learned: the XYZ API requires...
  ```
- Specify kind on the fence: ```mem core (permanent), ```mem learned (30d, default), ```mem note (7d).
- Only processed on the final turn (when no ```exec blocks are present).
- Content is NOT shown to the user — it's silently stored.
- Only save genuinely useful lessons, not routine facts.

[ Read ]
* mem.recent(days?: number) → [{id, content, kind, scope, created}]
* mem.core() → [{id, content, kind, scope, created}] (Permanent entries only)
* mem.all() → [{id, content, kind, scope, created}]
* mem.search(query: string) → [{id, content, kind, scope, created}]

[ Manage ]
* mem.rm(id: number, scope?: string) → void
  - scope: "user" for global entries (entries with scope "user" in list results)
* mem.promote(id: number, scope?: string) → void (Promote entry to "core"/permanent)
  - scope: "user" for global entries
