package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	Storage struct {
		OriginalBucket string `yaml:"original_bucket"`
		MarkdownBucket string `yaml:"markdown_bucket"`
	} `yaml:"storage"`
	Services struct {
		PreprocessService      string `yaml:"preprocess_service"`
		StorageService         string `yaml:"storage_service"`
		TransformerService     string `yaml:"transformer_service"`
		GithubPublisherService string `yaml:"github_publisher_service"`
		TableExtractorService  string `yaml:"table_extractor_service"`
	} `yaml:"services"`
	Github struct {
		Owner         string `yaml:"owner"`
		Repository    string `yaml:"repository"`
		Branch        string `yaml:"branch"`
		BaseBranch    string `yaml:"base_branch"`
		BaseURL       string `yaml:"base_url"`
		CommitMessage string `yaml:"commit_message"`
		PRTitle       string `yaml:"pr_title"`
		PRBody        string `yaml:"pr_body"`
		AuthorName    string `yaml:"author_name"`
		AuthorEmail   string `yaml:"author_email"`
	} `yaml:"github"`
	ServicesGRPC  ServicesGRPC  `yaml:"grpc_clients"`
	Observability Observability `yaml:"observability"`
	Reconciler    Reconciler    `yaml:"reconciler"`
	JobFollower   JobFollower   `yaml:"job_follower"`
	StageWorker   StageWorker   `yaml:"stage_worker"`
	Router        Router        `yaml:"router"`
}

type Observability struct {
	Logs struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"logs"`
	Traces struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"traces"`
	Metrics struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"metrics"`
}

type Reconciler struct {
	Interval       time.Duration `yaml:"interval"`
	MinUploadedAge time.Duration `yaml:"min_uploaded_age"`
	BatchSize      int           `yaml:"batch_size"`
}

type JobFollower struct {
	Interval        time.Duration `yaml:"interval"`
	BatchSize       int           `yaml:"batch_size"`
	RescheduleAfter time.Duration `yaml:"reschedule_after"`
}

type StageWorker struct {
	Interval             time.Duration `yaml:"interval"`
	BatchSize            int           `yaml:"batch_size"`
	ConvertBatchSize     int           `yaml:"convert_batch_size"`
	PreprocessRetryDelay time.Duration `yaml:"preprocess_retry_delay"`
	RetryDelay           time.Duration `yaml:"retry_delay"`
	WaitMarkdown         bool          `yaml:"wait_markdown"`
	GracePeriod          time.Duration `yaml:"grace_period"`
}

type ServicesGRPC struct {
	Timeout   time.Duration   `yaml:"timeout"`
	Keepalive KeepaliveConfig `yaml:"keepalive"`
	Backoff   BackoffConfig   `yaml:"backoff"`
}

type KeepaliveConfig struct {
	Time                time.Duration `yaml:"time"`
	Timeout             time.Duration `yaml:"timeout"`
	PermitWithoutStream bool          `yaml:"permit_without_stream"`
}

type BackoffConfig struct {
	BaseDelay  time.Duration `yaml:"base_delay"`
	Multiplier float64       `yaml:"multiplier"`
	Jitter     float64       `yaml:"jitter"`
	MaxDelay   time.Duration `yaml:"max_delay"`
}

type Router struct {
	PreprocessEnabled        bool                   `yaml:"preprocess_enabled"`
	PreprocessExtensions     []string               `yaml:"preprocess_extensions"`
	TableExtractorEnabled    bool                   `yaml:"table_extractor_enabled"`
	TableExtractorExtensions []string               `yaml:"table_extractor_extensions"`
	ArtifactsBucket          string                 `yaml:"artifacts_bucket"`
	ArtifactsPrefixTemplate  string                 `yaml:"artifacts_prefix_template"`
	RepositoryRules          []RepositoryRouterRule `yaml:"repositories"`
	TableDetectionMinCount   int                    `yaml:"table_detection_min_count"`
}

type RepositoryRouterRule struct {
	Owner                    string   `yaml:"owner"`
	Repository               string   `yaml:"repository"`
	PathPrefix               string   `yaml:"path_prefix"`
	PreprocessEnabled        *bool    `yaml:"preprocess_enabled"`
	PreprocessExtensions     []string `yaml:"preprocess_extensions"`
	TableExtractorEnabled    *bool    `yaml:"table_extractor_enabled"`
	TableExtractorExtensions []string `yaml:"table_extractor_extensions"`
	ArtifactsBucket          string   `yaml:"artifacts_bucket"`
	ArtifactsPrefixTemplate  string   `yaml:"artifacts_prefix_template"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Validate проверяет обязательные поля и интервальные параметры конфигурации.
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr is required")
	}
	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required")
	}
	if c.Storage.OriginalBucket == "" || c.Storage.MarkdownBucket == "" {
		return fmt.Errorf("storage buckets configuration is incomplete")
	}
	if c.Services.PreprocessService == "" || c.Services.StorageService == "" || c.Services.TransformerService == "" || c.Services.GithubPublisherService == "" || c.Services.TableExtractorService == "" {
		return fmt.Errorf("services endpoints must be provided")
	}

	if c.ServicesGRPC.Timeout <= 0 {
		return fmt.Errorf("grpc_clients.timeout must be positive")
	}
	if c.ServicesGRPC.Keepalive.Time <= 0 || c.ServicesGRPC.Keepalive.Timeout <= 0 {
		return fmt.Errorf("grpc_clients.keepalive time and timeout must be positive")
	}
	if c.ServicesGRPC.Backoff.BaseDelay <= 0 || c.ServicesGRPC.Backoff.MaxDelay <= 0 {
		return fmt.Errorf("grpc_clients.backoff base_delay and max_delay must be positive")
	}
	if c.ServicesGRPC.Backoff.Multiplier <= 0 {
		return fmt.Errorf("grpc_clients.backoff multiplier must be positive")
	}
	if c.ServicesGRPC.Backoff.Jitter < 0 {
		return fmt.Errorf("grpc_clients.backoff jitter cannot be negative")
	}

	if c.Reconciler.Interval <= 0 || c.Reconciler.BatchSize <= 0 {
		return fmt.Errorf("reconciler.interval and batch_size must be positive")
	}
	if c.JobFollower.Interval <= 0 || c.JobFollower.BatchSize <= 0 {
		return fmt.Errorf("job_follower.interval and batch_size must be positive")
	}
	if c.StageWorker.ConvertBatchSize == 0 {
		c.StageWorker.ConvertBatchSize = 1
	}
	if c.StageWorker.PreprocessRetryDelay <= 0 {
		c.StageWorker.PreprocessRetryDelay = c.StageWorker.RetryDelay
	}
	if c.StageWorker.Interval <= 0 || c.StageWorker.BatchSize <= 0 || c.StageWorker.RetryDelay <= 0 || c.StageWorker.PreprocessRetryDelay <= 0 || c.StageWorker.ConvertBatchSize <= 0 {
		return fmt.Errorf("stage_worker interval, batch_size, convert_batch_size, retry_delay and preprocess_retry_delay must be positive")
	}
	if c.StageWorker.GracePeriod < 0 {
		return fmt.Errorf("stage_worker.grace_period cannot be negative")
	}
	if strings.TrimSpace(c.Github.CommitMessage) == "" {
		return fmt.Errorf("github.commit_message is required")
	}
	if strings.TrimSpace(c.Github.AuthorName) == "" || strings.TrimSpace(c.Github.AuthorEmail) == "" {
		return fmt.Errorf("github.author_name and github.author_email are required")
	}
	c.Router.normalize()
	if err := c.Router.validate(); err != nil {
		return fmt.Errorf("router: %w", err)
	}
	return nil
}

func (r *Router) normalize() {
	r.PreprocessExtensions = normalizeExtensions(r.PreprocessExtensions)
	if len(r.PreprocessExtensions) == 0 {
		r.PreprocessExtensions = []string{"doc", "pdf"}
	}
	r.TableExtractorExtensions = normalizeExtensions(r.TableExtractorExtensions)
	if len(r.TableExtractorExtensions) == 0 {
		r.TableExtractorExtensions = []string{"pdf"}
	}
	if strings.TrimSpace(r.ArtifactsPrefixTemplate) == "" {
		r.ArtifactsPrefixTemplate = "tables/{{ .DocumentID }}/{{ .QueueID }}/"
	}
	if r.TableDetectionMinCount <= 0 {
		r.TableDetectionMinCount = 1
	}
	for i := range r.RepositoryRules {
		r.RepositoryRules[i].PreprocessExtensions = normalizeExtensions(r.RepositoryRules[i].PreprocessExtensions)
		r.RepositoryRules[i].TableExtractorExtensions = normalizeExtensions(r.RepositoryRules[i].TableExtractorExtensions)
		if strings.TrimSpace(r.RepositoryRules[i].ArtifactsBucket) == "" {
			r.RepositoryRules[i].ArtifactsBucket = strings.TrimSpace(r.ArtifactsBucket)
		}
		if strings.TrimSpace(r.RepositoryRules[i].ArtifactsPrefixTemplate) == "" {
			r.RepositoryRules[i].ArtifactsPrefixTemplate = r.ArtifactsPrefixTemplate
		}
		r.RepositoryRules[i].PathPrefix = strings.TrimSpace(r.RepositoryRules[i].PathPrefix)
	}
}

func (r Router) validate() error {
	for i, rule := range r.RepositoryRules {
		if strings.TrimSpace(rule.Owner) == "" && strings.TrimSpace(rule.Repository) == "" && strings.TrimSpace(rule.PathPrefix) == "" {
			return fmt.Errorf("repositories[%d]: owner, repository or path_prefix is required", i)
		}
	}
	return nil
}

func normalizeExtensions(values []string) []string {
	result := make([]string, 0, len(values))
	for _, val := range values {
		ext := strings.TrimSpace(val)
		if ext == "" {
			continue
		}
		ext = strings.TrimPrefix(ext, ".")
		ext = strings.ToLower(ext)
		result = append(result, ext)
	}
	return result
}
