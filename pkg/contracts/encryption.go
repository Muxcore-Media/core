package contracts

import "context"

// EncryptionProvider handles envelope encryption for sensitive data at rest.
// Modules call Encrypt before storing sensitive data and Decrypt after
// retrieving it. The provider manages key material, rotation, and versioning
// internally — modules never touch raw keys.
//
// The ciphertext produced by Encrypt is self-describing: it carries key
// metadata so Decrypt can identify which key was used, even after rotation.
//
// This contract is provider-agnostic: a module that calls Encrypt/Decrypt
// works identically whether the backend uses AES-GCM with local keys,
// a cloud KMS (AWS KMS, GCP KMS), HashiCorp Vault transit engine, or
// a future encryption service. The security posture is determined by
// the provider implementation, not by module code.
//
// If no EncryptionProvider is registered, modules must handle encryption
// themselves or operate without it. This is acceptable for development
// but not for production.
type EncryptionProvider interface {
	// Encrypt encrypts plaintext and returns self-describing ciphertext.
	// The returned bytes include key version metadata so Decrypt can
	// identify the correct key even after rotation.
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)

	// Decrypt decrypts ciphertext that was produced by Encrypt.
	// The provider reads key metadata from the ciphertext to select
	// the correct decryption key, including keys from previous rotations.
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)

	// RotateKey initiates key rotation. After rotation, new Encrypt calls
	// use the new key. Old keys remain available for Decrypt indefinitely —
	// previously encrypted data does not need re-encryption.
	// Returns an error if the provider does not support rotation (e.g.,
	// static-key implementations).
	RotateKey(ctx context.Context) error
}
