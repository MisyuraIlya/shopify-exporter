## excepted file structure

```
integration-service/
├── cmd/
│   ├── sync-to-shopify/            # Daily job: API X -> Shopify
│   │   └── main.go
│   ├── sync-orders/                # Every 5 min job: Shopify -> DB -> API X
│   │   └── main.go
│   └── worker/                     # OPTIONAL: one binary that can run both by flags
│       └── main.go
│
├── internal/
│   ├── app/                        # Application orchestration (job flows)
│   │   ├── jobs/
│   │   │   ├── daily_to_shopify.go
│   │   │   ├── orders_from_shopify.go
│   │   │   ├── lock.go             # prevents double-run
│   │   │   └── status.go           # sync run logs, metrics, error tracking
│   │   ├── usecases/
│   │   │   ├── sync_categories.go
│   │   │   ├── sync_products.go
│   │   │   ├── sync_prices.go
│   │   │   ├── fetch_orders.go
│   │   │   ├── store_orders.go
│   │   │   └── push_orders_to_api_x.go
│   │   └── pipeline/
│   │       ├── to_shopify_pipeline.go
│   │       └── orders_pipeline.go
│   │
│   ├── domain/                     # Stable business objects + interfaces
│   │   ├── model/
│   │   │   ├── category.go
│   │   │   ├── product.go
│   │   │   ├── price.go
│   │   │   ├── order.go
│   │   │   └── sync_run.go
│   │   ├── ports/                  # Interfaces (clean architecture)
│   │   │   ├── apix.go              # API X client interface
│   │   │   ├── shopify.go           # Shopify client interface
│   │   │   ├── orders_repo.go       # DB repo interface
│   │   │   ├── lock.go              # lock interface
│   │   │   └── syncstate.go         # run logs & mapping storage
│   │   └── errors/
│   │       └── errors.go
│   │
│   ├── adapters/                   # Implementations for the ports
│   │   ├── apix/
│   │   │   ├── client.go
│   │   │   ├── categories.go
│   │   │   ├── products.go
│   │   │   ├── prices.go
│   │   │   └── orders.go            # sending orders back to API X
│   │   ├── shopify/
│   │   │   ├── client.go
│   │   │   ├── categories.go        # upsert collections/categories
│   │   │   ├── products.go
│   │   │   ├── prices.go
│   │   │   ├── orders.go            # fetch orders
│   │   │   ├── rate_limit.go
│   │   │   └── retry.go
│   │   ├── repository/
│   │   │   └── mysql/
│   │   │       ├── db.go
│   │   │       ├── orders_repo.go
│   │   │       ├── mappings_repo.go # internal_id -> shopify_id
│   │   │       └── sync_runs_repo.go
│   │   ├── lock/
│   │   │   ├── redis/
│   │   │   │   └── lock.go
│   │   │   └── mysql/
│   │   │       └── lock.go          # optional if no redis
│   │   └── logging/
│   │       └── logger.go
│   │
│   ├── config/
│   │   ├── config.go                # config struct
│   │   └── env.go                   # read env vars, validate
│   │
│   └── infra/
│       ├── http/
│       │   └── http.go              # shared HTTP client tuning, timeouts
│       ├── metrics/
│       │   └── metrics.go           # optional Prometheus / OpenTelemetry
│       └── migrations/
│           ├── 001_sync_runs.sql
│           ├── 002_mappings.sql
│           └── 003_orders_state.sql
│
├── deployments/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   └── cron/
│       ├── daily_sync.cron
│       └── orders_sync.cron
│
├── scripts/
│   ├── run_daily.sh
│   ├── run_orders.sh
│   └── migrate.sh
│
├── test/
│   ├── integration/
│   └── fixtures/
│
├── go.mod
├── go.sum
└── README.md
```