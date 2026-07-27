package operator

import (
	"context"
	"errors"

	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
	getCR := func() *aiven_io_v1alpha1.ServiceUser {
		GinkgoHelper()
		cr := &aiven_io_v1alpha1.ServiceUser{}
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

	// setup rebuilds the manager over a fake client seeded with objects.
	setup := func(objects ...client.Object) {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		manager = &Manager{client: fakeClient}
	}

	// existingCR is a ServiceUser CR already targeting suService, used by the
	// read-only methods and the delete path.
	existingCR := func(serviceName string) *aiven_io_v1alpha1.ServiceUser {
		return &aiven_io_v1alpha1.ServiceUser{
			ObjectMeta: metav1.ObjectMeta{Name: suName, Namespace: suNamespace},
			Spec:       aiven_io_v1alpha1.ServiceUserSpec{ServiceName: serviceName},
		}
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
			AccessControl: &aiven_io_v1alpha1.ServiceUserAccessControl{
				ValkeyACLCategories: []string{"+@read"},
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
			Expect(cr.Spec.Project).To(Equal(suProject))
			Expect(cr.Spec.ServiceName).To(Equal(suService))
			Expect(cr.Spec.ConnInfoSecretTarget.Name).To(Equal(RawSecretName(suName)))
			Expect(cr.Spec.AccessControl).NotTo(BeNil())
			Expect(cr.Spec.AccessControl.ValkeyACLCategories).To(ConsistOf("+@read"))
		})

		// CR written but the operator hasn't published the secret yet: fail so the
		// caller requeues, rather than returning empty credentials.
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

			Expect(getCR().Spec.AccessControl).To(BeNil())
		})

		// Kafka's usernames contain '_' and can't name the CR; spec.Username
		// carries the real Aiven username instead.
		It("sets spec.username on first creation", func() {
			setup(rawSecret(map[string][]byte{ServiceUserUsername: []byte("team_app_abc123_xyz")}))
			s := spec()
			s.Username = "team_app_abc123_xyz"

			_, err := manager.CreateServiceUser(ctx, owner, s, logger)
			Expect(err).To(Succeed())

			Expect(getCR().Spec.Username).To(Equal("team_app_abc123_xyz"))
		})

		// spec.username is immutable, so aivenator sets it only on creation and never on
		// an existing user: a later call requesting a different username leaves the stored
		// value untouched instead of issuing a doomed update.
		It("leaves spec.username untouched on an existing user", func() {
			cr := existingCR(suService)
			cr.Spec.Username = "already-set"
			setup(cr, rawSecret(map[string][]byte{ServiceUserUsername: []byte("already-set")}))

			s := spec()
			s.Username = "different-candidate"

			su, err := manager.CreateServiceUser(ctx, owner, s, logger)
			Expect(err).To(Succeed())
			Expect(su.Username).To(Equal("already-set"))
			Expect(getCR().Spec.Username).To(Equal("already-set"))
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

			cr := &aiven_io_v1alpha1.ServiceUser{}
			err := fakeClient.Get(ctx, client.ObjectKey{Namespace: suNamespace, Name: suName}, cr)
			Expect(err).To(HaveOccurred())
		})

		// A k8s NotFound from the delete is propagated as a k8s error (not swallowed,
		// not disguised as an Aiven error); the caller decides what it means.
		It("propagates the k8s NotFound when the CR is already gone", func() {
			err := manager.DeleteServiceUser(ctx, suNamespace, suName, logger)
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Describe("ResolveExistingServiceUser", func() {
		const legacyIn = "carried-legacy"

		It("mints (adopt empty) and carries any legacy when there is no annotation", func() {
			adopt, legacy, err := ResolveExistingServiceUser(ctx, manager, suNamespace, "", legacyIn, suService)
			Expect(err).ToNot(HaveOccurred())
			Expect(adopt).To(BeEmpty())
			Expect(legacy).To(Equal(legacyIn))
		})

		It("routes an annotation that is not a valid CR name to the legacy drain", func() {
			const rawUser = "team-a_test-app_abc0_"
			Expect(utils.IsValidCRName(rawUser)).To(BeFalse())
			adopt, legacy, err := ResolveExistingServiceUser(ctx, manager, suNamespace, rawUser, "", suService)
			Expect(err).ToNot(HaveOccurred())
			Expect(adopt).To(BeEmpty())
			Expect(legacy).To(Equal(rawUser))
		})

		It("adopts the frozen name when its CR still targets the same service", func() {
			setup(existingCR(suService))
			adopt, _, err := ResolveExistingServiceUser(ctx, manager, suNamespace, suName, "", suService)
			Expect(err).ToNot(HaveOccurred())
			Expect(adopt).To(Equal(suName))
		})

		// Adopting a CR name must not drop a legacy username the secret still carries,
		// or the pre-CR Aiven user would never be drained in Cleanup.
		It("carries a legacy username through the adopt path", func() {
			setup(existingCR(suService))
			adopt, legacy, err := ResolveExistingServiceUser(ctx, manager, suNamespace, suName, legacyIn, suService)
			Expect(err).ToNot(HaveOccurred())
			Expect(adopt).To(Equal(suName))
			Expect(legacy).To(Equal(legacyIn))
		})

		It("adopts the frozen name when the CR is absent, to re-create it in place", func() {
			adopt, _, err := ResolveExistingServiceUser(ctx, manager, suNamespace, suName, "", suService)
			Expect(err).ToNot(HaveOccurred())
			Expect(adopt).To(Equal(suName))
		})

		It("fails terminally when the CR is bound to a different Aiven service", func() {
			setup(existingCR("some-other-service"))
			adopt, _, err := ResolveExistingServiceUser(ctx, manager, suNamespace, suName, "", suService)
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
			Expect(adopt).To(BeEmpty())
		})

		// A read failure must never be mistaken for absence (which would silently mint),
		// but it also isn't a permanent misconfiguration like a service mismatch: it's
		// retryable, so the reconciler must requeue quickly rather than stall until an
		// unrelated change comes along.
		It("fails with a retryable error when reading the ServiceUser errors, without mistaking it for absence", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return errors.New("apiserver unavailable")
					},
				}).Build()
			manager = &Manager{client: fakeClient}
			adopt, _, err := ResolveExistingServiceUser(ctx, manager, suNamespace, suName, "", suService)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeFalse())
			Expect(adopt).To(BeEmpty())
		})
	})
})

func ptrTo[T any](v T) *T {
	return &v
}
