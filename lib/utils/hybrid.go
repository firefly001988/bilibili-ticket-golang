package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
)

// HybridEncryptPKCS1v15 encrypts data using AES-128-CBC and encrypts the AES
// key with RSA PKCS#1 v1.5. The AES key is also used as the CBC IV, matching
// the hybrid format used by the Gaia WASM module.
func HybridEncryptPKCS1v15(data []byte, publicKeyPEM string, random io.Reader) (payloadBase64, keyBase64 string, err error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return "", "", errors.New("invalid RSA public key PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if pkcs1Key, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes); pkcs1Err == nil {
			parsed = pkcs1Key
		} else {
			return "", "", fmt.Errorf("parse RSA public key: %w", err)
		}
	}
	rsaKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", "", errors.New("public key is not RSA")
	}

	aesKey := make([]byte, aes.BlockSize)
	if _, err = io.ReadFull(random, aesKey); err != nil {
		return "", "", fmt.Errorf("generate AES key: %w", err)
	}
	aesBlock, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", "", err
	}
	padding := aes.BlockSize - len(data)%aes.BlockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(aesBlock, aesKey).CryptBlocks(ciphertext, padded)

	encryptedKey, err := rsa.EncryptPKCS1v15(random, rsaKey, aesKey)
	if err != nil {
		return "", "", fmt.Errorf("encrypt AES key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(encryptedKey), nil
}
