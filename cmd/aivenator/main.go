package main

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/credentials"
	"os"
	"os/signal"
	"strings"
	"sync"
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

type QuitChannel chan error

const (
	ExitOK = iota
	ExitController
	ExitConfig
	ExitRuntime
)

// Configuration options
const (
	AivenToken                   = "aiven-token"
	KubernetesWriteRetryInterval = "kubernetes-write-retry-interval"
	LogFormat                    = "log-format"
	MetricsAddress               = "metrics-address"
	Projects                     = "projects"
	SyncPeriod                   = "sync-period"
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
	quit := make(QuitChannel, 1)
	signals := make(chan os.Signal, 1)

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

	go manageCredentials(quit, aivenClient, logger, mgr)

	go janitor(quit, logger, mgr)

	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case err := <-quit:
				logger.Errorf("terminating unexpectedly: %s", err)
				os.Exit(ExitRuntime)
			case sig := <-signals:
				logger.Infof("exiting due to signal: %s", strings.ToUpper(sig.String()))
				os.Exit(ExitOK)
			}
		}
	}()

	terminator := context.Background()
	if err := mgr.Start(terminator); err != nil {
		quit <- fmt.Errorf("manager stopped unexpectedly: %s", err)
		wg.Wait()
		return
	}

	quit <- fmt.Errorf("manager has stopped")
	wg.Wait()
}

// TODO: Finds secrets that are no longer in use and cleans up associated service user before deleting secret
func janitor(quit QuitChannel, logger *log.Logger, mgr manager.Manager) {

}

func manageCredentials(quit QuitChannel, aiven *aiven.Client, logger *log.Logger, mgr manager.Manager) {
	reconciler := credentials.AivenApplicationReconciler{
		Logger:  logger,
		Client:  mgr.GetClient(),
		Creator: credentials.NewCreator(aiven),
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		quit <- fmt.Errorf("unable to set up reconciler: %s", err)
		return
	}

	logger.Info("Aivenator started")
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
