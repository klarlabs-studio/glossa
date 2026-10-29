# M3 exit test — report

Written by `TestM3Exit` (`make system-m3`, or `go test -tags=system ./internal/systemtest/m3/...` in `platform/`)
against a real glossa-server on Postgres and MinIO, glossa-edge on the same bucket, a fake GitHub on loopback and
a headless Chrome the test starts. Every number below comes from the public API, from GitHub's own view of the
pull request, or from the browser; the test fails when an exit criterion does not hold. RFC 0004 §1, §12.

## The application

- **150 messages** of Brotwerk's shop (seed 20260920, `internal/systemtest/m3/fixture`), source `de`, complete `en`, `es`,
  `fr` and `ja`: 65 labels, 36 rendered by `<GlossaText>`/`<T>`, 13 plurals, 25 with a `$name`, 11 in a placeholder
  attribute, 11 with markup.
- A **Vite + Vue** app over 11 source files and **8 routes**, with **one React island** (`PayButton.tsx`) on
  `/kasse`, built by `@klarlabs-studio/glossa/unplugin`. A Go receipt renderer and its `text/template` reuse 15 of the same
  messages, which is what `glossa extract` reads.
- `glossa push --translations` created 150 messages and 600 translations.

| Route | Page | Messages |
|---|---|---|
| `/` | `src/pages/HomePage.vue` | 18 |
| `/produkte` | `src/pages/ProductsPage.vue` | 20 |
| `/produkte/[id]` | `src/pages/ProductPage.vue` | 18 |
| `/warenkorb` | `src/pages/CartPage.vue` | 18 |
| `/kasse` | `src/pages/CheckoutPage.vue` | 20 |
| `/konto` | `src/pages/AccountPage.vue` | 16 |
| `/hilfe` | `src/pages/HelpPage.vue` | 14 |
| `/rechnung` | `src/pages/InvoicePage.vue` | 12 |
| _(every route)_ | `src/components/SiteHeader.vue`, `src/components/SiteFooter.vue` | 14 |

## 1. Where every message appears

Three uploads for one commit (`3c1a5f7`):

| Collector | Source | Usages | Unknown keys |
|---|---|---|---|
| `@klarlabs-studio/glossa/unplugin` (the Vite build) | `plugin` | 150 | 0 |
| `glossa extract` (Go and templates) | `extract` | 15 | 0 |
| `glossa capture` (headless Chrome) | `capture` | 32 captures | 0 |

`glossa capture` drove the preview build over 8 routes × German and Japanese × 1280×800 and 390×844 = **32 captures**, 32 images stored and 0
deduplicated.

### Coverage

| Criterion (RFC 0004 §1) | Result | Bar |
|---|---|---|
| Active messages with ≥ 1 usage | 150 / 150 | all |
| … naming a file, a line **and** a component | 150 / 150 | all |
| … with ≥ 1 **visible** region | 150 / 150 | all |
| Unused active messages | 0 | 0 |
| Messages `glossa capture` reported as not captured | 0 | 0 |

165 usages and 992 regions in all (992 visible). By kind: `component` 36, `t` 121, `template` 8. By collector: `extract` 15, `plugin` 150.

Visible regions per capture locale: `de` 496, `ja` 496. Per route: `/` 128, `/hilfe` 112, `/kasse` 136, `/konto` 120, `/produkte` 136, `/produkte/[id]` 128, `/rechnung` 104, `/warenkorb` 128.

One capture's image was read back through the API (`…/captures/{capture}/image`): `image/png`, its digest as the
`ETag`, `Cache-Control: private`.

## 2. The pull request

`feature/pickup-copy`, pull request #7 on `brotwerk/shop`, head `7d0e4b1`. A fake GitHub signs the webhooks and serves the App's endpoints;
everything else is the real server.

| Step | What the platform did |
|---|---|
| pull_request.opened | branch feature/pickup-copy opened, pr-7 created, Glossa check queued |
| CI push (`7d0e4b1`): 5 new keys, 1 invalid | 4 keys proposed; `pickup.reminder` refused (`invalid_message`), 155 usages uploaded with it as an unknown key |
| Glossa check on `7d0e4b1` | failure — "3 problems and 1 warning", 1 annotation at `src/pages/CheckoutPage.vue:35`, one sticky comment |
| second CI push (`b58f3a0`) + in-context edits | `pickup.reminder` fixed; 15 translations written with a `glossa_ctx_…` grant bound to the preview deployment's origin |
| Glossa check on `b58f3a0` | success — "Localization is ready", the same comment updated in place |
| branch environment | pr-7 published; the preview key reads it, the production key gets 404 |
| merge + default-branch push | 5 proposed messages activated |
| pull_request.closed (merged) | pr-7 destroyed; the edge answers 404 |

### The check

| Run | Head | Status | Conclusion | Title | Annotations |
|---|---|---|---|---|---|
| 1 | 7d0e4b1 | completed | `failure` | 3 problems and 1 warning | 1 annotation |
| 2 | b58f3a0 | completed | `success` | Localization is ready | 0 annotations |

The first run refused `pickup.reminder`: its plural has no catch-all variant, so the push never stored it, its usage stayed an
unknown key, and the check annotated the line the product's own source uses it on — `src/pages/CheckoutPage.vue:35`. The repair was an
ordinary CI push; the 15 missing Spanish, French and Japanese translations were written through an **in-context
grant** (`glossa_ctx_…`, 15 minutes, bound to the project and to the preview deployment's origin, with
`translations.write` among its permissions) — the same call the in-product editor makes. The same grant was
refused on the tenant's API tokens and from another origin.

One sticky comment existed throughout: it was updated in place, never duplicated, and no annotation was sent twice.

### The branch environment

| Delivery key | Environment | Through glossa-edge | When |
|---|---|---|---|
| preview | `pr-7` | 200 | while the pull request is open |
| production | `pr-7` | 404 | while the pull request is open |
| preview | `pr-7` | 404 | after the pull request closed |

The default-branch push that follows the merge activated all 5 proposed messages; `pull_request.closed` destroyed
`pr-7`, and the manifest the preview key had just read became a 404.

## 3. The in-product editor is never in production

| Build | Files | Bytes | Loader markers found |
|---|---|---|---|
| `preview` | 3 | 586333 | `/overlay/v1/overlay.js`, `data-glossa-overlay-loader`, `https://studio.glossa.test`, `sha384-` |
| `production` | 3 | 582001 | none |

The markers are what only the loader puts into a bundle: the attribute it sets on the script it adds, the
overlay's path on the Studio origin, that origin, and the SRI hash pinned at build time. The preview build holds
all four — otherwise the production scan would prove nothing — and the production build none.

The same preview build, the one that **does** carry the loader, was then driven in a real browser with the
`?glossa=edit` gesture:

| Release the runtime activated | Loader scripts on the page | Overlay registered |
|---|---|---|
| `preview` | 1 | — |
| `production` | 0 | no |

Against the production manifest the loader added nothing and said so: "the in-product editor stays off: a runtime serves a production release".

## 4. Documents in five locales

The Go runtime's own document tests and the fpdf example ran against `runtimes/go/testdata/documents`, which owns
the fixture and its goldens; the exit test runs them rather than keeping a second copy.

- Documents: `export`, `tax-summary`.
- Locales: `de`, `en`, `es`, `fr`, `ja`.
- Goldens compared: 15 — one HTML file per document and locale, plus one `Runs` file per locale.
- `examples/pdf` rendered 10 PDFs with the Noto fonts (2 documents × 5 locales).

| Test | Documents | Locales |
|---|---|---|
| `documents (HTML and Runs goldens)` | 2 | 5 |
| `examples/pdf (Noto fonts)` | 2 | 5 |

