package utils

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestHybridEncryptPKCS1v15RoundTrip(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	plaintext := []byte(`{"99b0":"ua","6365":"pc","8b9b":""}`)

	// Non-zero deterministic bytes satisfy both AES-key generation and RSA
	// PKCS#1 v1.5 padding while keeping the envelope reproducible.
	randomBytes := bytes.NewReader(bytes.Repeat([]byte{0x5a}, 4096))
	payloadBase64, keyBase64, err := HybridEncryptPKCS1v15(plaintext, string(publicPEM), randomBytes)
	if err != nil {
		t.Fatal(err)
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		t.Fatal(err)
	}
	aesKey, err := rsa.DecryptPKCS1v15(nil, privateKey, encryptedKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(aesKey) != aes.BlockSize {
		t.Fatalf("AES key length = %d, want %d", len(aesKey), aes.BlockSize)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(payloadBase64)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatal(err)
	}
	decrypted := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, aesKey).CryptBlocks(decrypted, ciphertext)
	padding := int(decrypted[len(decrypted)-1])
	decrypted = decrypted[:len(decrypted)-padding]
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted payload = %q, want %q", decrypted, plaintext)
	}
}
