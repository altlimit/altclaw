### [ fetch ] - HTTP Client
**WARNING:** SSRF-protected (blocks private/loopback IPs).

* fetch(url: string, opts?: object) → Response
  - opts.method: string (e.g., "GET", "POST")
  - opts.body: string | FormData
  - opts.headers: object (e.g., {"Content-Type": "application/json"})
  - opts.download: string (File path. Use this to stream files > 32MB directly to disk)
  - opts.secrets: boolean (default: false). Set to true to expand `{{secrets.}}` in body.
  *Note:* You can securely inject API keys in urls, headers, or string bodies using `{{secrets.YOUR_KEY}}`.

[ Response Object ]
* status: number (HTTP status code)
* statusText: string (HTTP status line)
* headers: object (lowercase keys)
* headers.get(name: string) → string|null
* text() → string (body as text)
* json() → object (body parsed as JSON)

[ FormData ] — Streaming form data uploads
Use FormData as the body to build form submissions. File paths are detected automatically
by "./" or "/" prefix and streamed from disk with constant memory.

* var fd = new FormData()
* fd.append(name, value)                  — text field (plain string)
* fd.append(name, "./path/to/file")       — file from workspace (streamed, constant memory)
* fd.append(name, "/path/to/file", name?) — file with optional custom filename
* fd.append(name, arrayBuffer, name?)     — in-memory binary data

**Encoding**: If any entry is a file, body is encoded as multipart/form-data.
If all entries are text, body is encoded as application/x-www-form-urlencoded.
File existence is validated at append() time — throws immediately if not found.

Example: multipart file upload
```js
var fd = new FormData();
fd.append("description", "Monthly report");
fd.append("file", "./reports/march.pdf");
var resp = fetch("https://api.example.com/upload", {
    method: "POST",
    body: fd,
    headers: { "Authorization": "Bearer {{secrets.API_TOKEN}}" }
});
```

Example: URL-encoded form POST
```js
var fd = new FormData();
fd.append("username", "john");
fd.append("password", "{{secrets.LOGIN_PW}}");
var resp = fetch("https://example.com/login", { method: "POST", body: fd, secrets: true });
```
