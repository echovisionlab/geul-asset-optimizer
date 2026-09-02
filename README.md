# Geul asset optimizer

Go worker for GLB mesh optimization. It consumes
`api.manage.v1.MeshOptimizationJob`, optimizes the requested source object, and
publishes the result and progress through the shared Geul contracts.

## Protocol contract

The worker uses `github.com/echovisionlab/geul-event-contracts`. These wire and
storage identifiers are intentionally unchanged for compatibility:

- input queue: `asset_optimizer.mesh`
- progress signal: `mesh.optimization.progress`
- result queue: `mesh.optimization.result`
- output object key: `media/{fileId}.glb`

The API remains the source of truth for file records, source/output IDs, stale
guards, and result association. Queue names, signal names, object keys, and
other wire identifiers are not derived from the repository name.

## Optimization profiles

- `draco-webp-v1`: general GLB optimization with Draco geometry compression and
  WebP textures.
- `particle-mesh-v1`: position/index-only particle geometry using the packaged
  `scripts/optimize-particle-mesh.mjs` pipeline.

The MVP accepts GLB inputs up to 50 MB, target ratios from 1 through 100, and a
single worker. Ratio 100 skips simplification while retaining cleanup and
compression. The pinned runtime uses Node.js 24.19.0 and
`@gltf-transform/cli@4.4.2`.

## Environment

See [`.env.example`](.env.example). The service expects `DATABASE_DSN`,
`S3_*`, and `OTEL_*` settings. PostgreSQL/PGMQ and S3-compatible object
storage are external runtime dependencies. The example DSN uses the Geul
database and `geul_asset_optimizer` service login; deployment supplies its
credentials.

## Development

```sh
go mod download
npm ci
go test ./...
npm test
go build ./cmd/asset-optimizer
```

Validate the runtime reference with:

```sh
npm run check:yaml
npm run check:compose
npx --yes @gltf-transform/cli@4.4.2 inspect testdata/triangle.glb --format csv
```

## Release image

The release workflow publishes the candidate image
`registry.dsub.io/echovisionlab/geul-asset-optimizer:v0.1.0`. It builds from
the exact Release Please tag and full release commit SHA, publishes matching
version and `sha-<full-release-sha>` tags, verifies their immutable digest, and
smoke-tests that digest. It does not deploy a runtime. CI uses GitHub-hosted
runners, public Go modules, native dependency caches, and no uploaded artifacts.

## License and maintainer

Licensed under PolyForm Noncommercial 1.0.0; commercial use requires a
separate license from Echo Vision Lab. Maintainer: state303
<state303@dsub.io>.
