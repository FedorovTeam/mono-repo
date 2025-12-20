package object

import (
	"net"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/mono-repo/table-extractor-service/internal/config"
)

func NewMinioClient(cfg config.MinioConfig, timeouts config.StorageConfig) (*minio.Client, error) {
	dialTimeout := timeouts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	readWriteTimeout := timeouts.ReadWriteTimeout
	if readWriteTimeout <= 0 {
		readWriteTimeout = 30 * time.Second
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: readWriteTimeout,
		IdleConnTimeout:       readWriteTimeout,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   10,
	}

	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Transport:    transport,
		BucketLookup: minio.BucketLookupAuto,
	})
}
