#!/usr/bin/env python3
"""Generate the runtime conformance fixtures (runtimes/SPEC.md §7).

Artifacts are stored as their exact bytes (a string), because runtimes
verify SHA-256 over bytes, not over a re-serialization. Manifests are
signed with a fixed, TEST-ONLY Ed25519 key whose seed is below; it
signs nothing outside these fixtures.

Every artifact message is validated against the MF2 data-model schema
and every manifest/artifact against the contract schemas, so a fixture
can't drift from the contract.

    python3 runtimes/testdata/gen/generate.py        # rewrite fixtures
    python3 runtimes/testdata/gen/generate.py --check  # fail on drift (CI)
"""

import base64
import copy
import hashlib
import json
import sys
from pathlib import Path

import jsonschema
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

ROOT = Path(__file__).resolve().parents[1]
REPO = ROOT.parents[1]
SCHEMAS = ROOT / "schemas"
MF2_SCHEMA = REPO / "messageformat/testdata/unicode/data-model/message.schema.json"

TEST_KEY_SEED = bytes(range(32))  # TEST ONLY — never used outside fixtures
TEST_KEY_ID = "k_test"
TEST_KEY = Ed25519PrivateKey.from_private_bytes(TEST_KEY_SEED)
TEST_PUBLIC_KEY = base64.urlsafe_b64encode(TEST_KEY.public_key().public_bytes_raw()).rstrip(b"=").decode()
OTHER_KEY = Ed25519PrivateKey.from_private_bytes(bytes(range(32, 64)))


# ── MF2 data-model helpers ─────────────────────────────────────────────

def text(s):
    return {"type": "message", "declarations": [], "pattern": [s]}


def var(name, fn=None, **options):
    expr = {"type": "expression", "arg": {"type": "variable", "name": name}}
    if fn:
        expr["function"] = {"type": "function", "name": fn}
        if options:
            expr["function"]["options"] = {k: {"type": "literal", "value": str(v)} for k, v in options.items()}
    return expr


def pattern(*parts):
    return {"type": "message", "declarations": [], "pattern": list(parts)}


def plural(name, one, other):
    """`.input {$name :number} .match $name  one {{…}}  * {{…}}`"""
    return {
        "type": "select",
        "declarations": [{"type": "input", "name": name, "value": var(name, "number")}],
        "selectors": [{"type": "variable", "name": name}],
        "variants": [
            {"keys": [{"type": "literal", "value": "one"}], "value": one},
            {"keys": [{"type": "*"}], "value": other},
        ],
    }


# ── Serialization, hashing, signing ─────────────────────────────────────

def artifact_bytes(locale, messages, namespace="default"):
    doc = {"schema": "glossa.artifact/v1", "locale": locale, "namespace": namespace, "messages": messages}
    return json.dumps(doc, ensure_ascii=False, separators=(",", ":"), sort_keys=True)


def sha256(s):
    return hashlib.sha256(s.encode("utf-8")).hexdigest()


def jcs(value):
    """RFC 8785 canonicalization for the value space used here (no floats)."""
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True)


def sign(manifest, key=TEST_KEY, key_id=TEST_KEY_ID):
    unsigned = {k: v for k, v in manifest.items() if k != "signatures"}
    sig = key.sign(jcs(unsigned).encode("utf-8"))
    signed = copy.deepcopy(unsigned)
    signed["signatures"] = [{"keyId": key_id, "alg": "Ed25519",
                             "sig": base64.urlsafe_b64encode(sig).rstrip(b"=").decode()}]
    return signed


def build_release(release_id, version, source, locales, fallback, catalogs):
    """catalogs: {locale: {message_id: message}} → (manifest, {sha: bytes})."""
    artifacts, blobs = {}, {}
    for loc, messages in catalogs.items():
        body = artifact_bytes(loc, messages)
        digest = sha256(body)
        blobs[digest] = body
        artifacts[loc] = {"default": {"sha256": digest, "size": len(body.encode("utf-8"))}}
    manifest = {
        "schema": "glossa.manifest/v1",
        "project": "prj_fixture",
        "environment": "production",
        "release": {"id": release_id, "version": version, "createdAt": f"2026-09-{version:02d}T08:00:00Z"},
        "sourceLocale": source,
        "locales": [{"code": c, "direction": d} for c, d in locales],
        "fallback": fallback,
        "artifacts": artifacts,
    }
    return manifest, blobs


# ── Scenarios (resolution) ──────────────────────────────────────────────

BASE_LOCALES = [("de", "ltr"), ("de-AT", "ltr"), ("en", "ltr"), ("zh-Hant", "ltr"), ("ar", "rtl"), ("fr-CA", "ltr"), ("fr", "ltr")]
BASE_CATALOGS = {
    "de": {
        "cart.checkout": text("Zur Kasse"),
        "cart.items": plural("n", [var("n"), " Artikel"], [var("n"), " Artikel"]),
        "cart.total": pattern(var("amount", "number"), " Artikel"),
        "greeting": pattern("Hallo ", var("name"), "!"),
        "only.de": text("Nur auf Deutsch"),
    },
    "de-AT": {"cart.checkout": text("Zur Kassa")},
    "en": {
        "cart.checkout": text("Checkout"),
        "cart.items": plural("n", ["one item"], [var("n"), " items"]),
        "greeting": pattern("Hello ", var("name"), "!"),
        "only.en": text("English only"),
    },
    "zh-Hant": {"cart.checkout": text("結帳")},
    "ar": {"greeting": pattern("مرحبا ", var("name"), "!")},
    "fr": {"cart.checkout": text("Paiement")},
    "fr-CA": {},
}


def resolution_scenarios():
    manifest, blobs = build_release(
        "rel_base", 1, "de", BASE_LOCALES,
        {"de-AT": ["de"], "*": ["en"]},
        BASE_CATALOGS,
    )

    def case(requested, id, exp, locale, chain, resolved, values=None, default=None, **extra):
        c = {"requested": requested, "id": id, "values": values or {}, "bidiIsolation": "none",
             "exp": exp, "expLocale": locale, "expChain": chain, "expResolvedFrom": resolved}
        if default is not None:
            c["default"] = default
        c.update(extra)
        return c

    yield "negotiation", {
        "description": "RFC 4647 lookup: exact, truncation, canonicalization, no match",
        "manifest": manifest, "artifacts": blobs,
        "cases": [
            case(["en"], "cart.checkout", "Checkout", "en", ["en", "de"], "en"),
            case(["en-GB"], "cart.checkout", "Checkout", "en", ["en", "de"], "en"),
            case(["zh-Hant-TW"], "cart.checkout", "結帳", "zh-Hant", ["zh-Hant", "en", "de"], "zh-Hant"),
            case(["EN_us"], "cart.checkout", "Checkout", "en", ["en", "de"], "en"),
            case(["ja", "fr-CA"], "cart.checkout", "Paiement", "fr-CA", ["fr-CA", "fr", "en", "de"], "fr"),
            case(["ja"], "cart.checkout", "Zur Kasse", "de", ["de", "en"], "de"),
            case([], "cart.checkout", "Zur Kasse", "de", ["de", "en"], "de"),
        ],
    }

    yield "fallback-graph", {
        "description": "Explicit edges, the '*' chain, truncation, and the source locale",
        "manifest": manifest, "artifacts": blobs,
        "cases": [
            case(["de-AT"], "cart.checkout", "Zur Kassa", "de-AT", ["de-AT", "de", "en"], "de-AT"),
            case(["de-AT"], "only.de", "Nur auf Deutsch", "de-AT", ["de-AT", "de", "en"], "de"),
            case(["de-AT"], "only.en", "English only", "de-AT", ["de-AT", "de", "en"], "en"),
            case(["fr-CA"], "only.de", "Nur auf Deutsch", "fr-CA", ["fr-CA", "fr", "en", "de"], "de"),
            case(["ar"], "greeting", "مرحبا Lina!", "ar", ["ar", "en", "de"], "ar", values={"name": "Lina"}, expDirection="rtl"),
        ],
    }

    yield "formatting-locale", {
        "description": "A message found in a fallback locale is formatted with that locale's rules",
        "manifest": manifest, "artifacts": blobs,
        "cases": [
            case(["en"], "cart.items", "one item", "en", ["en", "de"], "en", values={"n": 1}),
            case(["en"], "cart.items", "3 items", "en", ["en", "de"], "en", values={"n": 3}),
            case(["en"], "cart.total", "1.234,5 Artikel", "en", ["en", "de"], "de", values={"amount": 1234.5}),
        ],
    }

    yield "missing-message", {
        "description": "Nothing in the chain: the inline default, else the message ID; never empty",
        "manifest": manifest, "artifacts": blobs,
        "cases": [
            case(["en"], "does.not.exist", "Fallback text", "en", ["en", "de"], None, default="Fallback text"),
            case(["en"], "does.not.exist", "does.not.exist", "en", ["en", "de"], None),
        ],
    }

    cyclic, cyclic_blobs = build_release(
        "rel_cycle", 1, "en", [("en", "ltr"), ("pt-BR", "ltr"), ("pt-PT", "ltr")],
        {"pt-BR": ["pt-PT"], "pt-PT": ["pt-BR"]},
        {"en": {"hello": text("Hello")}, "pt-BR": {}, "pt-PT": {"hello": text("Olá")}},
    )
    yield "fallback-cycle", {
        "description": "Cycles in the fallback graph stop at the first repeat",
        "manifest": cyclic, "artifacts": cyclic_blobs,
        "cases": [
            case(["pt-BR"], "hello", "Olá", "pt-BR", ["pt-BR", "pt-PT", "en"], "pt-PT"),
        ],
    }


# ── Loading sequences ───────────────────────────────────────────────────

def ok(manifest, etag):
    return {"status": 200, "etag": etag, "body": manifest}


def loading_sequences():
    r1, b1 = build_release("rel_1", 1, "en", [("en", "ltr"), ("de", "ltr")], {},
                           {"en": {"hello": text("Hello v1")}, "de": {"hello": text("Hallo v1")}})
    r2, b2 = build_release("rel_2", 2, "en", [("en", "ltr"), ("de", "ltr")], {},
                           {"en": {"hello": text("Hello v2")}, "de": {"hello": text("Hallo v2")}})
    s1, s2 = sign(r1), sign(r2)
    blobs = {**b1, **b2}

    corrupt = dict(b2)
    for digest in list(corrupt):
        corrupt[digest] = corrupt[digest].replace("v2", "vX")  # bytes no longer match the hash

    unsigned_r2 = {k: v for k, v in s2.items() if k != "signatures"}
    wrong_key_r2 = sign(r2, key=OTHER_KEY, key_id=TEST_KEY_ID)
    future = copy.deepcopy(s2)
    future["schema"] = "glossa.manifest/v2"

    def step(desc, manifest, artifacts, active, source, errors=(), read="hello", locale="en", exp=None):
        return {"description": desc, "edge": {"manifest": manifest, "artifacts": artifacts},
                "read": {"id": read, "requested": [locale]},
                "expActiveRelease": active, "expSource": source, "expErrors": list(errors),
                "exp": exp}

    down = {"status": 503}
    yield "last-good", {
        "description": "Network loss falls back to the persisted last-good release",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "steps": [
            step("cold start, edge up", ok(s1, '"m1"'), blobs, "rel_1", "network", exp="Hello v1"),
            step("revalidate, not modified", {"status": 304}, blobs, "rel_1", "memory", exp="Hello v1"),
            step("process restart, edge down", down, {}, "rel_1", "persisted", ["network"], exp="Hello v1", ),
            step("edge back with a new release", ok(s2, '"m2"'), blobs, "rel_2", "network", exp="Hello v2"),
        ],
        "restartBefore": [2],
    }
    yield "atomic-activation", {
        "description": "A release whose artifact fails integrity never becomes active",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "steps": [
            step("release 1", ok(s1, '"m1"'), blobs, "rel_1", "network", exp="Hello v1"),
            step("release 2 with corrupted artifact bytes", ok(s2, '"m2"'), {**b1, **corrupt}, "rel_1", "memory",
                 ["integrity"], exp="Hello v1"),
            step("release 2 served correctly", ok(s2, '"m2b"'), blobs, "rel_2", "network", exp="Hello v2"),
        ],
    }
    yield "signatures", {
        "description": "With public keys configured, unsigned or wrongly signed manifests are rejected",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "steps": [
            step("release 1 signed", ok(s1, '"m1"'), blobs, "rel_1", "network", exp="Hello v1"),
            step("release 2 unsigned", ok(unsigned_r2, '"m2"'), blobs, "rel_1", "memory", ["signature"], exp="Hello v1"),
            step("release 2 signed by an unknown key", ok(wrong_key_r2, '"m3"'), blobs, "rel_1", "memory", ["signature"],
                 exp="Hello v1"),
        ],
    }
    yield "schema-version", {
        "description": "A manifest with an unknown major schema version is rejected; the last good release stays",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "steps": [
            step("release 1", ok(s1, '"m1"'), blobs, "rel_1", "network", exp="Hello v1"),
            step("v2 manifest", ok(future, '"m2"'), blobs, "rel_1", "memory", ["schema"], exp="Hello v1"),
        ],
    }
    yield "cold-offline", {
        "description": "No network and nothing persisted: inline default, never empty",
        "publicKeys": [],
        "steps": [
            step("edge down on first ever start", down, {}, None, "inline", ["network"], exp="hello"),
        ],
    }


# ── Staged rollout (SPEC §1.4) ──────────────────────────────────────────
#
# The expected cohorts are computed here, from SPEC §1.4's formula, with
# Python's hashlib and integer arithmetic. This generator is deliberately
# none of the runtimes (RFC 0006 §12.4): nothing below is imported, ported
# or copied from runtimes/js, runtimes/go or runtimes/dart, so a runtime
# can only agree with these fixtures by implementing the SPEC, never by
# sharing code with the thing that checks it. COHORT_VECTORS were computed
# a second way — `printf '%s%s' "$salt" "$key" | shasum -a 256`, first
# eight hex digits, mod 10000 — and are asserted below, so a change to
# `cohort` that disagrees with the SPEC's own vectors fails generation.

COHORT_BUCKETS = 10000

# (salt, key, cohort): the test vectors printed in SPEC §1.4.
COHORT_VECTORS = [
    ("AAAAAAAAAAAAAAAAAAAAAA", "00000000000000000000000000000000", 1550),
    ("AAAAAAAAAAAAAAAAAAAAAA", "user-42", 4935),
    ("AAAAAAAAAAAAAAAAAAAAAA", "jürgen@example.com", 4213),
]


def cohort(salt, key):
    """SPEC §1.4: SHA-256 over UTF-8(salt) ‖ UTF-8(key), the first four bytes
    as a big-endian unsigned integer, mod 10000."""
    digest = hashlib.sha256(salt.encode("utf-8") + key.encode("utf-8")).digest()
    return int.from_bytes(digest[:4], "big") % COHORT_BUCKETS


for _salt, _key, _want in COHORT_VECTORS:
    assert cohort(_salt, _key) == _want, (_salt, _key, cohort(_salt, _key), _want)


def installation_id(n):
    """The n-th fixture installation id: 32 lowercase hex digits (SPEC §1.4),
    derived deterministically so the fixtures are reproducible."""
    return hashlib.sha256(f"glossa-fixture-installation:{n}".encode()).hexdigest()[:32]


def first_installation(salt, want):
    """The first fixture installation id whose cohort satisfies `want`."""
    n = 0
    while not want(cohort(salt, installation_id(n))):
        n += 1
    return installation_id(n)


def first_installation_both(salt, want, other_salt, other_want):
    """The first fixture installation id whose cohorts under two salts
    satisfy `want` and `other_want`."""
    n = 0
    while not (want(cohort(salt, installation_id(n))) and other_want(cohort(other_salt, installation_id(n)))):
        n += 1
    return installation_id(n)


def with_rollout(stable, candidate, rollout_id, percent, salt):
    """The stable manifest carrying `candidate` as a rollout (unsigned)."""
    m = {k: copy.deepcopy(v) for k, v in stable.items() if k != "signatures"}
    m["rollout"] = {
        "id": rollout_id, "percent": percent, "salt": salt,
        "candidate": {k: copy.deepcopy(candidate[k]) for k in ("release", "locales", "fallback", "artifacts")},
    }
    return m


ROLLOUT_SALT = "c3RhZ2VkLXJvbGxvdXQtMQ"  # 22 base64url characters, as SPEC §1.4 requires
ROLLOUT_ID = "ro_fixture"
OTHER_ROLLOUT_SALT = "b3RoZXItcm9sbG91dC0yMg"
OTHER_ROLLOUT_ID = "ro_fixture_2"

# Sequences whose manifests deliberately break the schema's `rollout`
# member. Everything else in them must still validate.
INVALID_ROLLOUT_SEQUENCES = {"rollout-invalid"}


def rollout_sequences():
    """Loading sequences for SPEC §1.4: an installation outside the
    candidate and a runtime without rollout support — which every runtime
    written before §1.4 passes by ignoring `rollout` — and the sequences
    that put an installation *on* the candidate, which only a runtime that
    implements §1.4 passes (RFC 0006 §13, wave 2)."""
    r1, b1 = build_release("rel_1", 1, "en", [("en", "ltr"), ("de", "ltr")], {},
                           {"en": {"hello": text("Hello v1")}, "de": {"hello": text("Hallo v1")}})
    r2, b2 = build_release("rel_2", 2, "en", [("en", "ltr"), ("de", "ltr")], {},
                           {"en": {"hello": text("Hello v2")}, "de": {"hello": text("Hallo v2")}})
    everything = {**b1, **b2}
    stable_only = dict(b1)  # the candidate's artifacts answer 404: fetching one is an error

    def step(desc, manifest, artifacts, active, source, exp, rollout, errors=()):
        return {"description": desc, "edge": {"manifest": manifest, "artifacts": artifacts},
                "read": {"id": "hello", "requested": ["en"]},
                "expActiveRelease": active, "expSource": source, "expErrors": list(errors), "exp": exp,
                "expRollout": rollout}

    down = {"status": 503}

    # An old runtime: rollout support off, the rollout at 100 %, and an
    # installation id that is in the candidate at any percentage. It must do
    # exactly what a runtime written before SPEC §1.4 does: serve the stable
    # release and never fetch a candidate artifact.
    old_id = first_installation(ROLLOUT_SALT, lambda c: c < 100)
    at_100 = sign(with_rollout(r1, r2, ROLLOUT_ID, 100, ROLLOUT_SALT))
    yield "rollout-old-runtime", {
        "description": "A runtime without rollout support ignores `rollout` even at 100 %: it serves the stable "
                       "release, never fetches the candidate's artifacts, and gets the candidate only once the "
                       "rollout completes",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "installationId": old_id,
        "rolloutSupport": False,
        "steps": [
            step("cold start, a rollout of rel_2 at 100 %; the candidate's artifacts are not served",
                 ok(at_100, '"m1"'), stable_only, "rel_1", "network", "Hello v1", None),
            step("revalidate, not modified", {"status": 304}, stable_only, "rel_1", "memory", "Hello v1", None),
            step("process restart, edge down: the persisted manifest still carries the rollout",
                 down, {}, "rel_1", "persisted", "Hello v1", None, ["network"]),
            step("the rollout completes: rel_2 is the stable release", ok(sign(r2), '"m2"'), everything,
                 "rel_2", "network", "Hello v2", None),
        ],
        "restartBefore": [2],
    }

    # A rollout-aware runtime whose installation is outside the candidate at
    # 10 % and at 50 %: it stays on the stable release through start,
    # restart, advance and abort, and never fetches a candidate artifact.
    out_id = first_installation(ROLLOUT_SALT, lambda c: c >= 5000)
    out_cohort = cohort(ROLLOUT_SALT, out_id)
    at_10 = sign(with_rollout(r1, r2, ROLLOUT_ID, 10, ROLLOUT_SALT))
    at_50 = sign(with_rollout(r1, r2, ROLLOUT_ID, 50, ROLLOUT_SALT))
    side = lambda percent: {"id": ROLLOUT_ID, "percent": percent, "cohort": out_cohort, "side": "stable"}
    yield "rollout-stable-side", {
        "description": f"An installation whose cohort ({out_cohort}) is outside 10 % and 50 % stays on the stable "
                       "release through start, restart, advance and abort, and never fetches the candidate",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "installationId": out_id,
        "steps": [
            step("cold start, a rollout of rel_2 at 10 %; the candidate's artifacts are not served",
                 ok(at_10, '"m1"'), stable_only, "rel_1", "network", "Hello v1", side(10)),
            step("revalidate, not modified", {"status": 304}, stable_only, "rel_1", "memory", "Hello v1", side(10)),
            step("process restart, edge down: the persisted manifest decides the side again",
                 down, {}, "rel_1", "persisted", "Hello v1", side(10), ["network"]),
            step("advanced to 50 %", ok(at_50, '"m2"'), stable_only, "rel_1", "network", "Hello v1", side(50)),
            step("aborted: the manifest carries no rollout", ok(sign(r1), '"m3"'), stable_only, "rel_1", "network",
                 "Hello v1", None),
        ],
        "restartBefore": [2],
    }

    # An installation inside the candidate at 10 %: it activates the
    # candidate, keeps it across a restart (the persisted manifest decides
    # the side again) and an advance, returns to stable when the rollout is
    # aborted, and is outside a new rollout under another salt: a new
    # rollout is a new draw, not a continuation of the old one.
    in_id = first_installation_both(ROLLOUT_SALT, lambda c: c < 1000, OTHER_ROLLOUT_SALT, lambda c: c >= 5000)
    in_cohort, other_cohort = cohort(ROLLOUT_SALT, in_id), cohort(OTHER_ROLLOUT_SALT, in_id)
    at_10_other = sign(with_rollout(r1, r2, OTHER_ROLLOUT_ID, 10, OTHER_ROLLOUT_SALT))
    cand = lambda percent: {"id": ROLLOUT_ID, "percent": percent, "cohort": in_cohort, "side": "candidate"}
    yield "rollout-candidate-side", {
        "description": f"An installation whose cohort ({in_cohort}) is inside 10 % activates the candidate, keeps it "
                       "through restart and advance, returns to stable on abort, and is drawn again by a new rollout",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "installationId": in_id,
        "steps": [
            step("cold start, a rollout of rel_2 at 10 %: the installation is in the candidate",
                 ok(at_10, '"m1"'), everything, "rel_2", "network", "Hello v2", cand(10)),
            step("revalidate, not modified", {"status": 304}, everything, "rel_2", "memory", "Hello v2", cand(10)),
            step("process restart, edge down: the persisted manifest puts it on the candidate again",
                 down, {}, "rel_2", "persisted", "Hello v2", cand(10), ["network"]),
            step("advanced to 50 %: still in the candidate", ok(at_50, '"m2"'), everything, "rel_2", "network",
                 "Hello v2", cand(50)),
            step("aborted: the manifest carries no rollout, so the installation is back on stable",
                 ok(sign(r1), '"m3"'), everything, "rel_1", "network", "Hello v1", None),
            step("a new rollout under another salt: the installation's new cohort is outside it",
                 ok(at_10_other, '"m4"'), stable_only, "rel_1", "network", "Hello v1",
                 {"id": OTHER_ROLLOUT_ID, "percent": 10, "cohort": other_cohort, "side": "stable"}),
        ],
        "restartBefore": [2],
    }

    # A candidate that can't be activated falls back to the stable view of
    # the same manifest (rel_2 here, not the previously active rel_1), and
    # `explain()` shows a cohort inside the percentage on the stable side.
    r3, b3 = build_release("rel_3", 3, "en", [("en", "ltr"), ("de", "ltr")], {},
                           {"en": {"hello": text("Hello v3")}, "de": {"hello": text("Hallo v3")}})
    corrupt3 = {d: body.replace("v3", "vX") for d, body in b3.items()}  # bytes no longer match the hash
    r2_rolling_r3 = sign(with_rollout(r2, r3, ROLLOUT_ID, 10, ROLLOUT_SALT))
    side3 = lambda s: {"id": ROLLOUT_ID, "percent": 10, "cohort": in_cohort, "side": s}
    yield "rollout-candidate-fallback", {
        "description": "A candidate whose artifact fails integrity is never half-activated: the installation gets "
                       "the stable view of the same manifest, not the release it had before",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "installationId": in_id,
        "steps": [
            step("release 1, no rollout", ok(sign(r1), '"m1"'), b1, "rel_1", "network", "Hello v1", None),
            step("rel_2 published with a rollout of rel_3 at 10 %; rel_3's artifacts are corrupt",
                 ok(r2_rolling_r3, '"m2"'), {**b1, **b2, **corrupt3}, "rel_2", "network", "Hello v2",
                 side3("stable"), ["integrity"]),
            step("the same manifest, rel_3 served correctly", ok(r2_rolling_r3, '"m2b"'), {**b2, **b3}, "rel_3",
                 "network", "Hello v3", side3("candidate")),
        ],
    }

    # A `rollout` that doesn't match the schema is ignored with a `schema`
    # error: the stable view, no candidate fetch, `rollout: null`.
    invalid = with_rollout(r1, r2, ROLLOUT_ID, 100, ROLLOUT_SALT)
    invalid["rollout"]["salt"] = "not-a-salt"
    yield "rollout-invalid", {
        "description": "An invalid `rollout` (a salt that isn't 22 base64url characters) is ignored with a schema "
                       "error, even at 100 %; a valid one afterwards is joined",
        "publicKeys": [{"keyId": TEST_KEY_ID, "key": TEST_PUBLIC_KEY}],
        "installationId": in_id,
        "steps": [
            step("cold start, an invalid rollout of rel_2 at 100 %", ok(sign(invalid), '"m1"'), stable_only,
                 "rel_1", "network", "Hello v1", None, ["schema"]),
            step("a valid rollout of rel_2 at 100 %", ok(at_100, '"m2"'), everything, "rel_2", "network",
                 "Hello v2", {"id": ROLLOUT_ID, "percent": 100, "cohort": in_cohort, "side": "candidate"}),
        ],
    }


def rollout_cohorts():
    """The cohort table of RFC 0006 §12.4: 10,000 installation ids with the
    cohort SPEC §1.4 assigns each, plus vectors at the boundaries and for
    server-side cohort keys. Not a loading sequence: a runtime checks its
    cohort function against it, id by id."""
    ids = [installation_id(n) for n in range(10000)]
    cohorts = [cohort(ROLLOUT_SALT, i) for i in ids]
    percents = [0, 1, 10, 50, 99, 100]

    vectors = [{"salt": s, "key": k, "cohort": c, "note": "SPEC §1.4 test vector"} for s, k, c in COHORT_VECTORS]
    for want, note in [(0, "the lowest cohort: in the candidate from 1 %"),
                       (99, "the last cohort in at 1 %"),
                       (100, "the first cohort out at 1 %"),
                       (999, "the last cohort in at 10 %"),
                       (1000, "the first cohort out at 10 %"),
                       (9999, "the highest cohort: in the candidate only at 100 %")]:
        key = first_installation(ROLLOUT_SALT, lambda c, w=want: c == w)
        vectors.append({"salt": ROLLOUT_SALT, "key": key, "cohort": want, "note": note})
    other_salt = OTHER_ROLLOUT_SALT
    vectors.append({"salt": other_salt, "key": ids[0], "cohort": cohort(other_salt, ids[0]),
                    "note": "the first installation under another rollout's salt"})
    for key, note in [("user-42", "a Go per-request cohort key: any non-empty string, hashed as UTF-8"),
                      ("jürgen@example.com", "a non-ASCII cohort key: its UTF-8 bytes"),
                      ("Jürgen@example.com", "case is not folded"),
                      ("jürgen@example.com", "NFD is not NFC: keys are not normalized")]:
        vectors.append({"salt": ROLLOUT_SALT, "key": key, "cohort": cohort(ROLLOUT_SALT, key), "note": note})

    return {
        "description": "SPEC §1.4 cohorts: every fixture installation's cohort under one rollout's salt, how many are "
                       "in the candidate at each percentage, and boundary and cohort-key vectors. Computed by "
                       "runtimes/testdata/gen/generate.py, which is none of the runtimes",
        "salt": ROLLOUT_SALT,
        "expCandidates": {str(p): sum(1 for c in cohorts if c < p * 100) for p in percents},
        "vectors": vectors,
        "installations": [[i, c] for i, c in zip(ids, cohorts)],
    }


def render_cohorts(table):
    """One installation per line, so 10,000 entries stay reviewable in a diff."""
    head = {k: v for k, v in table.items() if k != "installations"}
    body = json.dumps(head, ensure_ascii=False, indent=2)[:-2]
    rows = ",\n".join("    " + json.dumps(row, ensure_ascii=False) for row in table["installations"])
    return f'{body},\n  "installations": [\n{rows}\n  ]\n}}\n'


# ── Edge: delivery-key scopes (SPEC §2) ─────────────────────────────────

EDGE_PROJECT = "0192f5a0-7a4e-7cc3-9d1e-3a4b5c6d7e8f"
DEFAULT_ENVIRONMENTS = ["development", "preview", "staging", "production"]


def delivery_key(label):
    """A fixed, well-formed publishable key: glossa_pk_ + 32 base64url characters."""
    raw = hashlib.sha256(f"fixture key {label}".encode("utf-8")).digest()[:24]
    return "glossa_pk_" + base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def key_index(key_id, environments=None, branches=None):
    """A key index object; environments=None writes one from before scopes."""
    idx = {"schema": "glossa.delivery-key/v1", "project": EDGE_PROJECT, "key_id": key_id}
    if environments is not None:
        idx["environments"] = sorted(environments)
        idx["branches"] = bool(branches)
    return idx


def edge_fixtures():
    keys = {
        "production": key_index("k_prod", ["production"]),
        "staging-and-production": key_index("k_stage", ["staging", "production"]),
        "preview": key_index("k_preview", ["preview"], branches=True),
        "branches-only": key_index("k_branches", [], branches=True),
        "legacy": key_index("k_legacy"),
        "revoked": None,
    }
    environments = DEFAULT_ENVIRONMENTS + ["qa", "pr-42", "br-0a1b2c3d"]

    def case(key, environment, status):
        return {"key": key, "environment": environment, "expStatus": status}

    cases = []
    for label, idx in keys.items():
        for env in environments:
            if idx is None:
                allowed = False
            else:
                allowlist = idx.get("environments", DEFAULT_ENVIRONMENTS)
                branch = env.startswith(("pr-", "br-"))
                allowed = env in allowlist or (idx.get("branches", False) and branch)
            cases.append(case(label, env, 200 if allowed else 404))
        cases.append({"key": label, "artifact": True, "expStatus": 404 if idx is None else 200})
    yield "key-scopes", {
        "description": "A key reads the environments on its allowlist and, with branches, every branch environment "
                       "(pr-<n>, br-<8 hex>); anything else answers 404 exactly like an unknown key. An index object "
                       "without environments predates scopes and reads as the four default environments. Artifacts "
                       "aren't scoped by environment.",
        "project": EDGE_PROJECT,
        "keys": {label: {"key": delivery_key(label), "index": idx} for label, idx in keys.items()},
        "environments": environments,
        "cases": cases,
    }


# ── Safe markup (formatted parts → HTML) ────────────────────────────────

# Inline, attribute-free phrasing elements a translation may produce. The
# single source of truth: @klarlabs-studio/glossa/elements' SAFE_TAGS and the Go runtime's
# safeTags are tested against this list.
SAFE_TAGS = ("b strong i em u s small mark sub sup code kbd samp var abbr cite dfn q "
             "del ins bdi span br wbr").split()
VOID_TAGS = ["br", "wbr"]


def t(value):
    return {"type": "text", "value": value}


def mk(kind, name, **options):
    p = {"type": "markup", "kind": kind, "name": name}
    if options:
        p["options"] = options
    return p


def markup_fixture():
    open_, close, alone = (lambda n, **o: mk("open", n, **o)), (lambda n: mk("close", n)), (lambda n: mk("standalone", n))

    def case(description, parts, html):
        return {"description": description, "parts": parts, "html": html}

    return {
        "$comment": "Formatted parts -> HTML, the rules of @klarlabs-studio/glossa/elements/parts and the Go runtime's HTML/th. "
                    "`safeTags`: the only markup names that become elements; `voidTags`: the safe tags rendered "
                    "without children or a closing tag. Markup options are always dropped. Text is escaped "
                    "(& < >). Unsafe markup keeps just its content. Unclosed markup closes at the end; a close "
                    "closes everything opened after its matching open; a close without an open is ignored. "
                    "Non-markup parts render as their text: `value`, the joined `parts` values of numbers and "
                    "dates, or `{source}` for a fallback.",
        "safeTags": SAFE_TAGS,
        "voidTags": VOID_TAGS,
        "cases": [
            case("text, isolation, values and fallbacks join into one text node",
                 [t("Hallo "), {"type": "bidiIsolation", "value": "⁨"}, {"type": "string", "value": "Lina"},
                  {"type": "bidiIsolation", "value": "⁩"}, t(", du hast "),
                  {"type": "number", "parts": [{"type": "integer", "value": "1"}, {"type": "group", "value": "."},
                                               {"type": "integer", "value": "000"}]},
                  t(" Punkte "), {"type": "fallback", "source": "$missing"}],
                 "Hallo ⁨Lina⁩, du hast 1.000 Punkte {$missing}"),
            case("safe markup becomes nested elements",
                 [t("Tippe "), open_("b"), t("hier "), open_("em"), t("jetzt"), close("em"), close("b"), t(".")],
                 "Tippe <b>hier <em>jetzt</em></b>."),
            case("standalone void markup", [t("a"), alone("br"), t("b"), alone("wbr")], "a<br>b<wbr>"),
            case("an opened void tag renders once, without children", [open_("br"), t("x"), close("br")], "<br>x"),
            case("standalone non-void markup renders nothing", [t("a"), alone("b"), t("c")], "ac"),
            case("unsafe markup keeps only its content",
                 [t("Klick "), open_("link"), t("hier"), close("link"), open_("script"), t("x"), close("script"),
                  alone("img")],
                 "Klick hierx"),
            case("a link from a translation stays text (en link markup)",
                 [t("By continuing you accept the "), open_("link", href="/terms"), t("terms of service"),
                  close("link"), t(".")],
                 "By continuing you accept the terms of service."),
            case("options are dropped, so a translation can't add attributes",
                 [open_("b", onclick="alert(1)", **{"class": "x"}), t("x"), close("b"),
                  open_("span", style="color:red", title="t"), t("y"), close("span")],
                 "<b>x</b><span>y</span>"),
            case("safe markup inside unsafe markup", [open_("a", href="javascript:alert(1)"), open_("b"), t("x"),
                                                      close("b"), close("a")], "<b>x</b>"),
            case("unclosed markup closes at the end", [open_("b"), t("x"), open_("i"), t("y")], "<b>x<i>y</i></b>"),
            case("a close without an open is ignored", [t("x"), close("b"), t("y")], "xy"),
            case("a close closes everything opened after its open",
                 [open_("b"), t("1"), open_("i"), t("2"), close("b"), t("3"), close("i")],
                 "<b>1<i>2</i></b>3"),
            case("a close for unsafe markup ends the safe markup inside it",
                 [open_("link"), t("1"), open_("b"), t("2"), close("link"), t("3")],
                 "1<b>2</b>3"),
            case("text is escaped, quotes are kept",
                 [t("<img src=x onerror=alert(1)> & "), open_("strong"), t('"fett" \'x\''), close("strong"),
                  alone("br")],
                 "&lt;img src=x onerror=alert(1)&gt; &amp; <strong>\"fett\" 'x'</strong><br>"),
            case("markup names are case-sensitive", [open_("B"), t("x"), close("B")], "x"),
            case("empty safe elements are kept", [open_("b"), close("b"), t("x")], "<b></b>x"),
        ],
    }


# ── Validation and output ───────────────────────────────────────────────

def validators():
    load = lambda p: json.loads(p.read_text())
    mf2 = jsonschema.Draft7Validator(load(MF2_SCHEMA))
    manifest = jsonschema.Draft202012Validator(load(SCHEMAS / "manifest.schema.json"))
    artifact = jsonschema.Draft202012Validator(load(SCHEMAS / "artifact.schema.json"))
    return mf2, manifest, artifact


def delivery_key_validator():
    return jsonschema.Draft202012Validator(json.loads((SCHEMAS / "delivery-key.schema.json").read_text()))


def validate_release(manifest, blobs, v):
    mf2, man, art = v
    if manifest.get("schema") == "glossa.manifest/v1":
        man.validate(manifest)
    for body in blobs.values():
        doc = json.loads(body)
        art.validate(doc)
        for message in doc["messages"].values():
            mf2.validate(message)


def render(obj):
    return json.dumps(obj, ensure_ascii=False, indent=2) + "\n"


def main():
    check = "--check" in sys.argv
    v = validators()
    outputs = {}
    for name, sc in resolution_scenarios():
        validate_release(sc["manifest"], sc["artifacts"], v)
        outputs[ROOT / "scenarios" / f"{name}.json"] = render(sc)
    for name, seq in loading_sequences():
        for st in seq["steps"]:
            m = st["edge"]["manifest"]
            if m.get("status") == 200:
                validate_release(m["body"], {}, v)
        outputs[ROOT / "loading" / f"{name}.json"] = render(seq)
    for name, seq in rollout_sequences():
        for st in seq["steps"]:
            m = st["edge"]["manifest"]
            if m.get("status") != 200:
                continue
            body = m["body"]
            if name in INVALID_ROLLOUT_SEQUENCES and not v[1].is_valid(body):
                body = {k: val for k, val in body.items() if k != "rollout"}
            validate_release(body, {}, v)
        outputs[ROOT / "loading" / f"{name}.json"] = render(seq)
    table = rollout_cohorts()
    assert json.loads(render_cohorts(table)) == table
    outputs[ROOT / "rollout" / "cohorts.json"] = render_cohorts(table)
    outputs[ROOT / "markup.json"] = render(markup_fixture())
    key_schema = delivery_key_validator()
    for name, fx in edge_fixtures():
        for k in fx["keys"].values():
            if k["index"] is not None:
                key_schema.validate(k["index"])
        outputs[ROOT / "edge" / f"{name}.json"] = render(fx)
    drift = [p for p, content in outputs.items() if not p.exists() or p.read_text() != content]
    if check:
        if drift:
            print("runtime fixtures are stale:", *[str(p.relative_to(REPO)) for p in drift], sep="\n  ")
            sys.exit(1)
        print(f"{len(outputs)} runtime fixtures up to date")
        return
    for p, content in outputs.items():
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    print(f"wrote {len(outputs)} fixtures ({len(drift)} changed)")


if __name__ == "__main__":
    main()
