### [ mem ] - Persistent Memory

[ Write ]
* mem.add(content: string, kind?: string) → string (id)
  - kind: "core" (permanent), "learned" (30d, default), "note" (7d)
  - TIP: If you keep trying or struggle to figure something out, use mem.add on the success case to help learn it for next time!
* mem.addUser(content: string, kind?: string) → string (id)
  Add user-level memory (global across all workspaces).

[ Read ]
* mem.recent(days?: number) → [{id, content, kind, created}]
* mem.core() → [{id, content, kind, created}] (Permanent entries only)
* mem.all() → [{id, content, kind, created}]
* mem.search(query: string) → [{id, content, kind, created}]

[ Manage ]
* mem.rm(id: string) → void (Delete entry)
* mem.promote(id: string) → void (Promote entry to "core"/permanent)
