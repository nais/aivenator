package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"
	liberator_service "github.com/nais/liberator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/nais/liberator/pkg/strings"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Keys in secret
const (
	KafkaBrokers           = "KAFKA_BROKERS"
	KafkaSchemaRegistry    = "KAFKA_SCHEMA_REGISTRY"
	KafkaSchemaUser        = "KAFKA_SCHEMA_REGISTRY_USER"
	KafkaSchemaPassword    = "KAFKA_SCHEMA_REGISTRY_PASSWORD"
	KafkaCertificate       = "KAFKA_CERTIFICATE"
	KafkaPrivateKey        = "KAFKA_PRIVATE_KEY"
	KafkaCA                = "KAFKA_CA"
	KafkaCredStorePassword = "KAFKA_CREDSTORE_PASSWORD"
	KafkaSecretUpdated     = "KAFKA_SECRET_UPDATED"
	KafkaKeystore          = "client.keystore.p12"
	KafkaTruststore        = "client.truststore.jks"
)

// Annotations
const (
	ServiceUserAnnotation = "kafka.aiven.nais.io/serviceUser"
	PoolAnnotation        = "kafka.aiven.nais.io/pool"
)

func NewKafkaHandler(ctx context.Context, aiven *aiven.Client, projects []string, logger log.FieldLogger) KafkaHandler {
	generator := certificate.NewNativeGenerator()
	handler := KafkaHandler{
		project:      project.NewManager(aiven.CA),
		serviceuser:  serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:      service.NewManager(aiven.Services),
		generator:    generator,
		nameResolver: liberator_service.NewCachedNameResolver(aiven.Services),
		projects:     projects,
	}
	handler.StartUserCounter(ctx, logger)
	return handler
}

type KafkaHandler struct {
	project       project.ProjectManager
	serviceuser   serviceuser.ServiceUserManager
	service       service.ServiceManager
	generator     certificate.Generator
	nameResolver  liberator_service.NameResolver
	projects      []string
	secretHandler secret.Handler
}

func (h KafkaHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, sharedSecret *corev1.Secret, logger log.FieldLogger) ([]corev1.Secret, error) {
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
		"pool":    projectName,
		"service": serviceName,
	})

	if !strings.ContainsString(h.projects, projectName) {
		err := fmt.Errorf("pool %s is not allowed in this cluster: %w", projectName, utils.ErrUnrecoverable)
		utils.LocalFail("ValidatePool", application, err, logger)
		return nil, err
	}

	addresses, err := h.service.GetServiceAddresses(ctx, projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, false, logger)
	}

	finalSecret := sharedSecret
	if spec.SecretName != "" {
		logger = logger.WithField("secret_name", spec.SecretName)
		logger.Info("Creating individual secret for Kafka")
		finalSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      spec.SecretName,
				Namespace: application.GetNamespace(),
			},
		}
		_, err := h.secretHandler.ApplyIndividualSecret(ctx, application, finalSecret, logger)
		if err != nil {
			return nil, utils.AivenFail("GetOrInitSecret", application, err, false, logger)
		}
	} else {
		logger.Infof("Using shared secret %s ", sharedSecret.Name)
	}

	ca, err := h.project.GetCA(ctx, projectName)
	if err != nil {
		return nil, utils.AivenFail("GetCA", application, err, false, logger)
	}

	aivenUser, err := h.provideServiceUser(ctx, application, projectName, serviceName, finalSecret, logger)
	if err != nil {
		return nil, err
	}

	finalSecret.SetAnnotations(utils.MergeStringMap(finalSecret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		PoolAnnotation:        spec.Pool,
	}))
	logger.Infof("Created service user %s", aivenUser.Username)

	credStore, err := h.generator.MakeCredStores(aivenUser.AccessKey, aivenUser.AccessCert, ca)
	if err != nil {
		utils.LocalFail("CreateCredStores", application, err, logger)
		return nil, err
	}

	finalSecret.StringData = utils.MergeStringMap(finalSecret.StringData, map[string]string{
		KafkaCertificate:       aivenUser.AccessCert,
		KafkaPrivateKey:        aivenUser.AccessKey,
		KafkaBrokers:           addresses.ServiceURI,
		KafkaSchemaRegistry:    addresses.SchemaRegistry.URI,
		KafkaSchemaUser:        aivenUser.Username,
		KafkaSchemaPassword:    aivenUser.Password,
		KafkaCA:                ca,
		KafkaCredStorePassword: credStore.Secret,
		KafkaSecretUpdated:     time.Now().Format(time.RFC3339),
	})

	finalSecret.Data = utils.MergeByteMap(finalSecret.Data, map[string][]byte{
		KafkaKeystore:   credStore.Keystore,
		KafkaTruststore: credStore.Truststore,
	})

	controllerutil.AddFinalizer(finalSecret, constants.AivenatorFinalizer)

	if spec.SecretName != "" {
		return []corev1.Secret{*finalSecret}, nil
	}
	logger.Infof("Applied secret: %s", application.Spec.SecretName)

	return nil, nil
}

func (h KafkaHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, projectName string, serviceName string, secret *corev1.Secret, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	var aivenUser *aiven.ServiceUser
	var err error

	var serviceUserName string

	if nameFromAnnotation, ok := secret.GetAnnotations()[ServiceUserAnnotation]; ok {
		serviceUserName = nameFromAnnotation
	} else {
		suffix, err := utils.CreateSuffix(application)
		if err != nil {
			err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.ErrUnrecoverable)
			utils.LocalFail("CreateSuffix", application, err, logger)
			return nil, err
		}

		serviceUserName, err = kafka_nais_io_v1.ServiceUserNameWithSuffix(application.Namespace, application.Name, suffix)
		if err != nil {
			err = fmt.Errorf("unable to create service user name: %s %w", err, utils.ErrUnrecoverable)
			utils.LocalFail("ServiceUserNameWithSuffix", application, err, logger)
			return nil, err
		}
	}

	aivenUser, err = h.serviceuser.Get(ctx, serviceUserName, projectName, serviceName, logger)
	if err == nil {
		return aivenUser, nil
	}
	if !aiven.IsNotFound(err) {
		return nil, utils.AivenFail("GetServiceUser", application, err, false, logger)
	}

	aivenUser, err = h.serviceuser.Create(ctx, serviceUserName, projectName, serviceName, nil, logger)
	if err != nil {
		return nil, utils.AivenFail("CreateServiceUser", application, err, false, logger)
	}
	return aivenUser, nil
}

func (h KafkaHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()
	if serviceUserName, okServiceUser := annotations[ServiceUserAnnotation]; okServiceUser {
		if projectName, okPool := annotations[PoolAnnotation]; okPool {
			serviceName, err := h.nameResolver.ResolveKafkaServiceName(ctx, projectName)
			if err != nil {
				return err
			}
			logger = logger.WithFields(log.Fields{
				"pool":    projectName,
				"service": serviceName,
			})
			err = h.serviceuser.Delete(ctx, serviceUserName, projectName, serviceName, logger)
			if err != nil {
				if aiven.IsNotFound(err) {
					logger.Infof("Service user %s does not exist", serviceUserName)
					return nil
				}
				return err
			}
			logger.Infof("Deleted service user %s", serviceUserName)
		} else {
			return fmt.Errorf("missing pool annotation on secret %s in namespace %s, unable to delete service user %s",
				secret.GetName(), secret.GetNamespace(), serviceUserName)
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
					logger.Warnf("unable to count service users for pool %s: %v", prj, err)
					continue
				}
				h.serviceuser.ObserveServiceUsersCount(ctx, prj, serviceName, logger)
			}
		}
	}
}
