package bccsp

// BCCSPConfig represents the BCCSP (Blockchain Crypto Service Provider) configuration
// This structure matches the BCCSP configuration format used in Fabric and Token SDK
type BCCSPConfig struct {
	// Default specifies the default crypto provider (SW, PKCS11)
	Default string `yaml:"default"`

	// SW contains software-based crypto provider configuration
	SW *SWConfig `yaml:"sw,omitempty"`

	// PKCS11 contains PKCS#11 HSM/KMS provider configuration
	PKCS11 *PKCS11Config `yaml:"pkcs11,omitempty"`
}

// SWConfig represents software-based crypto provider configuration
type SWConfig struct {
	// Hash specifies the hash algorithm (SHA2, SHA3)
	Hash string `yaml:"hash"`

	// Security specifies the security level (256, 384)
	Security int `yaml:"security"`
}

// PKCS11Config represents PKCS#11 HSM/KMS provider configuration
type PKCS11Config struct {
	// Library is the path to the PKCS#11 library (.so file)
	Library string `yaml:"library"`

	// Label is the token label to use
	Label string `yaml:"label"`

	// Pin is the user PIN for accessing the token
	Pin string `yaml:"pin"`

	// Hash specifies the hash algorithm (SHA2, SHA3)
	Hash string `yaml:"hash"`

	// Security specifies the security level (256, 384)
	Security int `yaml:"security"`

	// SoftwareVerify enables software verification (optional)
	SoftwareVerify bool `yaml:"softwareverify,omitempty"`

	// Immutable indicates if keys are immutable (optional)
	Immutable bool `yaml:"immutable,omitempty"`
}

// GenerateKMSConfig generates a BCCSP configuration for KMS mode (remote PKCS11)
// Parameters:
//   - endpoint: KMS service endpoint (not used in BCCSP config, but for context)
//   - tokenLabel: Token label to use in KMS
//   - pin: User PIN for accessing the token
//
// Returns: BCCSPConfig configured for KMS access via PKCS11
func GenerateKMSConfig(endpoint, tokenLabel, pin string) *BCCSPConfig {
	return &BCCSPConfig{
		Default: "PKCS11",
		PKCS11: &PKCS11Config{
			Library:  "/usr/local/lib/libkms_pkcs11.so", // KMS PKCS11 library path
			Label:    tokenLabel,
			Pin:      pin,
			Hash:     "SHA2",
			Security: 256,
		},
	}
}

// GenerateSoftwareConfig generates a BCCSP configuration for software mode
// Returns: BCCSPConfig configured for software-based crypto
func GenerateSoftwareConfig() *BCCSPConfig {
	return &BCCSPConfig{
		Default: "SW",
		SW: &SWConfig{
			Hash:     "SHA2",
			Security: 256,
		},
	}
}
