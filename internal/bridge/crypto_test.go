package bridge

import (
	"testing"

	"github.com/dop251/goja"
)

func TestCryptoCreateHash_SHA256(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.createHash("sha256").update("hello").digest("hex");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if val.String() != expected {
		t.Errorf("SHA256('hello') = %q, want %q", val.String(), expected)
	}
}

func TestCryptoCreateHash_SHA256_Base64(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.createHash("sha256").update("hello").digest("base64");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="
	if val.String() != expected {
		t.Errorf("SHA256('hello') base64 = %q, want %q", val.String(), expected)
	}
}

func TestCryptoCreateHash_SHA512(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.createHash("sha512").update("hello").digest("hex");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"
	if val.String() != expected {
		t.Errorf("SHA512('hello') = %q, want %q", val.String(), expected)
	}
}

func TestCryptoCreateHmac_SHA256(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.createHmac("sha256", "secret").update("hello").digest("hex");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "88aab3ede8d3adf94d26ab90d3bafd4a2083070c3bcce9c014ee04a443847c0b"
	if val.String() != expected {
		t.Errorf("HMAC-SHA256 = %q, want %q", val.String(), expected)
	}
}

func TestCryptoCreateHmac_SHA512_KrakenVector(t *testing.T) {
	// Test vector from Kraken API documentation:
	// https://docs.kraken.com/api/docs/guides/spot-rest-auth
	//
	// The Kraken signing algorithm:
	// 1. SHA256(nonce + postData) → binary hash
	// 2. HMAC-SHA512(urlPath + sha256Binary, base64Decode(secret)) → base64 signature
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		var apiSecret = "kQH5HW/8p1uGOVjbgWA7FunAmGO8lsSUXNsu3eow76sz84Q18fWxnyRzBHCd3pd5nE9qa99HAZtuZuj6F1huXg==";
		var nonce = "1616492376594";
		var postData = "nonce=1616492376594&ordertype=limit&pair=XBTUSD&price=37500&type=buy&volume=1.25";
		var urlPath = "/0/private/AddOrder";

		// Step 1: SHA256(nonce + postData) → binary
		var sha256Hash = crypto.createHash("sha256").update(nonce + postData).digest("binary");

		// Step 2: base64 decode the secret key
		var secretBytes = crypto.base64Decode(apiSecret);

		// Step 3: HMAC-SHA512(urlPath + sha256Hash, secretBytes) → base64
		var signature = crypto.createHmac("sha512", secretBytes).update(urlPath + sha256Hash, "binary").digest("base64");
		signature;
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := val.String()
	if result == "" {
		t.Fatal("expected non-empty signature")
	}

	// Expected signature from Kraken docs for this exact test vector
	expected := "4/dpxb3iT4tp/ZCVEwSnEsLxx0bqyhLpdfOpc6fn7OR8+UClSV5n9E6aSS8MPtnRfp32bAb0nmbRn6H8ndwLUQ=="
	if result != expected {
		t.Errorf("Kraken signature = %q, want %q", result, expected)
	}
}

func TestCryptoRandomBytes(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.randomBytes(16);
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := val.String()
	if len(result) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("randomBytes(16) returned %d chars, want 32", len(result))
	}
}

func TestCryptoBase64Encode(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.base64Encode("hello world");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "aGVsbG8gd29ybGQ="
	if val.String() != expected {
		t.Errorf("base64Encode('hello world') = %q, want %q", val.String(), expected)
	}
}

func TestCryptoBase64Decode(t *testing.T) {
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.base64Decode("aGVsbG8gd29ybGQ=");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "hello world"
	if val.String() != expected {
		t.Errorf("base64Decode = %q, want %q", val.String(), expected)
	}
}

func TestCryptoCreateHash_Chaining(t *testing.T) {
	// Verify .update() returns the hash for chaining (Node.js compat)
	vm := goja.New()
	RegisterCrypto(vm, nil)

	val, err := vm.RunString(`
		crypto.createHash("sha256").update("he").update("llo").digest("hex");
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if val.String() != expected {
		t.Errorf("chained SHA256('he'+'llo') = %q, want %q", val.String(), expected)
	}
}
