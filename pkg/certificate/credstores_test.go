//go:build integration

package certificate

import (
	"encoding/base64"
	"fmt"
	"github.com/aiven/aiven-go-client"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"testing"
)

const (
	project  = "nav-integration-test"
	service  = "nav-integration-test-kafka"
	username = "test_user"
)

func TestCredStoreGenerator(t *testing.T) {
	log.Error("Starting test")
	client, err := aiven.NewTokenClient(os.Getenv("AIVEN_TOKEN"), "")
	if err != nil {
		log.Errorf("failed to create client: %v", err)
		t.Fatal(err)
	}

	test_user, err := client.ServiceUsers.Get(project, service, username)
	if err != nil {
		log.Errorf("failed to get service user: %v", err)
		req := aiven.CreateServiceUserRequest{Username: username}
		test_user, err = client.ServiceUsers.Create(project, service, req)
		if err != nil {
			log.Errorf("failed to create service user: %v", err)
			t.Fatal(err)
		}
	}

	caCert, err := client.CA.Get(project)
	if err != nil {
		log.Errorf("failed to get CA cert: %v", err)
		t.Fatal(err)
	}

	workdir, err := os.MkdirTemp("", "credstores_test-workdir-*")
	runGenerator := func(t *testing.T, desc string, generator Generator) {
		stores, err := generator.MakeCredStores(test_user.AccessKey, test_user.AccessCert, caCert)
		if err != nil {
			log.Errorf("failed to create cred stores: %v", err)
			t.Fatal(err)
		}

		log.Infof("Generated CredStores using %s:", desc)
		log.Infof("Secret: '%v'", stores.Secret)
		truststorePath := path.Join(workdir, fmt.Sprintf("%s.client.truststore.jks", desc))
		_ = os.WriteFile(truststorePath, stores.Truststore, 0644)
		log.Infof("Truststore (saved to %s):", truststorePath)
		log.Info(base64.StdEncoding.EncodeToString(stores.Truststore))
		keystorePath := path.Join(workdir, fmt.Sprintf("%s.client.keystore.p12", desc))
		_ = os.WriteFile(keystorePath, stores.Keystore, 0644)
		log.Infof("Keystore (saved to %s):", keystorePath)
		log.Info(base64.StdEncoding.EncodeToString(stores.Keystore))
	}

	t.Run("native", func(t *testing.T) {
		runGenerator(t, "native", NewNativeGenerator())
	})
}
