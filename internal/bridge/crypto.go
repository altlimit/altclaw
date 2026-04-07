package bridge

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/dop251/goja"
	gossh "golang.org/x/crypto/ssh"
)

// RegisterCrypto adds a Node.js-like crypto namespace to the runtime.
func RegisterCrypto(vm *goja.Runtime) {
	cryptoObj := vm.NewObject()

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

	vm.Set(NameCrypto, cryptoObj)
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

