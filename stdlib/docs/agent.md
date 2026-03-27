### [ agent ] - Sub-Agent Spawning

* agent.run(task: string, providerName?: string) → string (handleID)
  Spawn sub-agent in background. Optional provider name for specialist delegation.
* agent.result(handleID: string) → string
  Block execution until sub-agent completes. Returns its response.
* agent.status(handleID: string) → {done: bool, result?: string, error?: string}
  Non-blocking status check. Use to poll multiple agents without blocking.
  Tip: If you need to wait, use a `while(!status.done) { sleep(5000); status = agent.status(id); }` loop inside your script rather than returning early to the AI. This saves tokens and avoids needless turns. Wait for at least 30-60 seconds before giving up and calling `agent.kill(id)`.
* agent.kill(handleID: string)
  Immediately terminates the running sub-agent.
