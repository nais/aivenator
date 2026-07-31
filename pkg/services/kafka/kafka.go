package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/utils"
	liberator_service "github.com/nais/liberator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/nais/liberator/pkg/strings"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Keys in secret
const (
	KafkaBrokers           = "KAFKA_BROKERS"
	KafkaCA                = "KAFKA_CA"
	KafkaCertificate       = "KAFKA_CERTIFICATE"
	KafkaCredStorePassword = "KAFKA_CREDSTORE_PASSWORD"
	KafkaKeystore          = "client.keystore.p12"
	KafkaPrivateKey        = "KAFKA_PRIVATE_KEY"
	KafkaSchemaPassword    = "KAFKA_SCHEMA_REGISTRY_PASSWORD"
	KafkaSchemaRegistry    = "KAFKA_SCHEMA_REGISTRY"
	KafkaSchemaUser        = "KAFKA_SCHEMA_REGISTRY_USER"
	KafkaSecretUpdated     = "KAFKA_SECRET_UPDATED"
	KafkaTruststore        = "client.truststore.jks"
)

const (
	InstanceAnnotation          = "kafka.aiven.nais.io/instance"
	PoolAnnotation              = "kafka.aiven.nais.io/pool"
	ServiceUserAnnotation       = "kafka.aiven.nais.io/serviceUser"
	LegacyServiceUserAnnotation = "kafka.aiven.nais.io/legacyServiceUser"
)

func NewKafkaHandler(ctx context.Context, aiven *aiven.Client, projects []string, projectName string, logger log.FieldLogger, k8sReader client.Reader, crServiceUser operator.ServiceUserManager) KafkaHandler {
	generator := certificate.NewNativeGenerator()
	handler := KafkaHandler{
		crServiceUser: crServiceUser,
		generator:     generator,
		k8sReader:     k8sReader,
		nameResolver:  liberator_service.NewCachedNameResolver(aiven.Services),
		project:       project.NewManager(aiven.CA),
		projects:      projects,
		secretConfig:  utils.NewSecretConfig(aiven, projectName),
		service:       service.NewManager(aiven.Services),
		serviceuser:   serviceuser.NewManager(ctx, aiven.ServiceUsers),
	}
	handler.StartUserCounter(ctx, logger)
	return handler
}

type KafkaHandler struct {
	crServiceUser operator.ServiceUserManager
	generator     certificate.Generator
	k8sReader     client.Reader
	nameResolver  liberator_service.NameResolver
	project       project.ProjectManager
	projects      []string
	secretConfig  utils.SecretConfig
	service       service.ServiceManager
	serviceuser   serviceuser.ServiceUserManager
}

func (h KafkaHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]corev1.Secret, error) {
	spec := application.Spec.Kafka
	if spec == nil {
		return nil, nil
	}

	projectName := spec.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil, nil
	}

	serviceName, err := h.nameResolver.ResolveKafkaServiceName(ctx, spec.Pool)
	if err != nil {
		return nil, utils.AivenFail("ResolveServiceName", application, err, false, logger)
	}

	logger = logger.WithFields(log.Fields{
		"aivenProject":         projectName,
		"aivenServiceInstance": spec.Pool,
		"pool":                 projectName,
		"serviceName":          serviceName,
	})

	if !strings.ContainsString(h.projects, projectName) {
		err := fmt.Errorf("pool %s is not allowed in this cluster: %w", projectName, utils.ErrUnrecoverable)
		utils.LocalFail("ValidatePool", application, err, logger)
		return nil, err
	}

	addresses, err := h.service.GetServiceAddressesFromCache(ctx, projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, false, logger)
	}

	// Fetch CA before attempting to create any secrets so tests fail on CA errors, not name validation
	ca, err := h.project.GetCA(ctx, projectName)
	if err != nil {
		return nil, utils.AivenFail("GetCA", application, err, false, logger)
	}

	// Only manage individual secret when a name is provided
	var individualSecret *corev1.Secret
	logger = logger.WithField("secretName", spec.SecretName)
	logger.Info("Creating individual secret for Kafka")
	individualSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.SecretName,
			Namespace: application.GetNamespace(),
		},
	}
	if _, err := h.secretConfig.ApplyIndividualSecret(ctx, application, individualSecret, logger); err != nil {
		return nil, utils.AivenFail("GetOrInitSecret", application, err, false, logger)
	}

	aivenUser, crName, legacyUsername, createdFresh, err := h.provideServiceUser(ctx, application, projectName, serviceName, logger)
	if err != nil {
		return nil, err
	}
	logger = logger.WithField("serviceUser", aivenUser.Username)
	annotations := map[string]string{
		InstanceAnnotation:    spec.Pool,
		PoolAnnotation:        spec.Pool,
		ServiceUserAnnotation: crName,
	}
	individualSecret.SetAnnotations(utils.MergeStringMap(individualSecret.GetAnnotations(), annotations))
	logger.WithField(utils.FieldInvariant, "Provided service user").Infof("Provided service user %s", aivenUser.Username)

	accessCert, err := operator.Required(aivenUser.Secret, operator.ServiceUserAccessCert)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	accessKey, err := operator.Required(aivenUser.Secret, operator.ServiceUserAccessKey)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	password, err := operator.Required(aivenUser.Secret, operator.ServiceUserPassword)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}

	credStore, err := h.generator.MakeCredStores(accessKey, accessCert, ca)
	if err != nil {
		utils.LocalFail("CreateCredStores", application, err, logger)
		if createdFresh {
			// Roll back only a user this reconcile created; adopted/migrated ones are live.
			return nil, errors.Join(err, h.Cleanup(ctx, individualSecret, logger))
		}
		return nil, err
	}

	// Recorded after MakeCredStores so a rolled-back reconcile can't drain a pre-CR user pods still use.
	if legacyUsername != "" {
		individualSecret.SetAnnotations(utils.MergeStringMap(individualSecret.GetAnnotations(), map[string]string{
			LegacyServiceUserAnnotation: legacyUsername,
		}))
	}

	individualSecret.StringData = utils.MergeStringMap(individualSecret.StringData, map[string]string{
		KafkaBrokers:           addresses.Kafka().URI,
		KafkaCA:                ca,
		KafkaCertificate:       accessCert,
		KafkaCredStorePassword: credStore.Secret,
		KafkaPrivateKey:        accessKey,
		KafkaSchemaPassword:    password,
		KafkaSchemaRegistry:    addresses.SchemaRegistry().URI,
		KafkaSchemaUser:        aivenUser.Username,
		KafkaSecretUpdated:     time.Now().Format(time.RFC3339),
	})

	individualSecret.Data = utils.MergeByteMap(individualSecret.Data, map[string][]byte{
		KafkaKeystore:   credStore.Keystore,
		KafkaTruststore: credStore.Truststore,
	})

	controllerutil.AddFinalizer(individualSecret, constants.AivenatorFinalizer)
	logger.Infof("Applied individualSecret")
	return []corev1.Secret{*individualSecret}, nil
}

// The CR name and spec.username are frozen per secret.
func (h KafkaHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, projectName, serviceName string, logger log.FieldLogger) (*operator.ServiceUser, string, string, bool, error) {
	namespace := application.GetNamespace()

	existingName, existingLegacy := "", ""
	existing := &corev1.Secret{}
	if err := h.k8sReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: application.Spec.Kafka.SecretName}, existing); err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, "", "", false, utils.AivenFail("GetSecret", application, err, false, logger)
		}
	} else {
		existingName = existing.GetAnnotations()[ServiceUserAnnotation]
		existingLegacy = existing.GetAnnotations()[LegacyServiceUserAnnotation]
	}

	crName, legacyUsername, err := operator.ResolveExistingServiceUser(ctx, h.crServiceUser, namespace, existingName, existingLegacy, serviceName)
	if err != nil {
		return nil, "", "", false, utils.AivenFail("ResolveServiceUser", application, err, false, logger)
	}

	// A minted (non-adopted) name means this call is creating a brand-new ServiceUser
	// CR, safe to roll back on a later failure this reconcile; an adopted name — from
	// an existing secret or an existing CR — is live and must never be deleted.
	createdFresh := crName == ""

	// spec.username is immutable and carried by the CR; mint it only for a new CR.
	// On adopt it stays empty so the operator wrapper preserves the CR's value.
	username := ""
	if createdFresh {
		crName = utils.ServiceUserName(application.GetName(), "", application.Spec.Kafka.Pool, application.Spec.Kafka.SecretName, time.Now())
		if username, err = h.serviceUserName(application, logger); err != nil {
			return nil, "", "", false, err
		}
	}

	aivenUser, err := h.crServiceUser.CreateServiceUser(ctx, application, operator.ServiceUserSpec{
		Name:        crName,
		Namespace:   namespace,
		Project:     projectName,
		ServiceName: serviceName,
		Username:    username,
	}, logger)
	if err != nil {
		return nil, "", "", false, utils.AivenFail("EnsureServiceUser", application, err, false, logger)
	}
	return aivenUser, crName, legacyUsername, createdFresh, nil
}

func (h KafkaHandler) serviceUserName(application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) (string, error) {
	suffix, err := utils.CreateSuffix(application)
	if err != nil {
		err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.ErrUnrecoverable)
		utils.LocalFail("CreateSuffix", application, err, logger)
		return "", err
	}
	serviceUserName, err := kafka_nais_io_v1.ServiceUserNameWithSuffix(application.Namespace, application.Name, suffix)
	if err != nil {
		err = fmt.Errorf("unable to create service user name: %s %w", err, utils.ErrUnrecoverable)
		utils.LocalFail("ServiceUserNameWithSuffix", application, err, logger)
		return "", err
	}
	return serviceUserName, nil
}

func (h KafkaHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()
	serviceUserName, okServiceUser := annotations[ServiceUserAnnotation]
	if !okServiceUser {
		return nil
	}

	projectName, okPool := annotations[PoolAnnotation]
	if !okPool {
		return fmt.Errorf("missing pool annotation on secret %s in namespace %s, unable to delete service user %s",
			secret.GetName(), secret.GetNamespace(), serviceUserName)
	}

	serviceName, err := h.nameResolver.ResolveKafkaServiceName(ctx, projectName)
	if err != nil {
		return err
	}

	logger = logger.WithFields(log.Fields{
		"aivenProject":         projectName,
		"serviceName":          serviceName,
		"serviceUser":          serviceUserName,
		"aivenServiceInstance": annotations[InstanceAnnotation],
	})

	crName, directTarget := serviceUserName, ""
	if !utils.IsValidCRName(serviceUserName) {
		crName, directTarget = "", serviceUserName
	}
	if err := operator.DrainServiceUser(ctx, h.crServiceUser, h.serviceuser, secret.GetNamespace(), crName, directTarget, projectName, serviceName, logger); err != nil {
		return err
	}

	if legacyUsername, ok := annotations[LegacyServiceUserAnnotation]; ok {
		if err := serviceuser.EnsureServiceUserDeleted(ctx, h.serviceuser, "legacy service user", legacyUsername, projectName, serviceName, logger); err != nil {
			return err
		}
	}

	return nil
}

func (h *KafkaHandler) StartUserCounter(ctx context.Context, logger log.FieldLogger) {
	go h.countUsers(ctx, logger)
}

func (h *KafkaHandler) countUsers(ctx context.Context, logger log.FieldLogger) {
	ticker := time.NewTicker(h.serviceuser.GetCacheExpiration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, prj := range h.projects {
				serviceName, err := h.nameResolver.ResolveKafkaServiceName(ctx, prj)
				if err != nil {
					logger.WithField(utils.FieldInvariant, "unable to count service users for pool").Warnf("unable to count service users for pool %s: %v", prj, err)
					continue
				}
				h.serviceuser.ObserveServiceUsersCount(ctx, prj, serviceName, logger)
			}
		}
	}
}
