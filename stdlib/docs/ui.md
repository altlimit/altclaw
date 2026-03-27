### [ ui ] - User Interaction

* ui.log(args...) → void
  Log timestamped message visible to user (Max 5000 chars). Use to explain what you are doing.
* ui.ask(question: string) → string
  Prompt user and block execution until they respond. Returns their answer.
* ui.confirm(action: string, params?: object) → {approved: boolean, result?: any, error?: string}
  Propose a privileged action for user approval. Shows a structured confirmation card.
  Blocks until user approves or rejects. If approved, executes the action server-side.
  Available actions:
  - "tunnel.connect" — Enable tunnel (no params)
  - "tunnel.disconnect" — Disable tunnel (no params)
  - "tunnel.pair" — Pair with hub ({code: "123456"})
  - "tunnel.unpair" — Unpair from hub (no params)
  - "provider.add" — Add provider ({name, provider, model, api_key, ...})
  - "provider.update" — Update provider ({id or name, ...fields})
  - "provider.delete" — Delete provider ({id or name})
  - "settings.update" — Update settings ({scope: "workspace"|"user", ...partial fields})
* ui.file(path: string) → void
  Attach a workspace file (image, PDF, etc.) for AI analysis in the next iteration.
