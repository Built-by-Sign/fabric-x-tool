bccsp:
  default: PKCS11
  pkcs11:
    Library: ./libkms_pkcs11.so
    Label: ${KMS_TOKEN_LABEL}
    Pin: ${KMS_USER_PIN}
    Hash: SHA2
    Security: 256