package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// GRPCBackoffConfig описывает параметры экспоненциального бэкоффа.
type GRPCBackoffConfig struct {
	BaseDelay  time.Duration `yaml:"baseDelay"`
	Multiplier float64       `yaml:"multiplier"`
	Jitter     float64       `yaml:"jitter"`
	MaxDelay   time.Duration `yaml:"maxDelay"`
}

// GRPCKeepaliveConfig описывает параметры keepalive для gRPC клиентов.
type GRPCKeepaliveConfig struct {
	Time                time.Duration `yaml:"time"`
	Timeout             time.Duration `yaml:"timeout"`
	PermitWithoutStream bool          `yaml:"permitWithoutStream"`
}

// GRPCClientConfig описывает параметры соединения gRPC клиента.
type GRPCClientConfig struct {
	Insecure          *bool               `yaml:"insecure"`
	DialTimeout       time.Duration       `yaml:"dialTimeout"`
	MinConnectTimeout time.Duration       `yaml:"minConnectTimeout"`
	Keepalive         GRPCKeepaliveConfig `yaml:"keepalive"`
	Backoff           GRPCBackoffConfig   `yaml:"backoff"`
}

// InsecureOrDefault возвращает true, если соединение должно устанавливаться без TLS.
func (g GRPCClientConfig) InsecureOrDefault() bool {
	if g.Insecure == nil {
		return true
	}

	return *g.Insecure
}

// setDefaults подставляет значения по умолчанию при отсутствии явной настройки.
func (g *GRPCClientConfig) setDefaults() {
	if g.DialTimeout == 0 {
		g.DialTimeout = 5 * time.Second
	}

	if g.MinConnectTimeout == 0 {
		g.MinConnectTimeout = 3 * time.Second
	}

	if g.Keepalive.Time == 0 {
		g.Keepalive.Time = 5 * time.Minute
	}

	if g.Keepalive.Timeout == 0 {
		g.Keepalive.Timeout = 10 * time.Second
	}

	if g.Backoff.BaseDelay == 0 {
		g.Backoff.BaseDelay = time.Second
	}

	if g.Backoff.Multiplier == 0 {
		g.Backoff.Multiplier = 1.6
	}

	if g.Backoff.Jitter == 0 {
		g.Backoff.Jitter = 0.2
	}

	if g.Backoff.MaxDelay == 0 {
		g.Backoff.MaxDelay = 30 * time.Second
	}
}

// validate проверяет, что установлены корректные значения таймаутов.
func (g GRPCClientConfig) validate(service string) error {
	if g.DialTimeout <= 0 {
		return fmt.Errorf("%s.grpc.dialTimeout must be positive", service)
	}

	if g.MinConnectTimeout <= 0 {
		return fmt.Errorf("%s.grpc.minConnectTimeout must be positive", service)
	}

	if g.Keepalive.Time <= 0 {
		return fmt.Errorf("%s.grpc.keepalive.time must be positive", service)
	}

	if g.Keepalive.Timeout <= 0 {
		return fmt.Errorf("%s.grpc.keepalive.timeout must be positive", service)
	}

	if g.Backoff.BaseDelay <= 0 {
		return fmt.Errorf("%s.grpc.backoff.baseDelay must be positive", service)
	}

	if g.Backoff.MaxDelay <= 0 {
		return fmt.Errorf("%s.grpc.backoff.maxDelay must be positive", service)
	}

	if g.Backoff.Multiplier <= 0 {
		return fmt.Errorf("%s.grpc.backoff.multiplier must be positive", service)
	}

	if g.Backoff.Jitter < 0 {
		return fmt.Errorf("%s.grpc.backoff.jitter must be non-negative", service)
	}

	return nil
}

// GRPCServiceConfig описывает адрес и настройки подключения к gRPC сервису.
type GRPCServiceConfig struct {
	Address string           `yaml:"address"`
	GRPC    GRPCClientConfig `yaml:"grpc"`
}

// ObservabilityConfig описывает параметры телеметрии.
type ObservabilityConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	OTLPEndpoint string `yaml:"otlpEndpoint"`
	ServiceName  string `yaml:"serviceName"`
}

// EnabledOrDefault возвращает true, если флаг не задан, и значение флага, если он задан явно.
func (o ObservabilityConfig) EnabledOrDefault() bool {
	if o.Enabled == nil {
		return true
	}

	return *o.Enabled
}

// Config описывает конфигурацию сервиса.
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Security struct {
		APIKey string `yaml:"apiKey"`
	} `yaml:"security"`
	DocumentService GRPCServiceConfig `yaml:"documentService"`
	StorageService  GRPCServiceConfig `yaml:"storageService"`
	Uploads         struct {
		SignedURLTTLSecond int64 `yaml:"signedUrlTtlSeconds"`
	} `yaml:"uploads"`
	Observability ObservabilityConfig `yaml:"observability"`
	Logging       struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
}

// Load читает конфигурацию из файла.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to open config: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.Security.APIKey == "" {
		return Config{}, fmt.Errorf("security.apiKey is required")
	}
	if cfg.DocumentService.Address == "" {
		return Config{}, fmt.Errorf("documentService.address is required")
	}
	if cfg.StorageService.Address == "" {
		return Config{}, fmt.Errorf("storageService.address is required")
	}

	cfg.DocumentService.GRPC.setDefaults()
	if err := cfg.DocumentService.GRPC.validate("documentService"); err != nil {
		return Config{}, err
	}

	cfg.StorageService.GRPC.setDefaults()
	if err := cfg.StorageService.GRPC.validate("storageService"); err != nil {
		return Config{}, err
	}
	if cfg.Uploads.SignedURLTTLSecond <= 0 {
		cfg.Uploads.SignedURLTTLSecond = 900
	}
	if cfg.Observability.ServiceName == "" {
		cfg.Observability.ServiceName = "backend-gateway-service"
	}
	if cfg.Observability.EnabledOrDefault() && cfg.Observability.OTLPEndpoint == "" {
		return Config{}, fmt.Errorf("observability.otlpEndpoint is required when observability.enabled is true")
	}

	return cfg, nil
}
