# M2 exit test — report

Written by `TestM2Exit` (`make system-m2`, or `go test -tags=system ./internal/systemtest/...` in `platform/`) against a real
glossa-server on Postgres and MinIO, a deterministic fake provider and glossa-edge. Every number below comes from the
public API after the run; the test fails when an exit criterion does not hold. RFC 0003 §1, intent §24, §69–70.

## Fixture

- 600 messages of a SaaS product (seed 20260919, `internal/systemtest/m2/fixture`), source `de`, complete `en`;
  25 plural counts and 25 plural selections (es/fr need `many`), 25 selects, 25 with markup, 50 activity lines with `$name`,
  200 with a max length, 40 legal clauses in the `legal` namespace (tagged sensitive).
- Termbase: 20 concepts, 288 terms (113 forbidden), imported as TBX. Style guides: 5 (formal es/fr/ja, an errors namespace guide).
- Legacy memory (TMX, tenant-wide): 58 exact, 40 fuzzy, 8 exact with a now-forbidden term.

## Launch summary

```text
Spanish (es)

600 messages

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Translated before the fill               432
Translation memory applied                15
AI translated                            151
  after a structural repair                5
Invalid output, not suggested              2
Refused: sensitive namespace               0
Human review required                     11
  length heuristic (clean draft)           1
  slip: forbidden_term                     3
  slip: formality                          2
  slip: plural_missing                     3
  slip: too_long                           2

Structural QA        ✓ 155 of 155 recommended valid
Terminology QA       ✓ no forbidden term recommended
Slips caught         ✓ 10 of 10

Coverage (approved)                   72.0 % → 97.8 %
High-confidence (approve_recommended)  93.4 %
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

11 messages need review.
```

```text
French (fr)

600 messages

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Translated before the fill               300
Translation memory applied                24
AI translated                            253
  after a structural repair                9
Invalid output, not suggested              3
Refused: sensitive namespace              20
Human review required                     17
  slip: forbidden_term                     5
  slip: formality                          4
  slip: plural_missing                     5
  slip: too_long                           3

Structural QA        ✓ 260 of 260 recommended valid
Terminology QA       ✓ no forbidden term recommended
Slips caught         ✓ 17 of 17

Coverage (approved)                   50.0 % → 93.3 %
High-confidence (approve_recommended)  93.9 %
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

17 messages need review.
```

```text
Japanese (ja)

600 messages

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Translated before the fill                 0
Translation memory applied                54
AI translated                            501
  after a structural repair                7
Invalid output, not suggested              5
Refused: sensitive namespace              40
Human review required                     33
  length heuristic (clean draft)           8
  slip: forbidden_term                    10
  slip: formality                          7
  slip: too_long                           5
  structural repair                        3

Structural QA        ✓ 522 of 522 recommended valid
Terminology QA       ✓ no forbidden term recommended
Slips caught         ✓ 22 of 22

Coverage (approved)                    0.0 % → 87.0 %
High-confidence (approve_recommended)  94.1 %
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

33 messages need review.
```

## Exit criteria

| Criterion | es | fr | ja | Bar |
|---|---|---|---|---|
| Human review required, share of filled | 11 / 166 = 6.6 % | 17 / 277 = 6.1 % | 33 / 555 = 5.9 % | ≤ 15 % |
| Review ≤ slips + repairs + length-flagged | 11 ≤ 13 | 17 ≤ 21 | 33 ≤ 44 | holds |
| Surviving slips routed review_required | 10 / 10 | 17 / 17 | 22 / 22 | all |
| Structural defects in approve_recommended | 0 of 155 | 0 of 260 | 0 of 522 | 0 |
| Forbidden terms in approve_recommended | 0 | 0 | 0 | 0 |
| Sensitive (legal) messages sent to the provider | 0 (0 refused) | 0 (20 refused) | 0 (40 refused) | 0 |
| Accepted revisions with full provenance | 155 / 155 | 260 / 260 | 522 / 522 | all |
| Invalid output kept out of the queue | 2 / 2 | 3 / 3 | 5 / 5 | all |

The bar on review follows from the script: every slip that reaches a suggestion must be reviewed, a draft that
needed a structural repair scores below `recommend_min`, and a clean draft without strong TM support is pushed just
under it (0.745 < 0.75) by the length heuristic. The fixture predicts which drafts the heuristic flags; nothing else
may need review, and the total stays under 15 % of what the platform filled.

## Slips

| Slip | es | fr | ja | Outcome |
|---|---|---|---|---|
| forbidden_term | 3 | 5 | 10 | `term_forbidden` → review_required |
| formality | 2 | 4 | 7 | reviewer model flags formality → review_required |
| plural_missing | 3 | 5 | 0 | repaired twice, still missing `many` → review_required |
| too_long | 2 | 3 | 5 | `max_length` → review_required |
| placeholder_repaired | 2 | 4 | 7 | fixed on the repair turn; `repairs` lowers the score |
| placeholder_persistent | 2 | 3 | 5 | `invalid_output`: never a suggestion |

8 legacy TM units with a forbidden term matched exactly and were not reused: the agent drafted those messages.

## Review queue

998 suggestions were pending after the fill: 61 `review_required` first, then 937 `approve_recommended`
(accepted as is by the test). The head of the queue:

| # | Locale | Key | Score | Risk tags | Why |
|---|---|---|---|---|---|
| 1 | es | `billing.plan.total` | 0.000 | term_forbidden | forbidden_term |
| 2 | fr | `billing.discount_code.selected` | 0.000 | term_forbidden | forbidden_term |
| 3 | fr | `billing.invoice.selected` | 0.000 | term_forbidden | forbidden_term |
| 4 | fr | `notifications.file.selected` | 0.000 | term_forbidden | forbidden_term |
| 5 | fr | `auth.passkey.count` | 0.014 | missing_plural_categories | plural_missing |
| 6 | es | `auth.password.selected` | 0.065 | missing_plural_categories | plural_missing |
| 7 | es | `billing.discount_code.selected` | 0.065 | missing_plural_categories | plural_missing |
| 8 | es | `notifications.notification.count` | 0.065 | missing_plural_categories | plural_missing |
| 9 | fr | `auth.api_key.selected` | 0.065 | missing_plural_categories | plural_missing |
| 10 | fr | `billing.subscription.selected` | 0.065 | missing_plural_categories | plural_missing |
| 11 | fr | `dashboard.chart.selected` | 0.065 | missing_plural_categories | plural_missing |
| 12 | fr | `settings.workspace.selected` | 0.065 | missing_plural_categories | plural_missing |
| 13 | es | `dashboard.project.delete` | 0.264 | term_forbidden | forbidden_term |
| 14 | es | `notifications.reminder.all` | 0.264 | max_length | too_long |
| 15 | fr | `auth.password.new` | 0.264 | max_length | too_long |

## Provider calls and spend

| | es | fr | ja |
|---|---|---|---|
| Draft calls (translate and repair) | 165 | 276 | 523 |
| Prompts with the style guide's form of address | 153 | 256 | 506 |
| Prompts with glossary entries | 119 | 185 | 370 |
| Prompts with translation-memory matches | 119 | 192 | 241 |

1869 provider calls (915 translate, 49 repair, 905 assess), each disclosed; none carried legal text.
Spent $3.5479 of the $200.0000 monthly budget (the preview estimated $6.5868, at most $58.0050).

## Release and runtime

Staging release v1 ships approved text only: es 587, fr 560, ja 522 of 600 messages. The Go runtime loaded it from glossa-edge, verified the signature, and rendered:

| Locale | Key | Args | Rendered | |
|---|---|---|---|---|
| es | `billing.invoice.count` | map[count:1] | 1 factura | AI, accepted |
| es | `billing.invoice.count` | map[count:3] | 3 facturas | AI, accepted |
| es | `billing.invoice.count` | map[count:1000000] | 1.000.000 de facturas | AI, accepted |
| es | `billing.payment_method.create_activity` | map[name:Ada] | Ada ha creado el método de pago. | AI, accepted |
| es | `billing.plan.edit_permission` | map[role:admin] | Como administrador, puede editar los planes. | AI, accepted |
| es | `billing.payment_method.delete` |  | Zahlungsmethode löschen | forbidden-term slip in review: falls back to de |
| fr | `billing.subscription.count` | map[count:1] | 1 abonnement | AI, accepted |
| fr | `billing.subscription.count` | map[count:3] | 3 abonnements | AI, accepted |
| fr | `billing.subscription.count` | map[count:1000000] | 1 000 000 d’abonnements | AI, accepted |
| fr | `billing.invoice.download_activity` | map[name:Ada] | Ada a téléchargé la facture. | AI, accepted |
| fr | `billing.subscription.renew_permission` | map[role:admin] | En tant qu’administrateur, vous pouvez renouveler les abonnements. | AI, accepted |
| fr | `auth.invitation.delete_success` |  | Die Einladung wurde gelöscht. | forbidden-term slip in review: falls back to de |
| ja | `billing.invoice.count` | map[count:1] | 1件の請求書 | AI, accepted |
| ja | `billing.invoice.count` | map[count:3] | 3件の請求書 | AI, accepted |
| ja | `billing.subscription.renew_activity` | map[name:Ada] | Adaさんがサブスクリプションを更新しました。 | AI, accepted |
| ja | `billing.invoice.download_permission` | map[role:admin] | 管理者は請求書をダウンロードできます。 | AI, accepted |
| ja | `billing.plan.edit_success` |  | Der Tarif wurde bearbeitet. | forbidden-term slip in review: falls back to de |
