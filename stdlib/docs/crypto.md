### [ crypto ] - Node-Compatible Cryptography

Access using `require('crypto')`. Currently provides secure, native key generation mirroring the Node.js API to avoid relying on external shell commands like `ssh-keygen`.

#### Methods

* `generateKeyPairSync(type: 'rsa' | 'ed25519', options: object) → { publicKey: string, privateKey: string }`
  Generates a new asymmetric key pair synchronously. The returned keys are formatted as PEM strings matching standard OpenSSH/PKCS8 formats.

  **Supported Options:**
  - `modulusLength` (Number): Key size in bits (RSA only, e.g., 2048 or 4096).
  - `publicKeyEncoding` (Object): `{ type: 'spki', format: 'pem' }`
  - `privateKeyEncoding` (Object): `{ type: 'pkcs8', format: 'pem' }`

**Example:**
```javascript
const crypto = require('crypto');
const { publicKey, privateKey } = crypto.generateKeyPairSync('ed25519', {
  publicKeyEncoding: { type: 'spki', format: 'pem' },
  privateKeyEncoding: { type: 'pkcs8', format: 'pem' }
});

fs.write(".agent/ssh/id_ed25519", privateKey);
fs.write(".agent/ssh/id_ed25519.pub", publicKey);
output("Keys saved successfully!");
```
