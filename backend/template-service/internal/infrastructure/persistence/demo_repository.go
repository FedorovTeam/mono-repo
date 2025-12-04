package persistence

import (
	"context"
	"database/sql"
	"template-service/internal/domain"
)

type DemoRepository struct {
	db *sql.DB
}

func NewDemoRepository(db *sql.DB) *DemoRepository {
	return &DemoRepository{db: db}
}

func (r *DemoRepository) Save(entity *domain.DemoEntity) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO demo_entity (id, bucket_id, region_id, value, bucket) VALUES ($1, $2, $3, $4, $5)`,
		entity.ID, string(entity.BucketID), string(entity.RegionID), entity.Value, entity.Bucket,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *DemoRepository) FindByID(id string) (*domain.DemoEntity, error) {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT id, bucket_id, region_id, value, bucket FROM demo_entity WHERE id = $1`, id)
	var e domain.DemoEntity
	err = row.Scan(&e.ID, &e.BucketID, &e.RegionID, &e.Value, &e.Bucket)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
