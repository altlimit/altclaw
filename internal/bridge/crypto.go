package bridge

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"hash"
	"strings"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
	gossh "golang.org/x/crypto/ssh"
)

// RegisterCrypto adds a Node.js-like crypto namespace to the runtime.
// store and ctxFn are used for {{secrets.NAME}} expansion in HMAC keys and hash data.
func RegisterCrypto(vm *goja.Runtime, store *config.Store, ctxFn ...func() context.Context) {
	cryptoObj := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	// ---------------------------------------------------------------
	// crypto.createHash(algorithm) → Hash
	// Node.js-compatible: returns object with .update(data) and .digest(encoding?)
	// Supported algorithms: "sha256", "sha512"
	// Supported encodings: "hex" (default), "base64", "binary"
	// The data passed to .update() supports {{secrets.NAME}} expansion.
	// ---------------------------------------------------------------
	cryptoObj.Set("createHash", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "crypto.createHash requires an algorithm argument")
		}
		algo := strings.ToLower(call.Arguments[0].String())

		var h hash.Hash
		switch algo {
		case "sha256":
			h = sha256.New()
		case "sha512":
			h = sha512.New()
		default:
			Throwf(vm, "crypto.createHash: unsupported algorithm %q (supported: sha256, sha512)", algo)
		}

		hashObj := vm.NewObject()
		// Collect all data, expand secrets, then write on digest
		var parts []string

		// .update(data) → returns hashObj for chaining
		hashObj.Set("update", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				Throw(vm, "hash.update requires a data argument")
			}
			raw := call.Arguments[0].String()

			// Check for optional input encoding (second arg)
			// Node.js supports: 'utf8' (default), 'binary', 'base64', 'hex'
			inputEnc := ""
			if len(call.Arguments) >= 2 {
				inputEnc = strings.ToLower(call.Arguments[1].String())
			}

			// Expand secrets in the data
			ctx := getCtx()
			expanded := maybeExpandSecrets(ctx, store, raw)

			switch inputEnc {
			case "binary":
				// Latin-1: each JS char code → one byte (Node.js "binary" encoding)
				h.Write(decodeBinaryString(expanded))
			case "base64":
				decoded, err := base64.StdEncoding.DecodeString(expanded)
				if err != nil {
					Throwf(vm, "hash.update: invalid base64 input: %v", err)
				}
				h.Write(decoded)
			case "hex":
				decoded, err := hex.DecodeString(expanded)
				if err != nil {
					Throwf(vm, "hash.update: invalid hex input: %v", err)
				}
				h.Write(decoded)
			default:
				// utf8 or unspecified
				h.Write([]byte(expanded))
			}

			parts = append(parts, expanded) // track for reference
			return vm.ToValue(hashObj)
		})

		// .digest(encoding?) → string
		hashObj.Set("digest", func(call goja.FunctionCall) goja.Value {
			encoding := "hex"
			if len(call.Arguments) >= 1 {
				encoding = strings.ToLower(call.Arguments[0].String())
			}

			sum := h.Sum(nil)
			return vm.ToValue(encodeBytes(vm, sum, encoding))
		})

		return vm.ToValue(hashObj)
	})

	// ---------------------------------------------------------------
	// crypto.createHmac(algorithm, key) → Hmac
	// Node.js-compatible: returns object with .update(data) and .digest(encoding?)
	// Supported algorithms: "sha256", "sha512"
	// The key supports {{secrets.NAME}} expansion — this is critical for
	// APIs like Kraken where the HMAC key is a secret.
	// If the key contains a secrets placeholder, it's expanded Go-side
	// so the raw value never touches JS.
	// ---------------------------------------------------------------
	cryptoObj.Set("createHmac", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "crypto.createHmac requires algorithm and key arguments")
		}
		algo := strings.ToLower(call.Arguments[0].String())
		rawKey := call.Arguments[1].String()

		// Expand secrets in the key
		ctx := getCtx()
		expandedKey := maybeExpandSecrets(ctx, store, rawKey)

		// Convert key to bytes using Latin-1 decoding (same as Node.js "binary" encoding).
		// This is critical when the key comes from crypto.base64Decode() which returns
		// a Latin-1 binary string — using Go's []byte() would re-encode as UTF-8 and
		// corrupt any bytes > 127.
		keyBytes := decodeBinaryString(expandedKey)

		var newHash func() hash.Hash
		switch algo {
		case "sha256":
			newHash = sha256.New
		case "sha512":
			newHash = sha512.New
		default:
			Throwf(vm, "crypto.createHmac: unsupported algorithm %q (supported: sha256, sha512)", algo)
		}

		mac := hmac.New(newHash, keyBytes)

		hmacObj := vm.NewObject()

		// .update(data, inputEncoding?) → returns hmacObj for chaining
		hmacObj.Set("update", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				Throw(vm, "hmac.update requires a data argument")
			}
			raw := call.Arguments[0].String()

			// Check for optional input encoding
			inputEnc := ""
			if len(call.Arguments) >= 2 {
				inputEnc = strings.ToLower(call.Arguments[1].String())
			}

			// Expand secrets in data too
			ctx := getCtx()
			expanded := maybeExpandSecrets(ctx, store, raw)

			switch inputEnc {
			case "binary":
				// Latin-1: each JS char code → one byte (Node.js "binary" encoding)
				mac.Write(decodeBinaryString(expanded))
			case "base64":
				decoded, err := base64.StdEncoding.DecodeString(expanded)
				if err != nil {
					Throwf(vm, "hmac.update: invalid base64 input: %v", err)
				}
				mac.Write(decoded)
			case "hex":
				decoded, err := hex.DecodeString(expanded)
				if err != nil {
					Throwf(vm, "hmac.update: invalid hex input: %v", err)
				}
				mac.Write(decoded)
			default:
				mac.Write([]byte(expanded))
			}

			return vm.ToValue(hmacObj)
		})

		// .digest(encoding?) → string
		hmacObj.Set("digest", func(call goja.FunctionCall) goja.Value {
			encoding := "hex"
			if len(call.Arguments) >= 1 {
				encoding = strings.ToLower(call.Arguments[0].String())
			}

			sum := mac.Sum(nil)
			return vm.ToValue(encodeBytes(vm, sum, encoding))
		})

		return vm.ToValue(hmacObj)
	})

	// ---------------------------------------------------------------
	// crypto.randomBytes(n) → string (hex-encoded)
	// Generates n cryptographically secure random bytes.
	// Returns hex-encoded string (2*n characters).
	// ---------------------------------------------------------------
	cryptoObj.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "crypto.randomBytes requires a size argument")
		}
		n := int(call.Arguments[0].ToInteger())
		if n <= 0 || n > 1024 {
			Throwf(vm, "crypto.randomBytes: size must be between 1 and 1024, got %d", n)
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			Throwf(vm, "crypto.randomBytes: %v", err)
		}
		return vm.ToValue(hex.EncodeToString(buf))
	})

	// ---------------------------------------------------------------
	// crypto.generateKeyPairSync(type, options) → {publicKey, privateKey}
	// (Existing implementation, preserved as-is)
	// ---------------------------------------------------------------
	cryptoObj.Set("generateKeyPairSync", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "crypto.generateKeyPairSync requires type and options arguments")
		}

		keyType := call.Arguments[0].String()
		optsObj := call.Arguments[1].ToObject(vm)
		if optsObj == nil {
			Throw(vm, "options must be an object")
		}

		pubEnc := optsObj.Get("publicKeyEncoding")
		if pubEnc == nil || goja.IsUndefined(pubEnc) {
			Throw(vm, "publicKeyEncoding option is required")
		}

		privEnc := optsObj.Get("privateKeyEncoding")
		if privEnc == nil || goja.IsUndefined(privEnc) {
			Throw(vm, "privateKeyEncoding option is required")
		}

		// Read format options
		pubFormat := "pem"
		privFormat := "pem"
		if pubEncObj := pubEnc.ToObject(vm); pubEncObj != nil {
			if f := pubEncObj.Get("format"); f != nil && !goja.IsUndefined(f) {
				pubFormat = f.String()
			}
		}
		if privEncObj := privEnc.ToObject(vm); privEncObj != nil {
			if f := privEncObj.Get("format"); f != nil && !goja.IsUndefined(f) {
				privFormat = f.String()
			}
		}

		var pubKeyStr, privKeyStr string

		if keyType == "rsa" {
			modulusLength := 2048 // default
			if mod := optsObj.Get("modulusLength"); mod != nil && !goja.IsUndefined(mod) {
				modulusLength = int(mod.ToInteger())
			}

			privKey, err := rsa.GenerateKey(rand.Reader, modulusLength)
			if err != nil {
				Throwf(vm, "Failed to generate RSA key: %v", err)
			}

			privKeyStr = marshalPrivateKey(vm, privKey, privFormat)
			pubKeyStr = marshalPublicKey(vm, &privKey.PublicKey, pubFormat)

		} else if keyType == "ed25519" {
			pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				Throwf(vm, "Failed to generate ed25519 key: %v", err)
			}

			privKeyStr = marshalPrivateKey(vm, privKey, privFormat)
			pubKeyStr = marshalPublicKey(vm, pubKey, pubFormat)

		} else {
			Throwf(vm, "Unsupported key type: %s", keyType)
		}

		res := vm.NewObject()
		res.Set("publicKey", pubKeyStr)
		res.Set("privateKey", privKeyStr)

		return res
	})

	// ---------------------------------------------------------------
	// crypto.base64Encode(data) → string
	// Base64 encode a string. Supports {{secrets.NAME}} expansion.
	// ---------------------------------------------------------------
	cryptoObj.Set("base64Encode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "crypto.base64Encode requires a data argument")
		}
		raw := call.Arguments[0].String()
		ctx := getCtx()
		expanded := maybeExpandSecrets(ctx, store, raw)
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(expanded)))
	})

	// ---------------------------------------------------------------
	// crypto.base64Decode(data) → string (binary)
	// Base64 decode a string. Supports {{secrets.NAME}} expansion.
	// Returns the decoded bytes as a binary string (each byte → one char).
	// ---------------------------------------------------------------
	cryptoObj.Set("base64Decode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "crypto.base64Decode requires a data argument")
		}
		raw := call.Arguments[0].String()
		ctx := getCtx()
		expanded := maybeExpandSecrets(ctx, store, raw)
		decoded, err := base64.StdEncoding.DecodeString(expanded)
		if err != nil {
			Throwf(vm, "crypto.base64Decode: invalid base64: %v", err)
		}
		// Return as Latin-1 binary string (same as encodeBytes with "binary")
		runes := make([]rune, len(decoded))
		for i, b := range decoded {
			runes[i] = rune(b)
		}
		return vm.ToValue(string(runes))
	})

	vm.Set(NameCrypto, cryptoObj)
}

// encodeBytes converts a byte slice to a string using the given encoding.
func encodeBytes(vm *goja.Runtime, data []byte, encoding string) string {
	switch encoding {
	case "hex":
		return hex.EncodeToString(data)
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	case "binary":
		// Node.js "binary" encoding is Latin-1 (ISO-8859-1).
		// Each byte maps 1:1 to a Unicode code point (0x00–0xFF).
		// We must NOT use Go's string(data) which interprets as UTF-8.
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes)
	default:
		Throwf(vm, "unsupported encoding %q (supported: hex, base64, binary)", encoding)
		return "" // unreachable
	}
}

// decodeBinaryString converts a "binary" (Latin-1) encoded string back to bytes.
// Each Unicode code point maps 1:1 to a byte. This is the inverse of encodeBytes
// with encoding="binary".
func decodeBinaryString(s string) []byte {
	runes := []rune(s)
	buf := make([]byte, len(runes))
	for i, r := range runes {
		buf[i] = byte(r)
	}
	return buf
}

// marshalPrivateKey encodes a private key in the requested format.
// "pem" → PKCS8 PEM, "openssh" → OpenSSH PEM (ssh-keygen compatible).
func marshalPrivateKey(vm *goja.Runtime, key interface{}, format string) string {
	switch format {
	case "openssh":
		// Marshal to OpenSSH format (-----BEGIN OPENSSH PRIVATE KEY-----)
		// This is what ssh-keygen produces and what go-git expects.
		block, err := gossh.MarshalPrivateKey(key, "")
		if err != nil {
			Throwf(vm, "Failed to marshal private key to OpenSSH format: %v", err)
		}
		return string(pem.EncodeToMemory(block))

	default: // "pem"
		privBytes, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			Throwf(vm, "Failed to marshal private key: %v", err)
		}
		block := &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}
		return string(pem.EncodeToMemory(block))
	}
}

// marshalPublicKey encodes a public key in the requested format.
// "pem" → SPKI PEM, "openssh" → authorized_keys format (ssh-ed25519 AAAA...).
func marshalPublicKey(vm *goja.Runtime, key interface{}, format string) string {
	switch format {
	case "openssh":
		// Marshal to OpenSSH authorized_keys format (ssh-ed25519 AAAA...)
		// This is the format GitHub/GitLab/etc expect for SSH deploy keys.
		sshPub, err := gossh.NewPublicKey(key)
		if err != nil {
			Throwf(vm, "Failed to marshal public key to OpenSSH format: %v", err)
		}
		return string(gossh.MarshalAuthorizedKey(sshPub))

	default: // "pem"
		pubBytes, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			Throwf(vm, "Failed to marshal public key: %v", err)
		}
		block := &pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}
		return string(pem.EncodeToMemory(block))
	}
}
