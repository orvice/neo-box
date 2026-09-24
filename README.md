# neo-box

A personal hub for managing and viewing third-party resources.

Monorepo: Go backend (Butterfly + Gin + ConnectRPC + MongoDB), React
dashboard in `front/`, protobuf contracts in `proto/` generated with buf.

## Quick start

```bash
# MongoDB
docker run -d --name neobox-mongo -p 27017:27017 mongo:8

# Backend — creates admin / change-me on first start (see config.yaml)
BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.yaml go run ./cmd/neobox

# Frontend
cd front && npm install && npm run dev
```

Open http://localhost:5173 and sign in.

## Layout

```
cmd/neobox/          entry point
internal/app/        route registration + bootstrap wiring
internal/application ConnectRPC service implementations
internal/repo/       repositories (MongoDB)
internal/auth/       OAuth providers
proto/neobox/v1/     protobuf API
pkg/proto/           generated Go (do not edit)
front/               React dashboard (front/src/gen is generated)
```

`make buf` regenerates code after proto changes. See `AGENTS.md` for more.
