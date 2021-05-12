package main

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/controllers/aiven_application"
	"github.com/nais/aivenator/controllers/secrets"
	"github.com/nais/aivenator/pkg/credentials"
	"os"
	"os/signal"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"syscall"
	"time"

	aivenatormetrics "github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	// +kubebuilder:scaffold:imports
)

var scheme = runtime.NewScheme()

const (
	ExitOK = iota
	ExitController
	ExitConfig
	ExitRuntime
	ExitCredentialsManager
	ExitJanitor
)

// Configuration options
const (
	AivenToken                   = "aiven-token"
	KubernetesWriteRetryInterval = "kubernetes-write-retry-interval"
	LogFormat                    = "log-format"
	MetricsAddress               = "metrics-address"
	Projects                     = "projects"
	SyncPeriod                   = "sync-period"
	JanitorPeriod                = "janitor-period"
)

const (
	LogFormatJSON = "json"
	LogFormatText = "text"
)

func init() {

	// Automatically read configuration options from environment variables.
	// i.e. --aiven-token will be configurable using AIVENATOR_AIVEN_TOKEN.
	viper.SetEnvPrefix("AIVENATOR")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))

	flag.String(AivenToken, "", "Administrator credentials for Aiven")
	flag.String(MetricsAddress, "127.0.0.1:8080", "The address the metric endpoint binds to.")
	flag.String(LogFormat, "text", "Log format, either 'text' or 'json'")
	flag.Duration(KubernetesWriteRetryInterval, time.Second*10, "Requeueing interval when Kubernetes writes fail")
	flag.Duration(SyncPeriod, time.Hour*1, "How often to re-synchronize all AivenApplication resources including credential rotation")
	flag.Duration(JanitorPeriod, time.Hour*1, "How often to clean up unused secrets managed by Aivenator")
	flag.StringSlice(Projects, []string{"nav-integration-test"}, "List of projects allowed to operate on")

	flag.Parse()

	err := viper.BindPFlags(flag.CommandLine)
	if err != nil {
		panic(err)
	}
}

func formatter(logFormat string) (log.Formatter, error) {
	switch logFormat {
	case LogFormatJSON:
		return &log.JSONFormatter{
			TimestampFormat:   time.RFC3339Nano,
			DisableHTMLEscape: true,
		}, nil
	case LogFormatText:
		return &log.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339Nano,
		}, nil
	}
	return nil, fmt.Errorf("unsupported log format '%s'", logFormat)
}

func main() {
	logger := log.New()
	logfmt, err := formatter(viper.GetString(LogFormat))
	if err != nil {
		logger.Error(err)
		os.Exit(ExitConfig)
	}

	logger.SetFormatter(logfmt)

	aivenClient, err := aiven.NewTokenClient(viper.GetString(AivenToken), "")
	if err != nil {
		logger.Errorf("unable to set up aiven client: %s", err)
		os.Exit(ExitConfig)
	}

	syncPeriod := viper.GetDuration(SyncPeriod)
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		SyncPeriod:         &syncPeriod,
		Scheme:             scheme,
		MetricsBindAddress: viper.GetString(MetricsAddress),
	})

	if err != nil {
		logger.Errorln(err)
		os.Exit(ExitController)
	}

	logger.Info("Aivenator running")
	terminator := context.Background()

	if err := manageCredentials(aivenClient, logger, mgr); err != nil {
		logger.Errorln(err)
		os.Exit(ExitCredentialsManager)
	}

	startJanitor(terminator, viper.GetDuration(JanitorPeriod), mgr.GetClient(), logger)

	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

		for {
			select {
			case sig := <-signals:
				logger.Infof("exiting due to signal: %s", strings.ToUpper(sig.String()))
				os.Exit(ExitOK)
			}
		}
	}()

	if err := mgr.Start(terminator); err != nil {
		logger.Errorln(fmt.Errorf("manager stopped unexpectedly: %s", err))
		os.Exit(ExitRuntime)
	}

	logger.Errorln(fmt.Errorf("manager has stopped"))
}

func startJanitor(ctx context.Context, janitorInterval time.Duration, c client.Client, logger *log.Logger) {
	janitor := secrets.Janitor{
		Client: c,
		Logger: logger.WithFields(log.Fields{"component": "Janitor"}),
		Ctx:    ctx,
	}
	janitor.Start(janitorInterval)

	logger.Info("Janitor started")
}

func manageCredentials(aiven *aiven.Client, logger *log.Logger, mgr manager.Manager) error {
	credentialsManager := credentials.NewManager(aiven)
	reconciler := aiven_application.AivenApplicationReconciler{
		Logger:  logger.WithFields(log.Fields{"component": "AivenApplicationReconciler"}),
		Client:  mgr.GetClient(),
		Manager: credentialsManager,
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to set up reconciler: %s", err)
	}
	logger.Info("Aiven Application reconciler setup complete")

	finalizer := secrets.SecretsFinalizer{
		Logger:  logger.WithFields(log.Fields{"component": "SecretsFinalizer"}),
		Client:  mgr.GetClient(),
		Manager: credentialsManager,
	}

	if err := finalizer.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to set up finalizer: %s", err)
	}
	logger.Info("Aiven Secret finalizer setup complete")

	return nil
}

func init() {
	err := clientgoscheme.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}

	err = kafka_nais_io_v1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}

	aivenatormetrics.Register(metrics.Registry)
	// +kubebuilder:scaffold:scheme
}
