# Database Migrations

Simple SQL migration runner that tracks applied migrations in `schema_migrations`.

## How it works

1. Reads `*.up.sql` files from `/migrations` (or `MIGRATIONS_DIR`)
2. Skips files already recorded in `schema_migrations`
3. Applies remaining files in alphabetical order
4. Records each successful migration

## Local

```bash
export DATABASE_URL="postgres://nimbus:password@localhost:5432/nimbus?sslmode=disable"
export MIGRATIONS_DIR="./migrations"
go run ./cmd/migrator
```

## Production (CI)

The GitHub Actions workflow runs migrations as a one-off ECS Fargate task before deploying. It reuses the same VPC/subnets/security groups as the main service.

If the migration fails, the deploy is blocked. Check CloudWatch logs at `/ecs/nimbus-prod` with stream prefix `migrator`.

## Adding a new migration

```bash
touch migrations/003_add_something.up.sql
touch migrations/003_add_something.down.sql
# Write SQL, then test locally
go run ./cmd/migrator
```

Migrations are idempotent—running the migrator multiple times is safe.
