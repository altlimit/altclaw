package bridge

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/dop251/goja"
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

		var privBlock, pubBlock *pem.Block

		if keyType == "rsa" {
			modulusLength := 2048 // default
			if mod := optsObj.Get("modulusLength"); mod != nil && !goja.IsUndefined(mod) {
				modulusLength = int(mod.ToInteger())
			}

			privKey, err := rsa.GenerateKey(rand.Reader, modulusLength)
			if err != nil {
				Throwf(vm, "Failed to generate RSA key: %v", err)
			}

			// Private Key pkcs8
			privBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
			if err != nil {
				Throwf(vm, "Failed to marshal RSA private key: %v", err)
			}
			privBlock = &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}

			// Public Key spki
			pubBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
			if err != nil {
				Throwf(vm, "Failed to marshal RSA public key: %v", err)
			}
			pubBlock = &pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}

		} else if keyType == "ed25519" {
			pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				Throwf(vm, "Failed to generate ed25519 key: %v", err)
			}

			privBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
			if err != nil {
				Throwf(vm, "Failed to marshal ed25519 private key: %v", err)
			}
			privBlock = &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}

			pubBytes, err := x509.MarshalPKIXPublicKey(pubKey)
			if err != nil {
				Throwf(vm, "Failed to marshal ed25519 public key: %v", err)
			}
			pubBlock = &pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}

		} else {
			Throwf(vm, "Unsupported key type: %s", keyType)
		}

		res := vm.NewObject()
		res.Set("publicKey", string(pem.EncodeToMemory(pubBlock)))
		res.Set("privateKey", string(pem.EncodeToMemory(privBlock)))

		return res
	})

	vm.Set(NameCrypto, cryptoObj)
}
