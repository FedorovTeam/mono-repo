package config

type YugabyteConfig struct {
	Host     string `yaml:"host" env:"YUGABYTE_HOST"`
	Port     string `yaml:"port" env:"YUGABYTE_PORT"`
	User     string `yaml:"user" env:"YUGABYTE_USER"`
	Password string `yaml:"password" env:"YUGABYTE_PASSWORD"`
	DB       string `yaml:"db" env:"YUGABYTE_DB"`
	SSLMode  string `yaml:"sslmode" env:"YUGABYTE_SSLMODE"`
}

type NatsConfig struct {
	URL            string `yaml:"url" env:"NATS_URL"`
	DemoSubject    string `yaml:"demo_subject" env:"NATS_DEMO_SUBJECT"`
	DemoDLQSubject string `yaml:"demo_dlq_subject" env:"NATS_DEMO_DLQ_SUBJECT"`
}

type MinioConfig struct {
	Endpoint  string `yaml:"endpoint" env:"MINIO_ENDPOINT"`
	AccessKey string `yaml:"access_key" env:"MINIO_ACCESS_KEY"`
	SecretKey string `yaml:"secret_key" env:"MINIO_SECRET_KEY"`
	Bucket    string `yaml:"bucket" env:"MINIO_BUCKET"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr" env:"REDIS_ADDR"`
	Password string `yaml:"password" env:"REDIS_PASSWORD"`
	DB       int    `yaml:"db" env:"REDIS_DB"`
}

type ClickhouseConfig struct {
	Addr     string `yaml:"addr" env:"CLICKHOUSE_ADDR"`
	User     string `yaml:"user" env:"CLICKHOUSE_USER"`
	Password string `yaml:"password" env:"CLICKHOUSE_PASSWORD"`
	DB       string `yaml:"db" env:"CLICKHOUSE_DB"`
}

type ServiceConfig struct {
	Name     string `yaml:"name" env:"SERVICE_NAME"`
	Port     int    `yaml:"port" env:"SERVICE_PORT"`
	GRPCPort int    `yaml:"grpc_port" env:"GRPC_PORT"`
	Region   string `yaml:"region" env:"SERVICE_REGION"`
}

type PrometheusConfig struct {
	Enabled bool `yaml:"enabled" env:"PROMETHEUS_ENABLED"`
}

type TracingConfig struct {
	Endpoint string `yaml:"endpoint" env:"TRACING_ENDPOINT"`
}

type LoggingConfig struct {
	Level string `yaml:"level" env:"LOG_LEVEL"`
}

type Config struct {
	Service    ServiceConfig    `yaml:"service"`
	Yugabyte   YugabyteConfig   `yaml:"yugabyte"`
	Minio      MinioConfig      `yaml:"minio"`
	Redis      RedisConfig      `yaml:"redis"`
	Nats       NatsConfig       `yaml:"nats"`
	Clickhouse ClickhouseConfig `yaml:"clickhouse"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Tracing    TracingConfig    `yaml:"tracing"`
	Logging    LoggingConfig    `yaml:"logging"`
	RegionID   string           `yaml:"region_id"`
}
