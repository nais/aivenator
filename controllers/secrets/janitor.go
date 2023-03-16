package secrets

import (
	"context"
	"github.com/nais/aivenator/pkg/credentials"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	cleanUpInterval = 15 * time.Minute
)

type Janitor struct {
	client.Client
	logger     log.FieldLogger
	cleaner    credentials.Cleaner
	appChanges <-chan aiven_nais_io_v1.AivenApplication
}

func NewJanitor(cleaner credentials.Cleaner, appChanges <-chan aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) *Janitor {
	return &Janitor{
		logger:     logger,
		cleaner:    cleaner,
		appChanges: appChanges,
	}
}

func (j *Janitor) InjectClient(c client.Client) error {
	j.Client = c
	return nil
}

func (j *Janitor) Start(ctx context.Context) error {
	ticker := time.NewTicker(cleanUpInterval)

	for {
		select {
		case <-ticker.C:
			j.logger.Info("Running cleaner for all secrets")
			err := j.cleaner.CleanUnusedSecrets(ctx)
			if err != nil {
				return err
			}
		case app := <-j.appChanges:
			// Clean secrets for app
			j.logger.Infof("Running cleaner for secrets belonging to aivenapp %s/%s", app.GetNamespace(), app.GetName())
			err := j.cleaner.CleanUnusedSecretsForApplication(ctx, app)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}
