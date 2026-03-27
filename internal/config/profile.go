package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"log/slog"

	"altclaw.ai/internal/util"
)

// DecryptProfileSecrets decrypts ECDH-encrypted profile secrets using the instance's
// P-256 private key. Each secret value is base64(ephemeral_pub_65B || iv_12B || ciphertext).
// Returns secrets with plaintext values. Secrets that fail to decrypt are skipped.
func DecryptProfileSecrets(secrets []Secret, secretPrivateKeyHex string) []Secret {
	if secretPrivateKeyHex == "" || len(secrets) == 0 {
		return secrets
	}

	privBytes, err := hex.DecodeString(secretPrivateKeyHex)
	if err != nil {
		slog.Warn("failed to decode secret private key", "error", err)
		return secrets
	}

	privKey, err := ecdh.P256().NewPrivateKey(privBytes)
	if err != nil {
		slog.Warn("failed to parse secret private key", "error", err)
		return secrets
	}

	var result []Secret
	for _, s := range secrets {
		plainValue, err := decryptECDH(s.Value, privKey)
		if err != nil {
			slog.Warn("failed to decrypt profile secret", "name", s.ID, "error", err)
			continue
		}
		s.Value = plainValue
		result = append(result, s)
	}
	return result
}

// decryptECDH decrypts a base64-encoded payload: ephemeral_pub(65B) || iv(12B) || ciphertext.
// Uses ECDH P-256 key agreement + AES-GCM decryption.
func decryptECDH(encoded string, privKey *ecdh.PrivateKey) (string, error) {
	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	// P-256 uncompressed public key = 65 bytes, IV = 12 bytes
	if len(combined) < 65+12+1 {
		return "", err
	}

	ephPubBytes := combined[:65]
	iv := combined[65 : 65+12]
	ciphertext := combined[65+12:]

	// Import ephemeral public key
	ephPub, err := ecdh.P256().NewPublicKey(ephPubBytes)
	if err != nil {
		return "", err
	}

	// ECDH key agreement
	sharedSecret, err := privKey.ECDH(ephPub)
	if err != nil {
		return "", err
	}

	// Use shared secret directly as AES-256 key (it's 32 bytes from P-256)
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// Profile is delivered from the hub via /api/discover when this instance is
// attached to a hub profile. It is merged into the runtime config in memory
// only — nothing is written to SQLite.
//
// Providers use existing Provider fields; api_key_enc is stored opaquely in
// APIKey and forwarded to the relay which decrypts it with X-Hub-Secret.
type Profile struct {
	Providers  []Provider     `json:"providers,omitempty"`
	Secrets    []Secret       `json:"secrets,omitempty"`
	AppConfig  map[string]any `json:"app_config,omitempty"`
	Workspace  map[string]any `json:"workspace,omitempty"`
	LockConfig bool           `json:"lock_config,omitempty"`
}

// ApplyProfile merges the hub profile into the running instance in memory.
//
// Rules:
//   - Each provider gets InMemory=true so BeforeSave blocks local persistence.
//   - Each secret gets InMemory=true so BeforeSave blocks local persistence.
//   - Secrets are ECDH-decrypted using cfg.SecretPrivateKey before storing.
//   - When LockConfig=false, local policy fields (AppConfig/Workspace) are only
//     overwritten when the local value is the zero value — local config always wins.
//   - When LockConfig=true, profile values overwrite ALL local values (locked mode).
//   - The merged providers are stored in the Store's in-memory list and
//     prepended to any locally configured providers at runtime.
func ApplyProfile(store *Store, profile *Profile) {
	if profile == nil {
		return
	}
	cfg := store.cfg
	ws := store.ws
	// Mark every profile provider as in-memory so it can never be saved.
	providers := make([]*Provider, 0, len(profile.Providers))
	for i := range profile.Providers {
		p := profile.Providers[i] // copy
		p.InMemory = true
		providers = append(providers, &p)
	}

	// Decrypt ECDH-encrypted secrets using instance private key, then mark in-memory.
	decryptedSecrets := profile.Secrets
	if cfg != nil && cfg.SecretPrivateKey != "" {
		decryptedSecrets = DecryptProfileSecrets(profile.Secrets, cfg.SecretPrivateKey)
	}
	secrets := make([]*Secret, 0, len(decryptedSecrets))
	for i := range decryptedSecrets {
		s := decryptedSecrets[i] // copy
		s.InMemory = true
		secrets = append(secrets, &s)
	}

	store.SetProfile(&ProfileData{
		Providers: providers,
		Secrets:   secrets,
	})

	if profile.AppConfig != nil && cfg != nil {
		util.Patch(profile.AppConfig, cfg)
		cfg.Locked = profile.LockConfig
	}

	if profile.Workspace != nil && ws != nil {
		util.Patch(profile.Workspace, ws)
		ws.Locked = profile.LockConfig
	}
}
