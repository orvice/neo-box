# Neo Box Frontend

React + TypeScript + Vite dashboard for Neo Box, built on the
[shadcn-admin](https://github.com/satnaing/shadcn-admin) layout.

- `src/routes/` — TanStack Router file routes (`routeTree.gen.ts` is generated)
- `src/features/` — page implementations
- `src/api/` — ConnectRPC clients + React Query hooks (Bearer interceptor)
- `src/gen/` — protobuf-es output from `buf generate` (do not edit)
- `src/components/ui/` — shadcn/ui primitives
- `src/context/`, `src/stores/` — providers (theme, layout) and the auth store

## Development

```bash
# Backend (from repo root)
BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.yaml go run ./cmd/neobox

# Frontend
cd front
npm install
npm run dev   # http://localhost:5173, proxies /api to :8080
```

`npm run build`, `npm run lint`, and `npm run format` are the other scripts.
