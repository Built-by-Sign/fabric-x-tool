/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package namespace

import (
	"time"

	"github.com/hyperledger/fabric-x-common/cmd/common/comm"

	"github.com/hyperledger/fabric-x-committer/service/verifier/policy"
)

// DefaultTimeout for gRPC connections.
const DefaultTimeout = 3 * time.Second

// OrdererConfig is a helper struct to deal with orderer-related arguments.
type OrdererConfig struct {
	OrderingEndpoint string
	Config           comm.Config
}

// MSPConfig is a helper struct to deal with MSP-related arguments.
type MSPConfig struct {
	MSPConfigPath string
	MSPID         string
	BCCSPConfig   *BCCSPConfig // Optional BCCSP configuration for PKCS11/KMS support
}

// BCCSPConfig represents the BCCSP (Blockchain Crypto Service Provider) configuration
type BCCSPConfig struct {
	// Default specifies the default crypto provider (SW, PKCS11)
	Default string

	// SW contains software-based crypto provider configuration
	SW *SWConfig

	// PKCS11 contains PKCS#11 HSM/KMS provider configuration
	PKCS11 *PKCS11Config
}

// SWConfig represents software-based crypto provider configuration
type SWConfig struct {
	// Hash specifies the hash algorithm (SHA2, SHA3)
	Hash string

	// Security specifies the security level (256, 384)
	Security int
}

// PKCS11Config represents PKCS#11 HSM/KMS provider configuration
type PKCS11Config struct {
	// Library is the path to the PKCS#11 library (.so file)
	Library string

	// Label is the token label to use
	Label string

	// Pin is the user PIN for accessing the token
	Pin string

	// Hash specifies the hash algorithm (SHA2, SHA3)
	Hash string

	// Security specifies the security level (256, 384)
	Security int

	// SoftwareVerify enables software verification (optional)
	SoftwareVerify bool

	// Immutable indicates if keys are immutable (optional)
	Immutable bool
}

// NsConfig is a helper struct to deal with namespace related arguments.
type NsConfig struct {
	Channel             string
	NamespaceID         string
	Version             int
	VerificationKeyPath string
}

func validateConfig(nsCfg NsConfig) error {
	return policy.ValidateNamespaceID(nsCfg.NamespaceID)
}
