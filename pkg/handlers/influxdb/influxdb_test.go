package influxdb

import (
	"context"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

const (
	appName                  = "test-app"
	namespace                = "team-a"
	projectName              = "my-project"
	instanceName             = "influx-team-a"
	serviceURI               = "https+influxdb://influx-team-a.example.com:23456"
	serviceHost              = "influx-team-a.example.com"
	servicePort              = 23456
	servicePassword          = "service-password"
	serviceUserName          = "avnadmin"
	serviceDbName            = "defaultdb"
	serviceUserAnnotationKey = "influxdb.aiven.nais.io/serviceUser"
	usernameKey              = "INFLUXDB_USERNAME"
	passwordKey              = "INFLUXDB_PASSWORD"
	uriKey                   = "INFLUXDB_URI"
	dbnameKey                = "INFLUXDB_NAME"
)

type mockContainer struct {
	serviceManager *service.MockServiceManager
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
	var ctx context.Context
	var cancel context.CancelFunc

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		secret = v1.Secret{}
		mocks = mockContainer{
			serviceManager: service.NewMockServiceManager(GinkgoT()),
		}
		influxdbHandler = InfluxDBHandler{
			service:     mocks.serviceManager,
			projectName: projectName,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})

	AfterEach(func() {
		cancel()
	})

	When("it receives a spec without InfluxDB", func() {
		BeforeEach(func() {
			application = applicationBuilder.Build()
		})

		It("ignores it", func() {
			_, err := influxdbHandler.Apply(ctx, &application, &secret, logger)
			Expect(err).To(Succeed())
			Expect(secret).To(Equal(v1.Secret{}))
		})
	})

	When("it receives a spec with InfluxDB requested", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					InfluxDB: &aiven_nais_io_v1.InfluxDBSpec{
						Instance: instanceName,
					}}).
				Build()
		})

		Context("and the service is unavailable when fetching addresses", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, instanceName).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				_, err := influxdbHandler.Apply(ctx, &application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})

		Context("and the service is unavailable when fetching the service", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, instanceName).
					Return(&service.ServiceAddresses{
						InfluxDB: service.ServiceAddress{
							URI:  serviceURI,
							Host: serviceHost,
							Port: servicePort,
						},
					}, nil)

				mocks.serviceManager.On("Get", mock.Anything, projectName, instanceName).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				_, err := influxdbHandler.Apply(ctx, &application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})
	})

	When("it receives a spec", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					InfluxDB: &aiven_nais_io_v1.InfluxDBSpec{
						Instance: instanceName,
					}}).
				Build()

			mocks.serviceManager.On("Get", mock.Anything, projectName, instanceName).
				Return(&aiven.Service{
					ConnectionInfo: aiven.ConnectionInfo{
						InfluxDBDatabaseName: serviceDbName,
						InfluxDBUsername:     serviceUserName,
						InfluxDBPassword:     servicePassword,
					},
				}, nil)

			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, instanceName).
				Return(&service.ServiceAddresses{
					InfluxDB: service.ServiceAddress{
						URI:  serviceURI,
						Host: serviceHost,
						Port: servicePort,
					},
				}, nil)
		})

		It("uses the avnadmin user", func() {
			_, err := influxdbHandler.Apply(ctx, &application, &secret, logger)

			Expect(err).To(Succeed())
			Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(serviceUserAnnotationKey, serviceUserName))
			Expect(secret.StringData).To(HaveKeyWithValue(usernameKey, serviceUserName))
			Expect(secret.StringData).To(HaveKeyWithValue(passwordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(uriKey, serviceURI))
			Expect(secret.StringData).To(HaveKeyWithValue(dbnameKey, serviceDbName))
		})
	})
})
