### [ fetch ] - HTTP Client
**WARNING:** SSRF-protected (blocks private/loopback IPs).

* fetch(url: string, opts?: object) → Response
  - opts.method: string (e.g., "GET", "POST")
  - opts.body: string
  - opts.headers: object (e.g., {"Content-Type": "application/json"})
  - opts.download: string (File path. Use this to stream files > 32MB directly to disk)
  *Note:* You can securely inject API keys in urls, headers, or string bodies using `{{secrets.YOUR_KEY}}`.

[ Response Object ]
* status: number (HTTP status code)
* statusText: string (HTTP status line)
* headers: object (lowercase keys)
* headers.get(name: string) → string|null
* text() → string (body as text)
* json() → object (body parsed as JSON)
