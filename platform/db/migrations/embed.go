// Package migrations embeds glossa-server's SQL migrations
// (golang-migrate format: NNNN_name.up.sql / NNNN_name.down.sql).
//
// One stream for the whole server (RFC 0002 §6), but each migration
// touches only its own bounded context's tables. Every tenant-owned
// table must ENABLE and FORCE row-level security with a policy on
// app_current_tenant(); the RLS guard test fails the build otherwise.
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS
