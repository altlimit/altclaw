### [ conn ] - Persistent Connections (WebSocket, SSE)

Background connections managed by Go with warm VM handlers. Auto-reconnect with exponential backoff (1s → 30s max). Connections survive across script executions and restart on app launch.

## API

* conn.open(url: string, handler: string, opts?: object) → string (id)
  - url: Connection endpoint. Type auto-detected from scheme (ws://, wss:// → WebSocket).
  - handler: Path to handler module (require()-resolvable). Can be workspace file or installed module (e.g. "kraken/trader_ws").
  - opts.type: Force connection type: "ws" (WebSocket) or "sse" (Server-Sent Events). Auto-detected from URL if omitted.
  - opts.reconnect: Auto-reconnect on disconnect (default: true).
  - opts.headers: Custom headers as {key: value} object.
  - Dedup: If a connection with the same url, type, handler, and headers already exists, returns the existing ID instead of creating a duplicate.

* conn.list() → [{id, type, url, handler, status, messages_in, errors, reconnects, ...}]
  Returns all active connections with live stats.

* conn.send(id: string, data: any) → void
  Send data on a connection. Objects are JSON-serialized. Not supported for SSE (read-only).

* conn.close(id: string) → void
  Close and remove a connection permanently.

## Handler Module Format

Handler is a CommonJS module with lifecycle callbacks:

```javascript
// .agent/my_handler.js
module.exports = {
  onConnect: function(conn) {
    // Connected — send subscriptions, init state
    conn.send({ method: "subscribe", params: { channel: "ticker" } });
  },
  onMessage: function(conn, msg) {
    // msg is auto-parsed from JSON when possible
    ui.log("Received: " + JSON.stringify(msg));
    store.lastPrice = msg.price; // store persists across messages
  },
  onClose: function(conn, reason) {
    ui.log("Disconnected: " + reason);
  },
  onError: function(conn, err) {
    // Return true to force retry, false to stop, or nothing for default behavior
    ui.log("Error: " + err);
    if (err.indexOf("429") >= 0) return true;  // rate-limited, retry
  }
};
```

If module.exports is a plain function, it is treated as onMessage.

## conn object (passed to handlers)

* conn.id — Connection ID (string)
* conn.url — Connection URL
* conn.send(data) — Send data (objects auto-serialized to JSON)
* conn.close() — Request connection close

## Connection Types

* **ws** — WebSocket (bidirectional). Auto-detected from ws:// or wss:// URLs.
* **sse** — Server-Sent Events (read-only). Must specify `{type: "sse"}` since URLs use http/https.

## Examples

```javascript
// WebSocket — type auto-detected
conn.open("wss://ws.example.com/feed", ".agent/ws_handler.js");

// SSE — must specify type
conn.open("https://api.example.com/stream", ".agent/sse_handler.js", { type: "sse" });

// With custom headers and no auto-reconnect
conn.open("wss://api.example.com/ws", "my_module/handler", {
  headers: { "Authorization": "Bearer {{secrets.API_KEY}}" },
  reconnect: false
});
```
