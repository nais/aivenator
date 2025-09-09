//go:build integration

package certificate

import (
	"encoding/base64"
	"os"
	"path"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	log "github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	project  = "dev-nais-dev"
	service  = "kafka"
	username = "test_user"
)

func TestCredStoreGenerator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CredStoreGenerator Suite")
}

var _ = Describe("CredStoreGenerator", func() {
	var client *aiven.Client
	var serviceUser *aiven.ServiceUser
	var caCert string
	var workdir string

	var err error

	BeforeEach(func(ctx SpecContext) {
		client, err = aiven.NewTokenClient(os.Getenv("AIVEN_TOKEN"), "")
		Expect(err).ToNot(HaveOccurred())

		log.Infof("Attempting to get service user %s in project %s, service %s", username, project, service)
		serviceUser, err = client.ServiceUsers.Get(ctx, project, service, username)
		Expect(aiven.IsNotFound(err)).To(BeTrueBecause("service user should not exist"))

		log.Infof("Attempting to create service user %s in project %s, service %s", username, project, service)
		req := aiven.CreateServiceUserRequest{Username: username}
		serviceUser, err = client.ServiceUsers.Create(ctx, project, service, req)
		Expect(err).ToNot(HaveOccurred())

		caCert, err = client.CA.Get(ctx, project)

		workdir, err = os.MkdirTemp("", "credstores_test-workdir-*")
	}, NodeTimeout(10*time.Second))

	AfterEach(func(ctx SpecContext) {
		err = client.ServiceUsers.Delete(ctx, project, service, username)
		Expect(err).ToNot(HaveOccurred())
	}, NodeTimeout(10*time.Second))

	It("should generate a native cred store", func(ctx SpecContext) {
		generator := NewNativeGenerator()
		stores, err := generator.MakeCredStores(serviceUser.AccessKey, serviceUser.AccessCert, caCert)
		Expect(err).ToNot(HaveOccurred())

		log.Infof("Generated CredStores using NativeGenerator")
		log.Infof("Secret: '%v'", stores.Secret)
		truststorePath := path.Join(workdir, "native.client.truststore.jks")
		_ = os.WriteFile(truststorePath, stores.Truststore, 0o644)
		log.Infof("Truststore (saved to %s):", truststorePath)
		log.Info(base64.StdEncoding.EncodeToString(stores.Truststore))
		keystorePath := path.Join(workdir, "native.client.keystore.p12")
		_ = os.WriteFile(keystorePath, stores.Keystore, 0o644)
		log.Infof("Keystore (saved to %s):", keystorePath)
		log.Info(base64.StdEncoding.EncodeToString(stores.Keystore))
	}, NodeTimeout(10*time.Second))
})
