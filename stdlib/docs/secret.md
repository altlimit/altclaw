# secret

The `secret` API provides read-only awareness of secure credentials configured in the workspace or user account.

### How to use secrets

To prevent accidental exfiltration of API keys, **you cannot read raw secret values via JS code**. Instead, use the template syntax `{{secrets.YOUR_SECRET_NAME}}` in strings passed to trusted bridge functions (`fetch` and `sys`). The Go backend will securely expand these templates out-of-band *before* executing the network or shell operation.

#### 1. `fetch` examples:
The backend will expand the token in the URL, Headers, and Body before sending the request.
```javascript
let resp = fetch('https://api.github.com/user', {
  headers: {
    'Authorization': 'Bearer {{secrets.GITHUB_TOKEN}}'
  }
});
```

#### 2. `sys` examples:
**SECURITY RESTRICTION:** Secrets in `sys` functions are **ONLY** expanded within the `env` options dictionary payload. They will NOT be expanded in the `cmd` string or `args` array. This prevents accidental logging of secrets to terminal output buffers.

```javascript
// Correct: pass secret via environment variable map
let out = sys.call("curl", {
  args: ["-s", "-H", "Authorization: Bearer $API_KEY", "https://api.example.com"],
  env: { "API_KEY": "{{secrets.MY_API_KEY}}" }
});

// INCORRECT (Will fail — secret template is not expanded in arguments array):
sys.call("curl", ["-H", "Authorization: Bearer {{secrets.MY_API_KEY}}", "..."]);
```

### API Reference

- `list()`
  Returns an array of strings representing the names of all available secrets in the current workspace.
  ```javascript
  let keys = secret.list(); // ["GITHUB_TOKEN", "AWS_KEY"]
  ```

- `exists(name)`
  Returns `true` if the secret exists.
  ```javascript
  if (!secret.exists('GITHUB_TOKEN')) {
     ui.log("Missing token");
  }
  ```

- `set(name, value)`
  Dynamically creates or updates a secret in the current workspace. The plain-text `value` is automatically scrubbed from the recent chat history database, replacing past occurrences with `[REDACTED: {{secrets.NAME}}]`.
  ```javascript
  secret.set('TEMP_OAUTH_TOKEN', 'ya29.a0AfB_cmp...');
  ui.log("Saved temporary token securely");
  ```

- `rm(name)`
  Deletes a secret from the current workspace.
  ```javascript
  secret.rm('TEMP_OAUTH_TOKEN');
  ```
