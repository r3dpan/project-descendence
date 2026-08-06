# project-descendence

## Running it

Each `go run`/`npm run dev` command below blocks, so run each in its own terminal.

```bash
# load .env into the shell - required, nothing reads .env itself
set -a; source .env; set +a

# api server (:8080)
go run ./cmd/api

# supervisor (separate terminal, same .env loaded)
go run ./cmd/supervisor

# web UI, dev mode with hot reload (:5173, proxies /api to :8080)
cd web && npm install && npm run dev

# web UI, production build (served by cmd/api itself at :8080; rebuild + restart cmd/api to see it)
cd web && npm run build

# CLI / TUI - needs DESCENDENCE_URL + DESCENDENCE_TOKEN
# (token from `go run ./cmd/seed -role admin`, once against a fresh DB)
export DESCENDENCE_URL=http://localhost:8080 DESCENDENCE_TOKEN=<token>
go run ./cmd/cli
```
