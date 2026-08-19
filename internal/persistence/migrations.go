package persistence

import "embed"

// Migrations embeds the goose SQL migration files so the binary carries
// its own schema and does not depend on a filesystem path at runtime.
//
//go:embed migrations/*.sql
var Migrations embed.FS
