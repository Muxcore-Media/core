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
// SECURITY: Production deployments MUST register an EncryptionProvider.
// If no EncryptionProvider is registered, the fabric SHOULD refuse to
// start in production mode. Development and CI environments may operate
// without encryption. Use the Available() method to check at runtime.
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
	//
	// SECURITY: Implementations that do NOT support rotation (static-key
	// implementations) MUST return an error from this method. Production
	// deployments SHOULD use a provider that supports rotation.
	RotateKey(ctx context.Context) error

	// Available reports whether encryption is configured and operational.
	// Returns false if no provider is registered or the provider is in a
	// degraded state. The fabric SHOULD use this to enforce encryption
	// requirements at startup.
	Available() bool
}
