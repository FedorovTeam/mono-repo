package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config описывает параметры фонового воркера публикации.
type Config struct {
	DSN          string        `yaml:"dsn"`
	Stage        string        `yaml:"stage"`
	PollInterval time.Duration `yaml:"poll_interval"`
	MaxAttempts  int           `yaml:"max_attempts"`
	Backoff      time.Duration `yaml:"backoff"`
}

type job struct {
	TaskID     string
	DocumentID string
	Stage      string
	Status     string
	Attempts   int
	NextRunAt  time.Time
	Payload    map[string]any
}

// Worker обрабатывает задания публикации документов в GitHub.
type Worker struct {
	pool         *pgxpool.Pool
	pollInterval time.Duration
	maxAttempts  int
	backoff      time.Duration
	stage        string
}

// New создаёт воркер и соединение с БД.
func New(ctx context.Context, cfg Config) (*Worker, error) {
	if cfg.DSN == "" {
		return nil, errors.New("processing.dsn is required")
	}

	stage := cfg.Stage
	if stage == "" {
		stage = "publish_to_github"
	}

	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 5 * time.Second
	}

	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = 5 * time.Second
	}

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, err
	}

	return &Worker{
		pool:         pool,
		pollInterval: poll,
		maxAttempts:  maxAttempts,
		backoff:      backoff,
		stage:        stage,
	}, nil
}

// Close освобождает ресурсы пула соединений.
func (w *Worker) Close() {
	if w.pool != nil {
		w.pool.Close()
	}
}

// Run запускает бесконечный цикл обработки заданий.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	w.processLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processLoop(ctx)
		}
	}
}

func (w *Worker) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			log.Printf("worker: begin tx: %v", err)
			return
		}

		job, err := w.selectJob(ctx, tx)
		if err != nil {
			tx.Rollback(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return
			}
			log.Printf("worker: select job: %v", err)
			return
		}

		if err := w.handleJob(ctx, tx, job); err != nil {
			_ = tx.Rollback(ctx)
			log.Printf("worker: handle job %s: %v", job.TaskID, err)
			continue
		}

		if err := tx.Commit(ctx); err != nil {
			log.Printf("worker: commit job %s: %v", job.TaskID, err)
		}
	}
}

func (w *Worker) selectJob(ctx context.Context, tx pgx.Tx) (*job, error) {
	row := tx.QueryRow(ctx, `
        SELECT task_id, document_id, stage, status, attempts, next_run_at, payload
        FROM processing_jobs
        WHERE stage=$1 AND status='pending' AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, w.stage)

	var payloadRaw []byte
	j := &job{}
	if err := row.Scan(&j.TaskID, &j.DocumentID, &j.Stage, &j.Status, &j.Attempts, &j.NextRunAt, &payloadRaw); err != nil {
		return nil, err
	}

	_ = json.Unmarshal(payloadRaw, &j.Payload)
	return j, nil
}

func (w *Worker) handleJob(ctx context.Context, tx pgx.Tx, j *job) error {
	if _, err := tx.Exec(ctx, `UPDATE processing_jobs SET status='in_progress', updated_at=now() WHERE task_id=$1`, j.TaskID); err != nil {
		return err
	}

	if err := w.insertEvent(ctx, tx, j.TaskID, "in_progress", "processing", "picked job"); err != nil {
		return err
	}

	if err := w.markDocument(ctx, tx, j.DocumentID, "PublishedToGitHub", j.Payload); err != nil {
		return w.failJob(ctx, tx, j, err)
	}

	if _, err := tx.Exec(ctx, `UPDATE processing_jobs SET status='completed', updated_at=now() WHERE task_id=$1`, j.TaskID); err != nil {
		return err
	}
	return w.insertEvent(ctx, tx, j.TaskID, "completed", "completed", "published to github")
}

func (w *Worker) failJob(ctx context.Context, tx pgx.Tx, j *job, reason error) error {
	j.Attempts++
	status := "pending"
	nextRun := time.Now().Add(time.Duration(j.Attempts) * w.backoff)
	if j.Attempts >= w.maxAttempts {
		status = "failed"
	}

	payload := j.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payload["last_error"] = reason.Error()

	payloadBytes, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `
        UPDATE processing_jobs
        SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=now()
        WHERE task_id=$5
    `, status, j.Attempts, nextRun, payloadBytes, j.TaskID); err != nil {
		return err
	}

	if err := w.insertEvent(ctx, tx, j.TaskID, status, "retry", reason.Error()); err != nil {
		return err
	}

	return nil
}

func (w *Worker) markDocument(ctx context.Context, tx pgx.Tx, documentID, status string, payload map[string]any) error {
	_, err := tx.Exec(ctx, `
        UPDATE documents
        SET status=$1, error_reason=NULL, updated_at=now()
        WHERE id=$2
    `, status, documentID)
	if err != nil {
		return err
	}

	correlationID, _ := payload["correlation_id"].(string)
	_, err = tx.Exec(ctx, `
        INSERT INTO document_status_logs (id, document_id, from_status, to_status, workflow_id, reason, created_at)
        VALUES (gen_random_uuid(), $1, NULL, $2, $3, $4, now())
    `, documentID, status, correlationID, "processed by github worker")
	return err
}

func (w *Worker) insertEvent(ctx context.Context, tx pgx.Tx, jobID, status, eventType, message string) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
        VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}'::jsonb, now())
    `, jobID, w.stage, status, eventType, message)
	return err
}
