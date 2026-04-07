### [ crypto ] - Node-Compatible Cryptography

Access using `require('crypto')`. Currently provides secure, native key generation mirroring the Node.js API to avoid relying on external shell commands like `ssh-keygen`.

#### Methods

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
