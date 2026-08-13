package operator

import (
	"context"
	"fmt"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	log "github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type OpenSearchACLSpec struct {
	Project     string
	ServiceName string
	Namespace   string
	Username    string
	Access      string
}

// OpenSearchACLManager manages one service user's entry in the per-service
// OpenSearchACLConfig CR. The CR is authoritative for the service's complete
// ACL list: aiven-operator removes any entry missing from it and reverts
// out-of-band API edits, so after the CR exists every ACL change must go
// through it.
type OpenSearchACLManager interface {
	// CreateServiceUserACLs is an idempotent upsert, not a strict create: the
	// CR is shared by all of the service's users and reconciles re-assert the
	// entry every cycle.
	CreateServiceUserACLs(ctx context.Context, instance client.Object, spec OpenSearchACLSpec, logger log.FieldLogger) error
	DeleteServiceUserACLs(ctx context.Context, namespace, serviceName, username string, logger log.FieldLogger) error
}

type OpenSearchACLConfigManager struct {
	client     client.Client
	liveConfig opensearch.ACLManager
}

func NewOpenSearchACLManager(client client.Client, liveConfig opensearch.ACLManager) OpenSearchACLManager {
	return &OpenSearchACLConfigManager{client: client, liveConfig: liveConfig}
}

func (m *OpenSearchACLConfigManager) CreateServiceUserACLs(ctx context.Context, instance client.Object, spec OpenSearchACLSpec, logger log.FieldLogger) error {
	// The CR is shared by every app on the service, so concurrent reconciles
	// of different apps race to update it; retry on conflict instead of
	// failing the whole reconcile on a stale resourceVersion.
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		aclConfig := &aiven_io_v1alpha1.OpenSearchACLConfig{
			ObjectMeta: metav1.ObjectMeta{Name: spec.ServiceName, Namespace: spec.Namespace},
		}
		_, err := controllerutil.CreateOrUpdate(ctx, m.client, aclConfig, func() error {
			if aclConfig.GetResourceVersion() == "" {
				if err := m.seed(ctx, aclConfig, spec); err != nil {
					return err
				}
			}
			// Owned by the OpenSearch instance CR only: the config is shared by
			// every app on the service, so it must outlive any single app and is
			// garbage-collected with the instance.
			if err := controllerutil.SetOwnerReference(instance, aclConfig, m.client.Scheme()); err != nil {
				return err
			}
			aclConfig.SetLabels(utils.MergeStringMap(aclConfig.GetLabels(), map[string]string{
				"team": spec.Namespace,
			}))
			aclConfig.Spec.Project = spec.Project
			aclConfig.Spec.ServiceName = spec.ServiceName
			aclConfig.Spec.Enabled = true
			upsertACLEntry(&aclConfig.Spec, spec.Username, spec.Access)
			return nil
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("ensuring ACL entry in OpenSearchACLConfig %s/%s: %w", spec.Namespace, spec.ServiceName, err)
	}
	logger.Infof("ensured ACL entry for %s in OpenSearchACLConfig %s/%s", spec.Username, spec.Namespace, spec.ServiceName)
	return nil
}

// DeleteServiceUserACLs removes the user's entry but never deletes the CR
// itself: aiven-operator reacts to CR deletion by disabling ACLs on the
// service entirely, giving every authenticated user unrestricted access. The
// CR is garbage-collected with the OpenSearch instance via its owner reference.
func (m *OpenSearchACLConfigManager) DeleteServiceUserACLs(ctx context.Context, namespace, serviceName, username string, logger log.FieldLogger) error {
	aclConfig := &aiven_io_v1alpha1.OpenSearchACLConfig{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, aclConfig); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting OpenSearchACLConfig %s/%s: %w", namespace, serviceName, err)
	}

	kept := make([]aiven_io_v1alpha1.OpenSearchACLConfigACL, 0, len(aclConfig.Spec.Acls))
	for _, acl := range aclConfig.Spec.Acls {
		if acl.Username != username {
			kept = append(kept, acl)
		}
	}
	if len(kept) == len(aclConfig.Spec.Acls) {
		return nil
	}

	aclConfig.Spec.Acls = kept
	if err := m.client.Update(ctx, aclConfig); err != nil {
		return fmt.Errorf("removing ACL entry from OpenSearchACLConfig %s/%s: %w", namespace, serviceName, err)
	}
	logger.Infof("removed ACL entry for %s from OpenSearchACLConfig %s/%s", username, namespace, serviceName)
	return nil
}

// seed copies the service's current live ACL list into a CR that is about to
// be created. Without this, the operator's first reconcile would delete the
// ACL entries of every user provisioned before the CR existed (the CR is the
// complete list). Transitional, like the opensearch.ACLManager it reads from.
func (m *OpenSearchACLConfigManager) seed(ctx context.Context, aclConfig *aiven_io_v1alpha1.OpenSearchACLConfig, spec OpenSearchACLSpec) error {
	resp, err := m.liveConfig.Get(ctx, spec.Project, spec.ServiceName)
	if err != nil {
		if aiven.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("seeding from live ACL config: %w", err)
	}
	for _, acl := range resp.OpenSearchACLConfig.ACLs {
		rules := make([]aiven_io_v1alpha1.OpenSearchACLConfigRule, 0, len(acl.Rules))
		for _, rule := range acl.Rules {
			rules = append(rules, aiven_io_v1alpha1.OpenSearchACLConfigRule{
				Index:      rule.Index,
				Permission: rule.Permission,
			})
		}
		aclConfig.Spec.Acls = append(aclConfig.Spec.Acls, aiven_io_v1alpha1.OpenSearchACLConfigACL{
			Username: acl.Username,
			Rules:    rules,
		})
	}
	return nil
}

func upsertACLEntry(spec *aiven_io_v1alpha1.OpenSearchACLConfigSpec, username, access string) {
	rules := []aiven_io_v1alpha1.OpenSearchACLConfigRule{
		{Index: "_*", Permission: access},
		{Index: "*", Permission: access},
	}
	for i := range spec.Acls {
		if spec.Acls[i].Username == username {
			spec.Acls[i].Rules = rules
			return
		}
	}
	spec.Acls = append(spec.Acls, aiven_io_v1alpha1.OpenSearchACLConfigACL{
		Username: username,
		Rules:    rules,
	})
}
