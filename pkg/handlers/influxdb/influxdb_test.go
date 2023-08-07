package influxdb

import (
	aivenator_mocks "github.com/nais/aivenator/pkg/mocks"
	"testing"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
)

const (
	appName         = "test-app"
	namespace       = "team-a"
	servicePassword = "service-password"
	projectName     = "my-project"
)

type testData struct {
	instanceName             string
	serviceURI               string
	username                 string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
}

var testInstance = testData{
	instanceName:             "influx-team-a",
	serviceURI:               "https+influxdb://influx-team-a.example.com:23456",
	username:                 "avnadmin",
	serviceUserAnnotationKey: "influxdb.aiven.nais.io/serviceUser",
	usernameKey:              "INFLUXDB_USERNAME",
	passwordKey:              "INFLUXDB_PASSWORD",
	uriKey:                   "INFLUXDB_URI",
}

type mockContainer struct {
	serviceUserManager *aivenator_mocks.ServiceUserManager
	serviceManager     *aivenator_mocks.ServiceManager
}

func TestInfluxDB(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "InfluxDB Suite")
}

var _ = Describe("influxdb.Handler", func() {
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var application aiven_nais_io_v1.AivenApplication
	var secret v1.Secret
	var influxdbHandler InfluxDBHandler
	var mocks mockContainer

	defaultServiceManagerMock := func(data testData) {
		mocks.serviceManager.On("GetServiceAddresses", projectName, data.instanceName).
			Return(&service.ServiceAddresses{
				InfluxDB: data.serviceURI,
			}, nil)
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		secret = v1.Secret{}
		mocks = mockContainer{
			serviceUserManager: aivenator_mocks.NewServiceUserManager(GinkgoT()),
			serviceManager:     aivenator_mocks.NewServiceManager(GinkgoT()),
		}
		influxdbHandler = InfluxDBHandler{
			serviceuser: mocks.serviceUserManager,
			service:     mocks.serviceManager,
			projectName: projectName,
		}
	})

	When("it receives a spec without InfluxDB", func() {
		BeforeEach(func() {
			application = applicationBuilder.Build()
		})

		It("ignores it", func() {
			err := influxdbHandler.Apply(&application, &secret, logger)
			Expect(err).To(Succeed())
			Expect(secret).To(Equal(v1.Secret{}))
		})
	})

	When("it receives a spec with InfluxDB requested", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					InfluxDB: &aiven_nais_io_v1.InfluxDBSpec{
						Instance: testInstance.instanceName,
					}}).
				Build()
		})

		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", projectName, testInstance.instanceName).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				err := influxdbHandler.Apply(&application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})

		Context("and service users are unavailable", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(testInstance)
				mocks.serviceUserManager.On("Get", testInstance.username, projectName, testInstance.instanceName, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				err := influxdbHandler.Apply(&application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetServiceUser failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})
	})

	When("it receives a spec", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					InfluxDB: &aiven_nais_io_v1.InfluxDBSpec{
						Instance: testInstance.instanceName,
					}}).
				Build()
		})

		assertHappy := func(secret *v1.Secret, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(testInstance.serviceUserAnnotationKey, testInstance.username))
			Expect(secret.StringData).To(HaveKeyWithValue(testInstance.usernameKey, testInstance.username))
			Expect(secret.StringData).To(HaveKeyWithValue(testInstance.passwordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(testInstance.uriKey, testInstance.serviceURI))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(testInstance)
				mocks.serviceUserManager.On("Get", testInstance.username, projectName, testInstance.instanceName, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: testInstance.username,
						Password: servicePassword,
					}, nil)
			})

			It("uses the existing user", func() {
				err := influxdbHandler.Apply(&application, &secret, logger)
				assertHappy(&secret, err)
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(testInstance)
				mocks.serviceUserManager.On("Get", testInstance.username, projectName, testInstance.instanceName, mock.Anything).
					Return(nil, aiven.Error{
						Message: "Service user does not exist",
						Status:  404,
					})
				var accessControl *aiven.AccessControl
				mocks.serviceUserManager.On("Create", testInstance.username, projectName, testInstance.instanceName, accessControl, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: testInstance.username,
						Password: servicePassword,
					}, nil)
			})

			It("creates the new user and returns credentials for the new user", func() {
				err := influxdbHandler.Apply(&application, &secret, logger)
				assertHappy(&secret, err)
			})
		})
	})
})
