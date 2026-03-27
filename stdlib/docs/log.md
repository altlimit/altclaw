### [ log ] - Application Logs

[ Read ]
* log.recent(n?: number) → [{time, level, msg, attrs?}]
  Returns the last N log entries (default 50, max 200). Newest first.
* log.search(query: string) → [{time, level, msg, attrs?}]
  Keyword search across log messages and attribute values.

[ Write ]
* log.debug(msg: string, ...keyValuePairs) → void
* log.info(msg: string, ...keyValuePairs) → void
* log.warn(msg: string, ...keyValuePairs) → void
* log.error(msg: string, ...keyValuePairs) → void
  Emit a structured log entry. Extra args are key-value attribute pairs.
  Example: log.info("request handled", "method", "GET", "status", "200")
