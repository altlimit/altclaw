### [ cache ] - TTL Key-Value Cache

[ Read/Write ]
* cache.set(key: string, value: string, ttlSec?: number) → void
  Store a value with TTL (default 3600). Keys auto-prefixed per workspace.
* cache.get(key: string) → string | null
  Retrieve a cached value, null if missing or expired.
* cache.del(key: string) → void
  Delete a cached key.
* cache.has(key: string) → boolean
  Check if key exists.

[ Rate Limiting ]
* cache.rate(key: string, limit: number, windowSec: number) → {allowed, remaining, resetAt}
  Check if request is within rate limit. Example: cache.rate("api:" + ip, 100, 60)
