package storage

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7/pkg/notification"
)

// ConfigureUploadNotification настраивает webhook/queue для событий загрузки исходников.
func ConfigureUploadNotification(ctx context.Context, client Client, bucket, targetArn string) error {
	if bucket == "" {
		return fmt.Errorf("bucket name is required")
	}
	if targetArn == "" {
		return fmt.Errorf("target arn is required")
	}

	arn, err := notification.NewArnFromString(targetArn)
	if err != nil {
		return fmt.Errorf("invalid target arn: %w", err)
	}

	cfg := notification.Configuration{}
	queueCfg := notification.NewConfig(arn)
	queueCfg.AddEvents(notification.ObjectCreatedAll)

	if ok := cfg.AddQueue(queueCfg); !ok {
		return fmt.Errorf("failed to add webhook queue config for %s", targetArn)
	}

	if err := client.SetBucketNotification(ctx, bucket, cfg); err != nil {
		return fmt.Errorf("configure bucket notification: %w", err)
	}

	return nil
}
