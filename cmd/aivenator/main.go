package main

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/controllers/aiven_application"
	"github.com/nais/aivenator/controllers/secrets"
	"github.com/nais/aivenator/pkg/credentials"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	aivenatormetrics "github.com/nais/aivenator/pkg/metrics"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "net/http/pprof" // Enable http profiling
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	ExitOK = iota
	ExitController
	ExitConfig
	ExitRuntime
	ExitCredentialsManager
)

// Configuration options
const (
	AivenToken                   = "aiven-token"
	KubernetesWriteRetryInterval = "kubernetes-write-retry-interval"
	LogFormat                    = "log-format"
	LogLevel                     = "log-level"
	MetricsAddress               = "metrics-address"
	Projects                     = "projects"
	SyncPeriod                   = "sync-period"
	MainProject                  = "main-project"
	UseNativeGenerator           = "use-native-generator"
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
	flag.String(LogFormat, LogFormatText, fmt.Sprintf("Log format, one of %s", strings.Join([]string{LogFormatText, LogFormatJSON}, ", ")))
	flag.String(LogLevel, "info", logLevelHelp())
	flag.Duration(KubernetesWriteRetryInterval, time.Second*10, "Requeueing interval when Kubernetes writes fail")
	flag.Duration(SyncPeriod, time.Hour*1, "How often to re-synchronize all AivenApplication resources including credential rotation")
	flag.StringSlice(Projects, []string{"nav-integration-test"}, "List of projects allowed to operate on")
	flag.String(MainProject, "nav-integration-test", "Main project to operate on for services that only allow one")
	flag.Bool(UseNativeGenerator, false, "Switch to using the native generator for JVM credential stores")

	flag.Parse()

	err := viper.BindPFlags(flag.CommandLine)
	if err != nil {
		panic(err)
	}
}

func logLevelHelp() string {
	help := strings.Builder{}
	help.WriteString("Log level, one of: ")
	notFirst := false
	for _, level := range log.AllLevels {
		if notFirst {
			help.WriteString(", ")
		}
		help.WriteString(fmt.Sprintf("\"%s\"", level.String()))
		notFirst = true
	}
	return help.String()
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

	go func() {
		// Serve profiling data on 6060
		err := http.ListenAndServe("localhost:6060", nil)
		logger.Errorf("Failed to serve profiling: %v", err)
	}()

	logger.SetFormatter(logfmt)
	level, err := log.ParseLevel(viper.GetString(LogLevel))
	if err != nil {
		logger.Error(err)
		os.Exit(ExitConfig)
	}
	logger.SetLevel(level)

	aivenClient, err := newAivenClient()
	if err != nil {
		logger.Errorf("unable to set up aiven client: %s", err)
		os.Exit(ExitConfig)
	}

	scheme, err := liberator_scheme.All()
	if err != nil {
		logger.Errorf("unable to load schemes: %s", err)
		os.Exit(ExitRuntime)
	}

	allowedProjects := viper.GetStringSlice(Projects)

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

	if err := manageCredentials(terminator, aivenClient, logger, mgr, allowedProjects, viper.GetString(MainProject), viper.GetBool(UseNativeGenerator)); err != nil {
		logger.Errorln(err)
		os.Exit(ExitCredentialsManager)
	}

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

func newAivenClient() (*aiven.Client, error) {
	aivenClient, err := aiven.NewTokenClient(viper.GetString(AivenToken), "")
	if err != nil {
		return nil, err
	}

	_, err = aivenClient.Projects.List()
	if err != nil {
		return nil, fmt.Errorf("error verifying Aiven connection: %w", utils.UnwrapAivenError(err))
	}
	return aivenClient, err
}

func manageCredentials(ctx context.Context, aiven *aiven.Client, logger *log.Logger, mgr manager.Manager, projects []string, mainProjectName string, useNativeGenerator bool) error {
	appChanges := make(chan aiven_nais_io_v1.AivenApplication)

	credentialsManager := credentials.NewManager(ctx, aiven, projects, mainProjectName, logger.WithFields(log.Fields{"component": "CredentialsManager"}), useNativeGenerator)
	reconciler := aiven_application.NewReconciler(mgr, logger, credentialsManager, appChanges)

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

	credentialsCleaner := credentials.Cleaner{
		Client: mgr.GetClient(),
		Logger: logger.WithFields(log.Fields{
			"component": "SecretsCleaner",
		}),
	}
	janitor := secrets.NewJanitor(credentialsCleaner, appChanges, logger.WithFields(log.Fields{"component": "SecretsJanitor"}))
	if err := mgr.Add(janitor); err != nil {
		return fmt.Errorf("unable to add janitor to manager: %v", err)
	}
	logger.Info("Aiven Secret janitor setup complete")

	return nil
}

func init() {
	aivenatormetrics.Register(metrics.Registry)
}
