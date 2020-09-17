package multiserverkit

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"

	"golang.org/x/crypto/acme/autocert"
)

type encryptedAutocertCache struct {
	encryptionKey []byte
	underlying    autocert.Cache
}

func NewEncryptedAutocertCache(encryptionKey []byte, underlying autocert.Cache) autocert.Cache {
	if len(encryptionKey) != 32 {
		panic("encryptionkey must be exactly 32 bytes")
	}

	return &encryptedAutocertCache{
		encryptionKey: encryptionKey,
		underlying:    underlying,
	}
}

func (c *encryptedAutocertCache) Get(ctx context.Context, key string) ([]byte, error) {
	// get value from underlying cache
	v, err := c.underlying.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	// decrypt value gotten from underlying cache
	return decrypt(v, c.encryptionKey)
}

func (c *encryptedAutocertCache) Put(ctx context.Context, key string, data []byte) error {
	// encrypt the value
	encrypted, err := encrypt(data, c.encryptionKey)
	if err != nil {
		return err
	}

	// store the value in the underlying cache
	return c.underlying.Put(ctx, key, encrypted)
}

func (c *encryptedAutocertCache) Delete(ctx context.Context, key string) error {
	return c.underlying.Delete(ctx, key)
}

func encrypt(plaintext []byte, key []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(ciphertext []byte, key []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
