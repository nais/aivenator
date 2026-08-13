package operator

import (
	"context"
	"errors"
	"time"

	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	Describe("ResolveServiceUserName", func() {
		const legacyIn = "carried-legacy"
		const suApp = "test-app"

		// resolve invokes the fold with the fixture's annotation values; the family
		// tuple matches existingCR's app label and suService.
		resolve := func(existingName, existingLegacy string) (NameResolution, error) {
			return ResolveServiceUserName(ctx, manager, suNamespace, suApp, "", "instance", "secret", suService, existingName, existingLegacy, log.NewEntry(log.New()))
		}

		It("mints a creating week-stamped name and carries any legacy when nothing exists", func() {
			res, err := resolve("", legacyIn)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Name).To(HavePrefix(utils.ServiceUserNamePrefix(suApp, "", "instance", "secret") + "-"))
			Expect(res.Legacy).To(Equal(legacyIn))
			Expect(res.Adopted).To(BeFalse())
			Expect(res.Creating).To(BeTrue())
		})

		It("routes an annotation that is not a valid CR name to the legacy drain, then mints", func() {
			const rawUser = "team-a_test-app_abc0_"
			Expect(utils.IsValidCRName(rawUser)).To(BeFalse())
			res, err := resolve(rawUser, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Legacy).To(Equal(rawUser))
			Expect(res.Adopted).To(BeFalse())
			Expect(res.Creating).To(BeTrue())
		})

		// A surviving family CR (labelled with the app, same prefix and service) from
		// a failed earlier attempt is adopted instead of minting a sibling; it exists,
		// so nothing is being created.
		It("recovers a stranded family CR when the secret has no annotation", func() {
			stranded := utils.ServiceUserNamePrefix(suApp, "", "instance", "secret") + "-2026w01"
			cr := existingCR(suService)
			cr.Name = stranded
			cr.Labels = map[string]string{"app": suApp}
			setup(cr)
			res, err := resolve("", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Name).To(Equal(stranded))
			Expect(res.Adopted).To(BeFalse())
			Expect(res.Creating).To(BeFalse())
		})

		It("adopts the frozen name when its CR still targets the same service", func() {
			setup(existingCR(suService))
			res, err := resolve(suName, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Name).To(Equal(suName))
			Expect(res.Adopted).To(BeTrue())
			Expect(res.Creating).To(BeFalse())
		})

		// Adopting a CR name must not drop a legacy username the secret still carries,
		// or the pre-CR Aiven user would never be drained in Cleanup.
		It("carries a legacy username through the adopt path", func() {
			setup(existingCR(suService))
			res, err := resolve(suName, legacyIn)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Name).To(Equal(suName))
			Expect(res.Legacy).To(Equal(legacyIn))
		})

		It("adopts the frozen name when the CR is absent, to re-create it in place", func() {
			res, err := resolve(suName, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Name).To(Equal(suName))
			Expect(res.Adopted).To(BeTrue())
			Expect(res.Creating).To(BeTrue())
		})

		It("fails terminally when the CR is bound to a different Aiven service", func() {
			setup(existingCR("some-other-service"))
			res, err := resolve(suName, "")
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
			Expect(res.Name).To(BeEmpty())
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
			res, err := resolve(suName, "")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeFalse())
			Expect(res.Name).To(BeEmpty())
		})
	})
})

func ptrTo[T any](v T) *T {
	return &v
}

var _ = Describe("service user name recovery", func() {
	const (
		ns      = "team-a"
		app     = "test-app"
		service = "valkey-team-a-cache"
		prefix  = "test-app-rw-aaaaaa-bbbbb"
	)

	var (
		ctx     context.Context
		manager *Manager
	)

	su := func(name, serviceName string, labels map[string]string) *aiven_io_v1alpha1.ServiceUser {
		return &aiven_io_v1alpha1.ServiceUser{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
			Spec:       aiven_io_v1alpha1.ServiceUserSpec{ServiceName: serviceName},
		}
	}

	newManager := func(objects ...client.Object) {
		scheme := runtime.NewScheme()
		Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())
		manager = &Manager{client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}
	}

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("FindAdoptable", func() {
		// Every decoy sorts above the family member, so a broken exclusion filter
		// flips the winner and fails the assertion instead of passing silently.
		It("adopts the family CR, ignoring other apps, families and services", func() {
			newManager(
				su(prefix+"-2026w03", service, map[string]string{"app": app}),
				su(prefix+"-2026w09", "other-service", map[string]string{"app": app}),
				su("test-app-rw-zzzzzz-ddddd-2026w09", service, map[string]string{"app": app}),
				su(prefix+"-2026w05", service, map[string]string{"app": "other-app"}),
			)
			name, err := manager.FindAdoptable(ctx, ns, app, prefix, service, log.NewEntry(log.New()))
			Expect(err).To(Succeed())
			Expect(name).To(Equal(prefix + "-2026w03"))
		})

		// The Error must ride the caller's logger so the reconcile's structured
		// fields (aivenapp, team, correlation id) reach dashboards and alerts.
		It("logs a tripwire on the caller's logger when the family has multiple members", func() {
			root, hook := logtest.NewNullLogger()
			fieldedLogger := log.NewEntry(root).WithField("aivenapp", app)
			sightings := metrics.ServiceUserFamilyDuplicates.With(prometheus.Labels{metrics.LabelNamespace: ns})
			before := testutil.ToFloat64(sightings)
			newManager(
				su(prefix+"-2026w01", service, map[string]string{"app": app}),
				su(prefix+"-2026w03", service, map[string]string{"app": app}),
			)
			name, err := manager.FindAdoptable(ctx, ns, app, prefix, service, fieldedLogger)
			Expect(err).To(Succeed())
			Expect(name).To(Equal(prefix + "-2026w03"))
			Expect(hook.Entries).ToNot(BeEmpty())
			Expect(hook.LastEntry().Level).To(Equal(log.ErrorLevel))
			Expect(hook.LastEntry().Data).To(HaveKeyWithValue(utils.FieldInvariant, "multiple ServiceUser CRs in family"))
			Expect(hook.LastEntry().Data).To(HaveKeyWithValue("aivenapp", app))
			Expect(testutil.ToFloat64(sightings)).To(Equal(before + 1))
		})

		// Cleanup deletes the CR, but the operator's finalizer keeps it terminating
		// for a while; its credentials die with finalization, so a redeploy in that
		// window must mint fresh instead of adopting it.
		It("does not adopt a family CR that is being deleted", func() {
			terminating := su(prefix+"-2026w03", service, map[string]string{"app": app})
			terminating.SetDeletionTimestamp(ptrTo(metav1.Now()))
			// The fake client rejects a deleting object without a finalizer; a real
			// terminating CR always holds the operator's.
			terminating.SetFinalizers([]string{"finalizers.aiven.io/processing"})
			newManager(terminating)
			name, err := manager.FindAdoptable(ctx, ns, app, prefix, service, log.NewEntry(log.New()))
			Expect(err).To(Succeed())
			Expect(name).To(BeEmpty())
		})

		// The mint name is deterministic within a week, so this CR — excluded from
		// adoption by its service binding — would be silently updated by the mint's
		// CreateOrUpdate, rejected on the immutable field every reconcile.
		It("fails terminally when the would-be mint name is held by a CR bound to another service", func() {
			collision := utils.ServiceUserName("test-app", "readwrite", "instance", "secret", time.Now())
			prefixNow := utils.ServiceUserNamePrefix("test-app", "readwrite", "instance", "secret")
			newManager(su(collision, "other-service", map[string]string{"app": app}))
			name, err := manager.FindAdoptable(ctx, ns, app, prefixNow, service, log.NewEntry(log.New()))
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
			Expect(name).To(BeEmpty())
		})

		// An earlier week's name cannot collide with the mint; blocking on it would
		// wedge apps over harmless stale debris.
		It("ignores a mismatched-service CR from an earlier week", func() {
			newManager(su(prefix+"-2020w01", "other-service", map[string]string{"app": app}))
			name, err := manager.FindAdoptable(ctx, ns, app, prefix, service, log.NewEntry(log.New()))
			Expect(err).To(Succeed())
			Expect(name).To(BeEmpty())
		})

		It("returns empty when the family has no CRs", func() {
			newManager()
			name, err := manager.FindAdoptable(ctx, ns, app, prefix, service, log.NewEntry(log.New()))
			Expect(err).To(Succeed())
			Expect(name).To(BeEmpty())
		})
	})

})
