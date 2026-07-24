package operator

import (
	"context"
	"errors"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("operator.Manager", func() {
	const (
		suNamespace = "team-a"
		suName      = "test-app-r-abc123"
		suService   = "valkey-team-a-cache"
		suProject   = "my-project"
	)

	var (
		ctx        context.Context
		logger     log.FieldLogger
		fakeClient client.Client
		manager    *Manager
		owner      client.Object
	)

	// getCR reads back the ServiceUser CR as the fake client holds it.
	getCR := func() *unstructured.Unstructured {
		GinkgoHelper()
		cr := &unstructured.Unstructured{}
		cr.SetGroupVersionKind(serviceUserGVK)
		Expect(fakeClient.Get(ctx, client.ObjectKey{Namespace: suNamespace, Name: suName}, cr)).To(Succeed())
		return cr
	}

	// rawSecret builds the connection secret the aiven-operator publishes for a
	// reconciled ServiceUser CR, keyed by the target name Manager reads back.
	rawSecret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: RawSecretName(suName), Namespace: suNamespace},
			Data:       data,
		}
	}

	// setup rebuilds the manager over a fake client seeded with objects. The
	// ServiceUser GVK is registered as unstructured because liberator has no
	// typed ServiceUser (aiven-operator#1238), matching how Manager treats it.
	setup := func(objects ...client.Object) {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		scheme.AddKnownTypeWithName(serviceUserGVK, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "aiven.io", Version: "v1alpha1", Kind: "ServiceUserList"}, &unstructured.UnstructuredList{})
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		manager = &Manager{client: fakeClient}
	}

	// existingCR is a ServiceUser CR already targeting suService, used by the
	// read-only methods and the delete path.
	existingCR := func(serviceName string) *unstructured.Unstructured {
		cr := &unstructured.Unstructured{}
		cr.SetGroupVersionKind(serviceUserGVK)
		cr.SetNamespace(suNamespace)
		cr.SetName(suName)
		Expect(unstructured.SetNestedField(cr.Object, serviceName, "spec", "serviceName")).To(Succeed())
		return cr
	}

	BeforeEach(func() {
		ctx = context.Background()
		logger = log.NewEntry(log.New())
		owner = ptrTo(aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", suNamespace).Build())
		setup()
	})

	spec := func() ServiceUserSpec {
		return ServiceUserSpec{
			Name:        suName,
			Namespace:   suNamespace,
			Project:     suProject,
			ServiceName: suService,
			AccessControl: map[string]any{
				"valkeyAclCategories": []any{"+@read"},
			},
		}
	}

	Describe("CreateServiceUser", func() {
		It("ensures the CR and returns the credentials the operator published", func() {
			setup(rawSecret(map[string][]byte{
				ServiceUserUsername: []byte(suName),
				ServiceUserPassword: []byte("s3cret"),
				ServiceUserHost:     []byte("cache.example.com"),
				ServiceUserPort:     []byte("23456"),
			}))

			su, err := manager.CreateServiceUser(ctx, owner, spec(), logger)
			Expect(err).To(Succeed())
			Expect(su.Username).To(Equal(suName))
			Expect(su.Secret).To(HaveKeyWithValue(ServiceUserPassword, "s3cret"))

			cr := getCR()
			Expect(cr.GetLabels()).To(HaveKeyWithValue("app", "test-app"))
			Expect(cr.GetLabels()).To(HaveKeyWithValue("team", suNamespace))
			project, _, _ := unstructured.NestedString(cr.Object, "spec", "project")
			Expect(project).To(Equal(suProject))
			serviceName, _, _ := unstructured.NestedString(cr.Object, "spec", "serviceName")
			Expect(serviceName).To(Equal(suService))
			target, _, _ := unstructured.NestedString(cr.Object, "spec", "connInfoSecretTarget", "name")
			Expect(target).To(Equal(RawSecretName(suName)))
			acl, found, _ := unstructured.NestedSlice(cr.Object, "spec", "accessControl", "valkeyAclCategories")
			Expect(found).To(BeTrue())
			Expect(acl).To(ConsistOf("+@read"))
		})

		// aiven-operator not responding: the CR is written but no connection secret
		// is ever published. Manager must fail (not return empty credentials) so the
		// caller requeues until the operator catches up.
		It("returns ErrNotFound when the operator has not published the secret", func() {
			su, err := manager.CreateServiceUser(ctx, owner, spec(), logger)
			Expect(err).To(MatchError(utils.ErrNotFound))
			Expect(su).To(BeNil())

			// The CR itself was still created, so the operator can act on it.
			Expect(getCR().GetName()).To(Equal(suName))
		})

		// A published-but-incomplete secret is an operator/Aiven fault; projecting a
		// blank username downstream would be worse than failing loudly.
		It("fails when the published secret lacks the username", func() {
			setup(rawSecret(map[string][]byte{
				ServiceUserPassword: []byte("s3cret"),
			}))

			su, err := manager.CreateServiceUser(ctx, owner, spec(), logger)
			Expect(err).To(MatchError(utils.ErrNotFound))
			Expect(su).To(BeNil())
		})

		It("omits accessControl from the CR when the spec has none", func() {
			setup(rawSecret(map[string][]byte{ServiceUserUsername: []byte(suName)}))
			s := spec()
			s.AccessControl = nil

			_, err := manager.CreateServiceUser(ctx, owner, s, logger)
			Expect(err).To(Succeed())

			_, found, _ := unstructured.NestedMap(getCR().Object, "spec", "accessControl")
			Expect(found).To(BeFalse())
		})
	})

	Describe("ServiceName", func() {
		It("returns the CR's immutable serviceName when it exists", func() {
			setup(existingCR(suService))
			serviceName, exists, err := manager.ServiceName(ctx, suNamespace, suName)
			Expect(err).To(Succeed())
			Expect(exists).To(BeTrue())
			Expect(serviceName).To(Equal(suService))
		})

		It("reports non-existence without error when the CR is absent", func() {
			serviceName, exists, err := manager.ServiceName(ctx, suNamespace, suName)
			Expect(err).To(Succeed())
			Expect(exists).To(BeFalse())
			Expect(serviceName).To(BeEmpty())
		})
	})

	Describe("Exists", func() {
		It("is true when the CR is present", func() {
			setup(existingCR(suService))
			Expect(manager.Exists(ctx, suNamespace, suName)).To(BeTrue())
		})

		It("is false when the CR is absent", func() {
			Expect(manager.Exists(ctx, suNamespace, suName)).To(BeFalse())
		})
	})

	Describe("DeleteServiceUser", func() {
		It("deletes an existing CR", func() {
			setup(existingCR(suService))
			Expect(manager.DeleteServiceUser(ctx, suNamespace, suName, logger)).To(Succeed())

			cr := &unstructured.Unstructured{}
			cr.SetGroupVersionKind(serviceUserGVK)
			err := fakeClient.Get(ctx, client.ObjectKey{Namespace: suNamespace, Name: suName}, cr)
			Expect(err).To(HaveOccurred())
		})

		// Cleanup treats an absent CR as an idempotent success, so Delete surfaces a
		// 404 the caller recognises via aiven.IsNotFound rather than a raw k8s error.
		It("returns an Aiven 404 when the CR is already gone", func() {
			err := manager.DeleteServiceUser(ctx, suNamespace, suName, logger)
			Expect(err).To(HaveOccurred())
			Expect(aiven.IsNotFound(err)).To(BeTrue())
			var aivenErr aiven.Error
			Expect(errors.As(err, &aivenErr)).To(BeTrue())
			Expect(aivenErr.Status).To(Equal(404))
		})
	})
})

func ptrTo[T any](v T) *T {
	return &v
}
