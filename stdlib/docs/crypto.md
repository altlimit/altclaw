### [ crypto ] - Node-Compatible Cryptography

Access using `require('crypto')`. Provides secure hashing, HMAC, key generation, and base64 utilities.

#### Methods

* `createHash(algorithm: 'sha256' | 'sha512') → Hash`
  Creates a hash object for computing digests.

  **Hash methods:**
  - `.update(data: string, inputEncoding?: string) → Hash` — Add data to hash. Returns self for chaining. `inputEncoding`: `'utf8'` (default), `'binary'`, `'base64'`, `'hex'`. Data supports `{{secrets.NAME}}` expansion.
  - `.digest(encoding?: string) → string` — Compute the digest. `encoding`: `'hex'` (default), `'base64'`, `'binary'`.

  ```javascript
  var crypto = require('crypto');
  var hash = crypto.createHash('sha256').update('hello').digest('hex');
  // "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
  ```

* `createHmac(algorithm: 'sha256' | 'sha512', key: string) → Hmac`
  Creates an HMAC object. The `key` supports `{{secrets.NAME}}` expansion — the raw secret value is resolved Go-side and never exposed to JS.

  **Hmac methods:**
  - `.update(data: string, inputEncoding?: string) → Hmac` — Add data. Returns self for chaining.
  - `.digest(encoding?: string) → string` — Compute the HMAC digest.

  ```javascript
  var crypto = require('crypto');
  // HMAC with inline key
  var mac = crypto.createHmac('sha256', 'my-key').update('data').digest('hex');

  // HMAC with secret key (never exposed to JS)
  var secretKey = crypto.base64Decode('{{secrets.API_SECRET}}');
  var sig = crypto.createHmac('sha512', secretKey).update(message, 'binary').digest('base64');
  ```

* `randomBytes(size: number) → string`
  Generate `size` cryptographically secure random bytes, returned as a hex-encoded string (2×size chars). Max 1024.
  ```javascript
  var nonce = crypto.randomBytes(16); // "a1b2c3d4e5f6..."  (32 hex chars)
  ```

* `base64Encode(data: string) → string`
  Base64 encode a string. Supports `{{secrets.NAME}}` expansion.
  ```javascript
  crypto.base64Encode('hello world'); // "aGVsbG8gd29ybGQ="
  ```

* `base64Decode(data: string) → string`
  Base64 decode a string. Returns a binary string (Latin-1). Supports `{{secrets.NAME}}` expansion.
  ```javascript
  crypto.base64Decode('aGVsbG8gd29ybGQ='); // "hello world"
  ```

* `generateKeyPairSync(type: 'rsa' | 'ed25519', options: object) → { publicKey: string, privateKey: string }`
  Generates a new asymmetric key pair synchronously.

  **Supported Options:**
  - `modulusLength` (Number): Key size in bits (RSA only, e.g., 2048 or 4096).
  - `publicKeyEncoding` (Object): `{ type: 'spki', format: 'pem' | 'openssh' }`
  - `privateKeyEncoding` (Object): `{ type: 'pkcs8', format: 'pem' | 'openssh' }`

  **Formats:**
  - `'pem'` (default): PKCS8/SPKI PEM (-----BEGIN PRIVATE KEY----- / -----BEGIN PUBLIC KEY-----)
  - `'openssh'`: SSH-native format. Private key as OpenSSH PEM (-----BEGIN OPENSSH PRIVATE KEY-----), public key as authorized_keys line (ssh-ed25519 AAAA...). Use this for git SSH auth and GitHub/GitLab deploy keys.

**Example (SSH key for GitHub):**
```javascript
const crypto = require('crypto');
const { publicKey, privateKey } = crypto.generateKeyPairSync('ed25519', {
  publicKeyEncoding: { type: 'spki', format: 'openssh' },
  privateKeyEncoding: { type: 'pkcs8', format: 'openssh' }
});

secret.set('SSH_KEY', privateKey);
output({
  publicKey: publicKey,
  hint: "Add this public key to GitHub → Settings → SSH Keys"
});
```
