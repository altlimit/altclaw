### [ chat ] - Cross-Conversation Access

Read-only access to other conversations in the same workspace.

[ List ]
* chat.list(opts?: {limit?}) → [{id, title, provider, created, modified}]
  List workspace chats, newest first. limit defaults to 20.

[ Read ]
* chat.read(chatID: number, opts?: {limit?}) → [{role, content, created}]
  Read messages from a specific chat. limit defaults to 50 (from the end).
  Filters out execution noise — only returns clean user questions and final assistant responses.
