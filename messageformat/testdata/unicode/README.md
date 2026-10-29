# Unicode MessageFormat conformance suite (vendored)

Copied verbatim from [unicode-org/message-format-wg](https://github.com/unicode-org/message-format-wg/tree/main/test)
at commit `5c4ddb27e726fd7881c1787a632efba83ab0d850` (2026-08-31), under the
Unicode license in [`LICENSE`](./LICENSE).

Every MessageFormat implementation in this repository (Go in
`messageformat/`, TypeScript in `messageformat/js` and `runtimes/js/runtime`)
runs these files in CI. Don't edit them: update by re-vendoring a newer
commit and recording it here. Glossa's own cases live in
[`../glossa`](../glossa).

The upstream suite's format and error codes are documented in its
[README](https://github.com/unicode-org/message-format-wg/blob/main/test/README.md).

## Data model schema

[`data-model/message.schema.json`](./data-model/message.schema.json) is the
spec's JSON Schema for the MessageFormat 2 data model
(`spec/data-model/message.json`, same commit). It is **the wire contract** for
precompiled messages: the Go server serializes messages into release artifacts
in exactly this shape, and every runtime reads exactly this shape. Both sides
validate their fixtures against it.
