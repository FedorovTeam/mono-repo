Если миграции не работают или сломались запустить:
```shell
kubectl -n typebot run prisma-resolve \
  --rm -it --restart=Never \
  --image=baptistearno/typebot-builder:latest \
  --command -- /bin/sh -lc '
set -eu
cd /app
export DATABASE_URL="postgresql://typebot:typebot_pass@postgres-postgresql.postgres.svc.cluster.local:5432/typebot"

SCHEMA="packages/prisma/postgresql/schema.prisma"
MIGR_DIR="packages/prisma/postgresql/migrations"

if [ ! -f "$SCHEMA" ]; then
  echo "schema not found at $SCHEMA"
  ls -la packages/prisma/postgresql || true
  exit 1
fi

echo "== status (before) =="
npx prisma migrate status --schema "$SCHEMA" || true

echo "== resolve all migrations as applied =="
for m in $(ls -1 "$MIGR_DIR" | sort); do
  echo "Mark as applied: $m"
  npx prisma migrate resolve --applied "$m" --schema "$SCHEMA"
done

echo "== status (after) =="
npx prisma migrate status --schema "$SCHEMA" || true

echo "== deploy (idempotent) =="
npx prisma migrate deploy --schema "$SCHEMA"
'
```