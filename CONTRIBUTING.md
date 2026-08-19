# Contributing

Read `docs/migration-plan.md`, `API_CONTRACT.md`, and `METRICS_CONTRACT.md`
before changing behavior. Preserve provenance on every displayed measurement;
do not invent HAMi metrics or use fabricated values.

Run `npm run build` and `npm test` from `web/` for frontend changes. Backend
changes must include the relevant Go tests. Do not commit secrets, generated
run artifacts, or production claims unsupported by passing tests.
