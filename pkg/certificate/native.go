package certificate

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"software.sslmate.com/src/go-pkcs12"
)

type Native struct {
	secret string
}

func NewNativeGenerator() Native {
	n := Native{
		secret: GetSecret(),
	}
	return n
}

func (n Native) MakeCredStores(accessKey, accessCert, caCert string) (*CredStoreData, error) {
	keystore, err := n.makeKeyStore(accessKey, accessCert)
	if err != nil {
		return nil, err
	}

	truststore, err := n.makeTrustStore(caCert)
	if err != nil {
		return nil, err
	}

	return &CredStoreData{
		Keystore:   keystore,
		Truststore: truststore,
		Secret:     n.secret,
	}, nil
}

func (n Native) makeTrustStore(caCert string) ([]byte, error) {
	cert, err := parseCertificate(caCert)
	if err != nil {
		return nil, err
	}
	certs := []pkcs12.TrustStoreEntry{
		{
			Cert:         cert,
			FriendlyName: "ca",
		},
	}

	data, err := pkcs12.LegacyRC2.EncodeTrustStoreEntries(certs, n.secret)
	if err != nil {
		return nil, fmt.Errorf("failed to encode truststore: %v", err)
	}

	return data, nil
}

func (n Native) makeKeyStore(accessKey string, accessCert string) ([]byte, error) {
	cert, err := parseCertificate(accessCert)
	if err != nil {
		return nil, err
	}

	privateKey, err := parsePrivateKey(accessKey)
	if err != nil {
		return nil, err
	}

	pfxData, err := pkcs12.LegacyRC2.Encode(privateKey, cert, nil, n.secret)
	if err != nil {
		return nil, fmt.Errorf("failed to encode pkcs12 keystore: %v", err)
	}

	return pfxData, nil
}

func parseCertificate(rawCert string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(rawCert))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("failed to decode PEM block containing certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %v", err)
	}
	return cert, nil
}

func parsePrivateKey(rawKey string) (any, error) {
	block, _ := pem.Decode([]byte(rawKey))
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing certificate")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}
	return privateKey, nil
}
