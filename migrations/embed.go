package migrations

import "embed"

// FS holds SQL migrations. Node config is not included.
//
//go:embed sql/*.sql
var FS embed.FS
