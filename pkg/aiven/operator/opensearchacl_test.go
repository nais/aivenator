package operator

import (
	"context"
	"testing"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	aclTestProject   = "my-project"
	aclTestService   = "opensearch-my-namespace-my-instance"
	aclTestNamespace = "my-namespace"
	aclTestUsername  = "my-namespace-r-abc"
)

func TestOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Operator Suite")
}

var _ = Describe("OpenSearchACLConfigManager", func() {
	var (
		ctx        context.Context
		logger     log.FieldLogger
		manager    *OpenSearchACLConfigManager
		liveConfig *opensearch.MockACLManager
		fakeClient client.Client
		instance   *aiven_io_v1alpha1.OpenSearch
	)

	aclTestSpec := func() OpenSearchACLSpec {
		return OpenSearchACLSpec{
			Project:     aclTestProject,
			ServiceName: aclTestService,
			Namespace:   aclTestNamespace,
			Username:    aclTestUsername,
			Access:      "read",
		}
	}

	// setup rebuilds the manager over a fake client seeded with the OpenSearch
	// instance plus any extra objects a spec needs.
	setup := func(objects ...client.Object) {
		scheme := runtime.NewScheme()
		Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())

		instance = &aiven_io_v1alpha1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: aclTestService, Namespace: aclTestNamespace, UID: types.UID("instance-uid")},
		}
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objects, instance)...).Build()
		liveConfig = opensearch.NewMockACLManager(GinkgoT())
		manager = &OpenSearchACLConfigManager{client: fakeClient, liveConfig: liveConfig}
	}

	getACLConfig := func() *aiven_io_v1alpha1.OpenSearchACLConfig {
		GinkgoHelper()
		aclConfig := &aiven_io_v1alpha1.OpenSearchACLConfig{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Namespace: aclTestNamespace, Name: aclTestService}, aclConfig)).To(Succeed())
		return aclConfig
	}

	// storedInstance returns the instance as the fake client holds it (with UID
	// and resourceVersion), matching how the owner ref is resolved in practice.
	storedInstance := func() client.Object {
		GinkgoHelper()
		stored := &aiven_io_v1alpha1.OpenSearch{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Namespace: aclTestNamespace, Name: aclTestService}, stored)).To(Succeed())
		return stored
	}

	BeforeEach(func() {
		ctx = context.Background()
		logger = log.NewEntry(log.New())
		setup()
	})

	Describe("CreateServiceUserACLs", func() {
		It("seeds spec.acls from the live config on first creation", func() {
			liveConfig.On("Get", mock.Anything, aclTestProject, aclTestService).Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{
					Enabled: true,
					ACLs: []aiven.OpenSearchACL{
						{Username: "pre-existing-user", Rules: []aiven.OpenSearchACLRule{{Index: "*", Permission: "readwrite"}}},
					},
				},
			}, nil).Once()

			Expect(manager.CreateServiceUserACLs(ctx, storedInstance(), aclTestSpec(), logger)).To(Succeed())

			aclConfig := getACLConfig()
			Expect(aclConfig.Spec.Enabled).To(BeTrue())
			Expect(aclConfig.Spec.Project).To(Equal(aclTestProject))
			Expect(aclConfig.Spec.ServiceName).To(Equal(aclTestService))
			Expect(aclConfig.Spec.Acls).To(HaveLen(2))
			Expect(aclConfig.Spec.Acls[0].Username).To(Equal("pre-existing-user"))
			Expect(aclConfig.Spec.Acls[1].Username).To(Equal(aclTestUsername))
			Expect(aclConfig.Spec.Acls[1].Rules).To(HaveLen(2))
			Expect(aclConfig.Spec.Acls[1].Rules[0].Index).To(Equal("_*"))
			Expect(aclConfig.Spec.Acls[1].Rules[0].Permission).To(BeEquivalentTo("read"))
			Expect(aclConfig.Spec.Acls[1].Rules[1].Index).To(Equal("*"))

			Expect(aclConfig.GetLabels()).To(Equal(map[string]string{"team": aclTestNamespace}))
			Expect(aclConfig.GetOwnerReferences()).To(HaveLen(1))
			Expect(aclConfig.GetOwnerReferences()[0].Name).To(Equal(aclTestService))
			Expect(aclConfig.GetOwnerReferences()[0].Kind).To(Equal("OpenSearch"))
		})

		It("is idempotent and seeds from the live config only once", func() {
			liveConfig.On("Get", mock.Anything, aclTestProject, aclTestService).Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{Enabled: true},
			}, nil).Once()

			Expect(manager.CreateServiceUserACLs(ctx, instance, aclTestSpec(), logger)).To(Succeed())
			Expect(manager.CreateServiceUserACLs(ctx, instance, aclTestSpec(), logger)).To(Succeed())

			aclConfig := getACLConfig()
			Expect(aclConfig.Spec.Acls).To(HaveLen(1))
			Expect(aclConfig.Spec.Acls[0].Username).To(Equal(aclTestUsername))
		})

		It("updates the rules of an existing entry", func() {
			liveConfig.On("Get", mock.Anything, aclTestProject, aclTestService).Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{Enabled: true},
			}, nil).Once()

			Expect(manager.CreateServiceUserACLs(ctx, instance, aclTestSpec(), logger)).To(Succeed())

			adminSpec := aclTestSpec()
			adminSpec.Access = "admin"
			Expect(manager.CreateServiceUserACLs(ctx, instance, adminSpec, logger)).To(Succeed())

			aclConfig := getACLConfig()
			Expect(aclConfig.Spec.Acls).To(HaveLen(1))
			Expect(aclConfig.Spec.Acls[0].Rules[0].Permission).To(BeEquivalentTo("admin"))
		})

		It("treats a missing live config as an empty seed", func() {
			liveConfig.On("Get", mock.Anything, aclTestProject, aclTestService).
				Return(nil, aiven.Error{Message: "not found", Status: 404}).Once()

			Expect(manager.CreateServiceUserACLs(ctx, instance, aclTestSpec(), logger)).To(Succeed())

			aclConfig := getACLConfig()
			Expect(aclConfig.Spec.Acls).To(HaveLen(1))
		})

		// A non-404 read is a live Aiven failure, not an empty service: seeding
		// must abort rather than write a CR that drops every pre-existing user.
		It("propagates a non-404 error from the live-config read", func() {
			liveConfig.On("Get", mock.Anything, aclTestProject, aclTestService).
				Return(nil, aiven.Error{Message: "boom", Status: 500}).Once()

			Expect(manager.CreateServiceUserACLs(ctx, instance, aclTestSpec(), logger)).ToNot(Succeed())
		})
	})

	Describe("DeleteServiceUserACLs", func() {
		It("removes only the entry and keeps the CR", func() {
			setup(&aiven_io_v1alpha1.OpenSearchACLConfig{
				ObjectMeta: metav1.ObjectMeta{Name: aclTestService, Namespace: aclTestNamespace},
				Spec: aiven_io_v1alpha1.OpenSearchACLConfigSpec{
					Enabled: true,
					Acls: []aiven_io_v1alpha1.OpenSearchACLConfigACL{
						{Username: aclTestUsername, Rules: []aiven_io_v1alpha1.OpenSearchACLConfigRule{{Index: "*", Permission: "read"}}},
						{Username: "other-user", Rules: []aiven_io_v1alpha1.OpenSearchACLConfigRule{{Index: "*", Permission: "read"}}},
					},
				},
			})

			Expect(manager.DeleteServiceUserACLs(ctx, aclTestNamespace, aclTestService, aclTestUsername, logger)).To(Succeed())

			aclConfig := getACLConfig()
			Expect(aclConfig.Spec.Acls).To(HaveLen(1))
			Expect(aclConfig.Spec.Acls[0].Username).To(Equal("other-user"))
			Expect(aclConfig.Spec.Enabled).To(BeTrue())
		})

		It("is a no-op when the CR does not exist", func() {
			Expect(manager.DeleteServiceUserACLs(ctx, aclTestNamespace, aclTestService, aclTestUsername, logger)).To(Succeed())
		})
	})
})
