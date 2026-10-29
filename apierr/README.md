# `github.com/klarlabs-studio/glossa/apierr`

Framework-agnostic JSON error envelope for Go HTTP services that want
to ship error responses both `curl`-friendly and `glossa-text`-friendly.

## Why

A glossa-aware service has two audiences for its error responses:

- **Logs, third-party API clients, curl, debugging** — want a stable
  machine-readable code and a default English literal that's safe to
  splat anywhere.
- **Web / mobile clients** — want a translation key + params they can
  resolve against the user's locale bundle.

This package emits both in one envelope so callers don't have to pick.

## Wire shape

```json
{
  "error": {
    "code":    "validation_email_required",
    "message": "Email address is required",
    "key":     "validation.email.required",
    "params":  { "field": "email" },
    "status":  400
  }
}
```

| Field     | Purpose                                                            | Consumer                            |
| --------- | ------------------------------------------------------------------ | ----------------------------------- |
| `code`    | stable identifier — never renamed once shipped                     | logs, alerting, third-party clients |
| `message` | canonical English literal — what gets logged + non-i18n clients see| logs, curl, third-party             |
| `key`     | glossa translation key the frontend resolves                       | web / mobile                        |
| `params`  | interpolation values (`{field}` etc.)                              | web / mobile                        |
| `status`  | HTTP status echoed for clients that don't read headers             | mobile SDKs, browser fetch wrappers |

## Quickstart

Install:

```bash
go get github.com/klarlabs-studio/glossa/apierr
```

Declare errors once, in a registry:

```go
// internal/errs/registry.go
package errs

import (
    "net/http"
    "github.com/klarlabs-studio/glossa/apierr"
)

var (
    ValidationEmailRequired = apierr.New(
        "validation_email_required",
        "validation.email.required",
        "Email address is required",
        http.StatusBadRequest,
    )
    AuthInvalidCredentials = apierr.New(
        "auth_invalid_credentials",
        "auth.invalid_credentials",
        "Invalid email or password",
        http.StatusUnauthorized,
    )
)
```

Use at the call site (gin example via the `ginerr` subpackage):

```go
import "github.com/klarlabs-studio/glossa/apierr/ginerr"

if email == "" {
    ginerr.Send(c, errs.ValidationEmailRequired.WithParam("field", "email"))
    return
}
```

For non-typed errors bubbling up from deeper layers:

```go
out, err := uc.Execute(ctx, in)
if err != nil {
    ginerr.SendErr(c, err) // unwraps to *apierr.Error or wraps as 500
    return
}
```

## Client-side resolution

The `resolveApiError` helper of the retired v0.3 `@felixgeelhaar/glossa-sdk`
(see `runtimes/js/elements/MIGRATION.md`) took the envelope and returned the localised string:

```ts
import { resolveApiError } from "<your resolver>";

const res = await fetch("/api/v1/admin/projects", { ... });
if (!res.ok) {
  const body = await res.json();
  const message = resolveApiError(body, {
    locale: "de",
    messages: glossaProvider.messages, // your bundle
  });
  toast(message);
}
```

The resolver falls back to the server-supplied English `message` on
bundle miss, and to `"Unknown error"` on malformed input — it never
throws.

## Design notes

- The base package has **zero external dependencies**. Framework
  adapters (gin in `apierr/ginerr`, echo / fiber / net/http to follow)
  live in subpackages so importers only pull in what they use.
- `WithParam` / `WithParams` / `WithMessage` / `WithStatus` / `Wrap`
  all return clones; the registry-level `var ValidationEmailRequired`
  stays safe under concurrent use.
- `Error()` returns the English `Message` so apierr values compose
  with `errors.Is` / `errors.As` and slog / zerolog formatters.

## Sibling packages

- **Frontend resolver** — was `@felixgeelhaar/glossa-sdk` (retired with v0.3).
- **Gin adapter** — `github.com/klarlabs-studio/glossa/apierr/ginerr`.
