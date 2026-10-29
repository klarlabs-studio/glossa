/**
 * Generated from platform/api/openapi.yaml by scripts/gen-api.mjs
 * (openapi-typescript). Do not edit; run `pnpm --filter @glossa/studio gen:api`.
 */

export interface paths {
    "/v1/meta": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What this deployment offers
         * @description The sign-in methods this server accepts, whether it sends email,
         *     and glossa-edge's public base URL, so clients don't guess.
         *     Public and the same for every caller.
         */
        get: operations["getMeta"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/magic-links": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Email a sign-in link
         * @description Sends a single-use link valid for 15 minutes. Signing in with it
         *     creates the account (and the person's individual tenant) if the
         *     address is new, and verifies the address. Always `202`, so the
         *     response never reveals whether an account exists. Problem codes:
         *     `email_disabled` (404: the server sends no email; see
         *     `GET /v1/meta`).
         */
        post: operations["requestMagicLink"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/magic-link-redemptions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in with an emailed link
         * @description Spends the link's token and starts a session. Problem codes:
         *     `link_invalid` (unknown, expired or already used),
         *     `email_disabled` (404).
         */
        post: operations["redeemMagicLink"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/registrations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Register with a password
         * @description Creates the account and its individual tenant, then emails a
         *     verification link (a sign-in link). Password sign-in works once
         *     the address is verified. Always `202`: an existing address gets
         *     a sign-in link instead, so registration can't be used to probe
         *     for accounts. On a server that sends no email
         *     (`email_delivery: false` in `GET /v1/meta`) nothing is mailed and
         *     the account can sign in with its password right away, its
         *     address unverified; an existing address is left untouched.
         */
        post: operations["register"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password-sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in with email and password
         * @description Problem codes: `invalid_credentials` (401), `totp_required` (401,
         *     send `totp_code`), `totp_invalid` (401), `email_unverified` (403;
         *     only on servers that send email), `account_locked` (429 after
         *     repeated failures; the lock lifts after 15 minutes).
         */
        post: operations["signInWithPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password-resets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Email a password reset link
         * @description Always `202`, whether or not the address has an account. Problem
         *     codes: `email_disabled` (404).
         */
        post: operations["requestPasswordReset"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password-reset-redemptions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Set a new password with a reset link
         * @description Sets the password, verifies the address and signs the person out
         *     everywhere. Problem codes: `link_invalid` (401), `weak_password`
         *     (400), `email_disabled` (404).
         */
        post: operations["resetPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/passkey-challenges": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Start a passkey sign-in
         * @description Returns WebAuthn request options for `navigator.credentials.get`
         *     and sets a short-lived, HttpOnly ceremony cookie that
         *     `POST /v1/auth/passkey-sessions` consumes. Problem codes:
         *     `passkeys_disabled` (404), `no_passkeys` (404).
         */
        post: operations["beginPasskeySignIn"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/passkey-sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in with a passkey assertion
         * @description Problem codes: `passkey_invalid` (401).
         */
        post: operations["finishPasskeySignIn"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/session": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Sign out of this session */
        delete: operations["signOut"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Sign out of every session
         * @description Revokes all of the person's sessions on every device.
         */
        delete: operations["signOutEverywhere"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/device-authorizations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Start signing a device in as a person
         * @description How `glossa login` signs a person in on a machine with no
         *     browser session (RFC 0006 §7.2, the OAuth 2.0 device
         *     authorization grant, RFC 8628). The CLI asks for a
         *     `device_code`, shows the `user_code` and `verification_uri`,
         *     and polls `POST /v1/auth/device-sessions` every `interval`
         *     seconds while the person approves the code in Studio, signed
         *     in as themselves.
         *
         *     The `device_code` is the CLI's secret and is never shown; the
         *     `user_code` is eight characters from an alphabet without
         *     look-alikes, shown as `XXXX-XXXX`, and is only good for
         *     approving or denying. Both expire after `expires_in` seconds
         *     (15 minutes). Unauthenticated, rate limited per client address.
         */
        post: operations["startDeviceAuthorization"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/device-authorizations/{user_code}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description The code the device shows, with or without its hyphen, in any case. */
                user_code: string;
            };
            cookie?: never;
        };
        /**
         * What a device code would sign in
         * @description What Studio shows before the person approves: the name the
         *     device gave itself and when it asked, so a code read out by
         *     someone else is recognisable as not one's own. Only a browser
         *     session may look a code up. A code that is unknown, expired or
         *     already decided answers `404` `device_authorization_not_found`,
         *     whichever it is.
         */
        get: operations["getDeviceAuthorization"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/device-approvals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve or deny a device code
         * @description The signed-in person approves the code, which signs the device
         *     in as them, or denies it, which ends it. Only a browser session
         *     decides — never an API token, and never a device session, so a
         *     signed-in CLI cannot sign further devices in. The device's
         *     session belongs to the person and to nobody else: it acts with
         *     exactly the person's memberships, roles, project scope and
         *     visibility, in every tenant, and ends when the person signs out
         *     everywhere. Problem codes: `device_authorization_not_found`
         *     (404: unknown, expired or already decided).
         */
        post: operations["decideDeviceAuthorization"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/device-sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Poll for the device's session
         * @description The device polls with its `device_code`. Until the person
         *     decides, the answer is `400` `authorization_pending`; polling
         *     faster than `interval` answers `400` `slow_down`, and the device
         *     adds five seconds to its interval (RFC 8628 §3.5). A denied code
         *     answers `400` `access_denied`, an expired or unknown one `400`
         *     `expired_token`. Once approved, the first poll returns the
         *     session's bearer and spends the code: a second poll with it is
         *     `expired_token`.
         */
        post: operations["redeemDeviceAuthorization"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/github-oidc-exchanges": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Exchange a GitHub Actions ID token for a CI token
         * @description How a GitHub Actions run authenticates without a stored secret
         *     (RFC 0004 §6.3). The job requests an OIDC ID token with audience
         *     `glossa` and posts it here; `glossa login` does this by itself
         *     when it sees `ACTIONS_ID_TOKEN_REQUEST_URL`.
         *
         *     The operation is unauthenticated because the ID token *is* the
         *     credential. Glossa verifies it against the issuer's published
         *     keys — issuer, audience, signature, algorithm and expiry — and
         *     then matches its `repository_id` claim, GitHub's immutable
         *     number, against a Git connection. The repository's *name* is
         *     never consulted: a name can be renamed, deleted and registered
         *     by a stranger, and a policy keyed on one could be inherited.
         *
         *     The token it returns:
         *
         *     - allows exactly `catalog.read` and `catalog.write`, which is
         *       what `glossa push`, `extract --upload`, `context push`,
         *       `capture --upload` and `preview register` need and no more —
         *       it cannot import translations, publish a release, ask an AI
         *       provider for anything or read the tenant's members or tokens;
         *     - is bound to one project: a request under another project of
         *       the same tenant is `403` `grant_project_mismatch`;
         *     - lives 30 minutes and cannot be refreshed. The next job asks
         *       GitHub for a fresh ID token.
         *
         *     When a repository feeds several projects — a monorepo with one
         *     connection per path — send `project_id`; without it the answer
         *     is `409` `ambiguous_project`, whose `errors` list the candidate
         *     projects (`pointer: "/project_id"`, `detail` the project id and
         *     its path) so CI can be told which to name.
         *
         *     **Forks.** GitHub gives a pull request from a fork no ID token
         *     and no secrets, so a fork's job has nothing to present. Its
         *     Glossa check completes as `neutral` with an explanation
         *     (RFC 0004 §6.4) rather than failing. Nothing here has to detect
         *     a fork: a token minted from a fork's own repository would be
         *     minted for that repository's connections, which are not the
         *     upstream's.
         *
         *     Problem codes: `invalid_id_token` (401),
         *     `repository_not_connected` (403), `ambiguous_project` (409),
         *     `github_not_configured` (503).
         */
        post: operations["exchangeGitHubOIDCToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The signed-in person and their tenants
         * @description Also accepts any open invitations addressed to the person's
         *     verified email.
         */
        get: operations["getMe"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/passkey-challenges": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Start adding a passkey
         * @description Returns WebAuthn creation options for
         *     `navigator.credentials.create` and sets the ceremony cookie.
         *     Problem codes: `passkeys_disabled` (404).
         */
        post: operations["beginPasskeyRegistration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/passkeys": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The signed-in person's passkeys
         * @description Every passkey registered to the person, oldest first, on any
         *     device — not only this browser's. Listed even while passkeys are
         *     not configured on the server.
         */
        get: operations["listPasskeys"];
        put?: never;
        /**
         * Add a passkey
         * @description Problem codes: `passkey_invalid` (400).
         */
        post: operations["finishPasskeyRegistration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/passkeys/{passkey}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A passkey `id` (its credential ID, base64url). */
                passkey: components["parameters"]["PasskeyPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Remove one of the signed-in person's passkeys
         * @description The passkey can't sign in any more. Sessions it started stay
         *     signed in (sign out everywhere to end them). Another person's
         *     passkey is `404`, like an unknown one.
         */
        delete: operations["deletePasskey"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/totp": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Start enrolling an authenticator app
         * @description Creates a pending TOTP secret (replacing any earlier pending
         *     one). It takes effect once confirmed with a code. Problem codes:
         *     `totp_already_enabled` (409).
         */
        put: operations["beginTotpEnrollment"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/totp/confirmation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Confirm the authenticator app with a code
         * @description Problem codes: `totp_invalid` (400), `totp_not_pending` (409).
         */
        post: operations["confirmTotpEnrollment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/me/totp/deactivation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Turn TOTP off with a current code
         * @description Problem codes: `totp_invalid` (400), `totp_not_enabled` (409).
         */
        post: operations["disableTotp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/message-previews": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Parse, convert and format a message without storing it
         * @description Runs the server's MessageFormat kernel — the one ICU MF1
         *     converter (RFC 0002 §5) — on `source`: parses it in `syntax`
         *     (default `mf1`) for `locale` into the canonical MF2 data model
         *     (`message`), serializes that as MF2 (`mf2`), derives `arguments`
         *     and `markup`, and, when `values` are sent, formats it
         *     (`formatted`, with MF2 fallbacks for placeholders that failed).
         *     Source that doesn't parse is still `200`, with `valid: false`
         *     and the kernel's error codes in `errors`, so editors can show
         *     them inline; formatting problems are `errors` with stage
         *     `format`. Nothing is stored and no tenant data is read, but the
         *     caller must be signed in or send an API token. Limits: `source`
         *     at most 20 000 bytes; `values` at most 100 names of at most 64
         *     characters, each a string (at most 1 000 bytes), number or
         *     boolean; 10 requests a second per person or token, bursts of up
         *     to 120 (per server instance). Problem codes: `message_too_long`,
         *     `invalid_locale`, `invalid_syntax`, `invalid_values` (400),
         *     `rate_limited` (429).
         */
        post: operations["previewMessage"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Tenants the caller can act in
         * @description A person's active memberships; an API token's own tenant.
         */
        get: operations["listTenants"];
        put?: never;
        /**
         * Create an organization
         * @description The caller becomes its owner. Only people create organizations.
         *     Problem codes: `slug_taken` (409).
         */
        post: operations["createTenant"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * A tenant
         * @description Needs `tenant.read`.
         */
        get: operations["getTenant"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/members": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Members and open invitations
         * @description Needs `members.read`.
         */
        get: operations["listMembers"];
        put?: never;
        /**
         * Invite someone by email
         * @description Opens an invitation that becomes an active membership when the
         *     address's owner signs in. `projects`, `vendor_id` and
         *     `visibility` restrict the member from the start (RFC 0006 §3.3,
         *     §4.1): a vendor's translator is invited with `vendor_id`,
         *     `visibility: assigned` and, usually, the projects they work on.
         *     Needs `members.manage`; the `owner` role needs `owners.manage`,
         *     naming a vendor `vendors.manage`, and an inviter limited to some
         *     projects invites only within them. Problem codes:
         *     `already_member` (409), `individual_tenant` (409),
         *     `owner_change_forbidden` (403), `scope_exceeds_grant` (403: a
         *     project outside the inviter's), `locales_need_locale_role`,
         *     `invalid_visibility`, `vendor_member_visibility`,
         *     `assigned_visibility_role`, `owner_project_scoped`,
         *     `too_many_projects` (400), `not_found` (404: no such vendor).
         */
        post: operations["addMember"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/members/{member}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        /**
         * A member
         * @description Needs `members.read`.
         */
        get: operations["getMember"];
        put?: never;
        post?: never;
        /**
         * Remove a member or withdraw an invitation
         * @description Needs `members.manage` (`owners.manage` for an owner).
         *     `If-Match` is optional here; when sent it must match. Problem
         *     codes: `last_owner` (409), `owner_change_forbidden` (403).
         */
        delete: operations["removeMember"];
        options?: never;
        head?: never;
        /**
         * Change a member's roles or locales
         * @description Members omitted from the body keep their value. A request changes
         *     the member's access (`roles`, `locales`) or their restriction
         *     (`projects`, `vendor_id`, `visibility`, RFC 0006 §3.3, §4.1), not
         *     both: each is its own event and version. A restriction takes
         *     effect on the member's next request. Needs `members.manage`;
         *     anything touching the `owner` role needs `owners.manage`, and
         *     setting or clearing a vendor `vendors.manage`. Problem codes:
         *     `last_owner` (409), `owner_change_forbidden`,
         *     `scope_exceeds_grant` (403), `locales_need_locale_role`,
         *     `access_and_restriction` (400: both in one request), and the
         *     restriction codes of `addMember` (400).
         */
        patch: operations["updateMember"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/tokens": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * API tokens, including revoked ones
         * @description Needs `tokens.read`.
         */
        get: operations["listTokens"];
        put?: never;
        /**
         * Create an API token
         * @description The response is the only time the secret is shown. A token's
         *     scopes can't exceed what its creator may do. Needs
         *     `tokens.manage`. Problem codes: `scope_exceeds_grant` (403).
         */
        post: operations["createToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tokens/{token}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An API token `id` (never its secret). */
                token: components["parameters"]["TokenPath"];
            };
            cookie?: never;
        };
        /**
         * An API token (never its secret)
         * @description Needs `tokens.read`.
         */
        get: operations["getToken"];
        put?: never;
        post?: never;
        /**
         * Revoke an API token
         * @description The token stops working at once and stays listed as revoked.
         *     Needs `tokens.manage`. Problem codes: `token_revoked` (409).
         */
        delete: operations["revokeToken"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/groups": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The organization's groups
         * @description Each with its members. Needs `members.read`.
         */
        get: operations["listGroups"];
        put?: never;
        /**
         * Create a group
         * @description An empty group; add members with `putGroupMember`. Needs
         *     `members.manage`. Problem codes: `invalid_group_name` (400),
         *     `individual_tenant` (409).
         */
        post: operations["createGroup"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/groups/{group}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
            };
            cookie?: never;
        };
        /**
         * A group
         * @description Needs `members.read`.
         */
        get: operations["getGroup"];
        put?: never;
        post?: never;
        /**
         * Delete a group
         * @description Its members stay members. Assignments and approvals that named
         *     the group keep naming it and reach nobody. Needs
         *     `members.manage`.
         */
        delete: operations["deleteGroup"];
        options?: never;
        head?: never;
        /**
         * Rename a group
         * @description Needs `members.manage`. Problem codes: `invalid_group_name`
         *     (400).
         */
        patch: operations["renameGroup"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/groups/{group}/members/{member}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Put a member into a group
         * @description Idempotent: a member already in the group stays, and the group
         *     is answered as it is. Needs `members.manage`. Problem codes:
         *     `group_full` (409).
         */
        put: operations["putGroupMember"];
        post?: never;
        /**
         * Take a member out of a group
         * @description Needs `members.manage`. Problem codes: `not_in_group` (404).
         */
        delete: operations["removeGroupMember"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/vendors": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The organization's vendors
         * @description Needs `members.read`.
         */
        get: operations["listVendors"];
        put?: never;
        /**
         * Add a vendor
         * @description A vendor is a named group of members inside this tenant, not a
         *     tenant of its own (RFC 0006 §3.3); invite its people with
         *     `addMember` and `vendor_id`. Needs `vendors.manage`. Problem
         *     codes: `invalid_vendor_name`, `invalid_vendor_contact`,
         *     `invalid_locale` (400), `individual_tenant` (409).
         */
        post: operations["createVendor"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/vendors/{vendor}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A vendor `id`. */
                vendor: components["parameters"]["VendorPath"];
            };
            cookie?: never;
        };
        /**
         * A vendor
         * @description Needs `members.read`.
         */
        get: operations["getVendor"];
        put?: never;
        post?: never;
        /**
         * Delete a vendor nobody works for any more
         * @description A vendor that still has members is refused: taking each of them
         *     off it, or removing them, is a decision about that person.
         *     Needs `vendors.manage`. Problem codes: `vendor_has_members`
         *     (409).
         */
        delete: operations["deleteVendor"];
        options?: never;
        head?: never;
        /**
         * Change a vendor's details
         * @description Members omitted from the body keep their value. Needs
         *     `vendors.manage`. Problem codes: as `createVendor`.
         */
        patch: operations["updateVendor"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Projects of the tenant
         * @description Needs `catalog.read`.
         */
        get: operations["listProjects"];
        put?: never;
        /**
         * Create a project
         * @description The source locale is fixed at creation. A `settings.check_policy`
         *     sent here is checked for shape only — the project has no locales
         *     yet to require one against; the first update checks its locales.
         *     Needs `catalog.write`. Problem codes: `slug_taken` (409),
         *     `invalid_slug`, `invalid_name`, `invalid_locale`,
         *     `invalid_syntax`, `invalid_branch`, `invalid_check_policy` (400).
         */
        post: operations["createProject"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project
         * @description Needs `catalog.read`.
         */
        get: operations["getProject"];
        put?: never;
        post?: never;
        /**
         * Delete a project and everything in it
         * @description Deletes its applications, messages, source history, locales and
         *     translations. Needs `tenant.manage`. `If-Match` is optional; when
         *     sent it must match.
         */
        delete: operations["deleteProject"];
        options?: never;
        head?: never;
        /**
         * Rename a project or change its settings
         * @description Members omitted from the body keep their value; the source locale
         *     can't change. A `settings.check_policy` that names locales
         *     (`require_complete: listed`) is checked against the project's own
         *     locales, which needs `translations.read` as well. Needs
         *     `catalog.write`. Problem codes: `slug_taken` (409),
         *     `invalid_slug`, `invalid_name`, `invalid_syntax`,
         *     `invalid_branch`, `invalid_check_policy` (400).
         */
        patch: operations["updateProject"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/applications": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Applications of a project
         * @description Needs `catalog.read`.
         */
        get: operations["listApplications"];
        put?: never;
        /**
         * Add an application to a project
         * @description Needs `catalog.write`. Problem codes: `slug_taken` (409),
         *     `invalid_platform` (400).
         */
        post: operations["createApplication"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/applications/{application}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An application `id`. */
                application: components["parameters"]["ApplicationPath"];
            };
            cookie?: never;
        };
        /**
         * An application
         * @description Needs `catalog.read`.
         */
        get: operations["getApplication"];
        put?: never;
        post?: never;
        /**
         * Remove an application
         * @description Needs `catalog.write`. `If-Match` is optional; when sent it must match.
         */
        delete: operations["deleteApplication"];
        options?: never;
        head?: never;
        /**
         * Change an application
         * @description Needs `catalog.write`. Problem codes: `slug_taken` (409).
         */
        patch: operations["updateApplication"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Messages of a project, by key
         * @description Filters combine. `missing_in` and `outdated_in` (at most one) ask
         *     Localization which messages have no translation in a locale, or
         *     one made against an older source revision; they also need
         *     `translations.read`. They see a bulk upsert's messages
         *     (`message-upserts`, `glossa push`) as soon as it returns, and
         *     other message writes once their event has been processed
         *     (usually within a second). Needs `catalog.read`.
         *     Problem codes: `coverage_filter_conflict` (400).
         */
        get: operations["listMessages"];
        put?: never;
        /**
         * Create a message
         * @description The source is parsed (ICU MF1 unless `syntax` or the project's
         *     default says MF2) into the canonical MF2 model; revision 1 starts
         *     the message's source log. Needs `catalog.write`. Problem codes:
         *     `key_taken` (409), `invalid_message` (400, the MessageFormat
         *     error code in `detail`), `invalid_message_key`,
         *     `invalid_namespace`, `invalid_details`, `invalid_syntax`,
         *     `message_too_long` (400).
         */
        post: operations["createMessage"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/namespaces": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's namespaces, by name, with their message counts
         * @description Every namespace that holds a message, in name order, with how
         *     many of its messages are `active` and `obsolete` — what an
         *     export or a filter offers to choose from. One grouped read of
         *     the project's messages per page. Needs `catalog.read`.
         */
        get: operations["listNamespaces"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/message-upserts": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create or revise messages in bulk (CLI push)
         * @description Up to 500 messages by key, in one transaction. Idempotent: a
         *     message whose source parses to the stored model is `unchanged`,
         *     so pushing the same catalog twice changes nothing. An obsolete
         *     message pushed again is reactivated. Items fail on their own
         *     (`results[].error.code`: `invalid_message_key`,
         *     `invalid_message`, `invalid_namespace`, `invalid_details`,
         *     `invalid_syntax`, `message_too_long`, `duplicate_key`,
         *     `source_revision_conflict` when `base_revision` is stale) without
         *     failing the batch. Localization's view of the changed messages is
         *     updated in the same transaction, so `missing_in`/`outdated_in`
         *     listings and fills see the push as soon as it returns. Needs
         *     `catalog.write`. Problem codes: `too_many_items` (400).
         */
        post: operations["upsertMessages"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        /**
         * A message
         * @description Needs `catalog.read`.
         */
        get: operations["getMessage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Change a message's namespace, description or length limit
         * @description Not a source revision. `max_length: 0` removes the limit. Needs
         *     `catalog.write`.
         */
        patch: operations["updateMessage"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/source": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Revise a message's source text
         * @description Appends a source revision and publishes
         *     `catalog.message.source_revised`, which makes existing
         *     translations outdated. Text that parses to the current model is
         *     no revision (the message comes back unchanged). The key and ID
         *     never change. Needs `catalog.write`. Problem codes:
         *     `invalid_message` (400), `message_obsolete` (409).
         */
        put: operations["reviseMessageSource"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/obsoletion": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Retire a message
         * @description The message keeps its history and translations but is no longer
         *     released. Pushing its key again reactivates it. Idempotent. Needs
         *     `catalog.write`. `If-Match` is optional; when sent it must match.
         */
        post: operations["obsoleteMessage"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/renames": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Give a message a new key
         * @description The explicit way to change a message's identity as runtimes see
         *     it: the ID, source log and translations stay with the message.
         *     `Location` points at the new key. Needs `catalog.write`. `If-Match`
         *     is optional; when sent it must match. Problem codes: `key_taken`
         *     (409), `invalid_message_key` (400).
         */
        post: operations["renameMessage"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/source-revisions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        /**
         * A message's source log, newest first
         * @description Needs `catalog.read`.
         */
        get: operations["listSourceRevisions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/locales": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's locales, the source locale included
         * @description Needs `translations.read`.
         */
        get: operations["listLocales"];
        put?: never;
        /**
         * Add a locale to a project
         * @description The code is canonicalized (`de_de` → `de-DE`); extensions and
         *     private use are refused. Adding an existing locale returns it
         *     with `200`. Needs `catalog.write`. Problem codes: `invalid_locale`
         *     (400).
         */
        post: operations["addLocale"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/locales/{locale}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        /**
         * A locale of a project
         * @description Needs `translations.read`. Problem codes: `locale_not_found` (404).
         */
        get: operations["getLocale"];
        put?: never;
        post?: never;
        /**
         * Remove a locale from a project
         * @description Its translations and their history are kept and come back if the
         *     locale is added again. Needs `catalog.write`. Problem codes:
         *     `source_locale` (409), `locale_in_fallback` (409, remove it from
         *     the fallback graph first), `locale_not_found` (404).
         */
        delete: operations["removeLocale"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/fallback-graph": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's fallback graph
         * @description Exactly the `fallback` member releases publish
         *     (runtimes/SPEC.md §1.1). Empty until set. Needs
         *     `translations.read`.
         */
        get: operations["getFallbackGraph"];
        /**
         * Replace a project's fallback graph
         * @description Keys are project locales or `*` (every locale without its own
         *     entry); chains name project locales, in order. Cycles are
         *     refused. The first write takes no `If-Match`; later ones need the
         *     current `ETag` (`428` without it). Needs `catalog.write`. Problem
         *     codes (400): `fallback_unknown_locale`, `fallback_self_reference`,
         *     `fallback_duplicate`, `fallback_cycle`, `fallback_invalid_locale`.
         */
        put: operations["putFallbackGraph"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        /**
         * A message's translations, by locale
         * @description Needs `translations.read`.
         */
        get: operations["listMessageTranslations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        /**
         * A message's translation in a locale
         * @description Needs `translations.read`.
         */
        get: operations["getTranslation"];
        /**
         * Write a message's translation in a locale
         * @description Parses the text for the locale, checks it structurally against
         *     the source revision it was made from (`source_revision`, default
         *     the current one) and appends a revision with provenance
         *     (`origin`, default `human`, and `origin_detail`). Error findings
         *     reject the write with `422 structural_qa_failed` and the
         *     `findings`; warnings are stored and returned. New text gets the
         *     project's review policy (`needs_review` when review is required,
         *     else `approved`) unless `state` asks otherwise; approving needs
         *     `translations.review` for the locale. A person asking for
         *     `approved` on text they just wrote approves their own work (RFC
         *     0006 §15 Q6): while another active member could review the
         *     locale, the text lands as `needs_review` (the response's `state`
         *     says so; imports and token writes are exempt); when no one else
         *     could, it lands approved and the revision is marked
         *     `self_review`. Unchanged text is no new
         *     revision (asking a different state for it is a review, with the
         *     rule of `reviewTranslation`). Creating takes no `If-Match`; changing needs the
         *     current `ETag` (`428` without it). Needs `translations.write` for
         *     the locale. Problem codes: `locale_not_found` (404),
         *     `source_locale` (409), `invalid_message`, `invalid_state`,
         *     `invalid_origin`, `invalid_origin_detail`,
         *     `invalid_source_revision`, `write_cannot_reject` (400),
         *     `review_forbidden` (403), `translation_conflict` (409).
         */
        put: operations["putTranslation"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/tm-matches": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        /**
         * Translation-memory matches for one unit
         * @description The memory's matches for one translation unit — the message the
         *     key names, in the locale — scored as `lookupTranslationMemory`
         *     scores them, from the tenant-wide units and the project's own,
         *     best first. It is the translation workspace's read, and the only
         *     one a member with visibility `assigned` has (RFC 0006 §3.3): the
         *     unit must be in an assignment of theirs, and anything else —
         *     another unit, another project — is `404`. A match is text and a
         *     score, never the matched unit: for an `assigned` member no id or
         *     key of the remembered unit is returned (`message_key` is omitted),
         *     so the memory does not tell a vendor what else is in the
         *     project. It records no hit. Needs `knowledge.read`. Problem
         *     codes: `invalid_query`, `invalid_syntax` (400).
         */
        get: operations["listUnitTMMatches"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/ai-suggestions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        /**
         * AI suggestions for one unit
         * @description The newest suggestions (at most 5) for one translation unit — the
         *     message the key names, in the locale — as its workspace shows
         *     them: the text, its score with the explanation, the routed
         *     action, risks and findings. It is the read a member with
         *     visibility `assigned` has (RFC 0006 §3.3): the unit must be in an
         *     assignment of theirs, and anything else is `404`. The job,
         *     provider, model, calls, cost and the translation-memory units it
         *     drew on are not part of it; they stay with
         *     `getAISuggestion`. `decidable` is false for an `assigned`
         *     member, who writes the translation instead of accepting a
         *     suggestion. Needs `intelligence.read`.
         */
        get: operations["listUnitAISuggestions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/revisions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        /**
         * A translation's history with provenance, newest first
         * @description Needs `translations.read`.
         */
        get: operations["listTranslationRevisions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/reviews": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Record a review decision
         * @description Moves the translation to `state` and appends a `review` revision
         *     naming the reviewer; the text and its provenance stay. Approving
         *     and rejecting need `translations.review` for the locale, other
         *     states `translations.write`. An author never approves their own
         *     work when someone else could review it (RFC 0006 §15 Q6): while
         *     another active member holds `translations.review` for the locale
         *     in this project, approving or rejecting the text the caller wrote
         *     is refused with `own_text` (403). When no one else could review,
         *     the author may; the `review` revision is then marked
         *     `self_review`. Imported text is not authored by the reviewer and
         *     stays reviewable. Problem codes:
         *     `review_forbidden` (403), `own_text` (403), `invalid_transition`
         *     (409), `invalid_state` (400).
         */
        post: operations["reviewTranslation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/translations": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's translations in one or more locales, by key
         * @description Every translation in the given locales (`locale`, repeatable, 1
         *     to 20), across messages, ordered by message key and then locale,
         *     with the message's `key`, `namespace` and `message_state`, the
         *     `source_revision` it was made against and the derived `outdated`.
         *     Filters combine: `state` (repeatable review states), `origin`
         *     (repeatable provenance), `outdated`, `namespace`, `key_prefix`
         *     and `message_state`. Locales the project no longer has list
         *     nothing. Keys and namespaces come from Localization's view of
         *     the catalog: current when a bulk upsert returns, and after other
         *     message writes once their events are processed (usually within a
         *     second). One query per page. Needs `translations.read`. Problem
         *     codes: `invalid_locale`, `too_many_locales`, `invalid_state`,
         *     `invalid_origin`, `invalid_message_state` (400).
         */
        get: operations["listProjectTranslations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/translation-stats": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Per-locale status of a project's translations
         * @description For every locale of the project (the source included, by code):
         *     how many of its active messages are translated (a usable — not
         *     rejected — translation exists), missing (none, or rejected) and
         *     outdated (usable but made against an older source revision), and
         *     its translations of active messages per review state. The source
         *     locale counts every active message as translated and approved.
         *     Computed in one query on each request, from Localization's view
         *     of the catalog. Needs `translations.read`.
         */
        get: operations["getTranslationStats"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/translation-imports": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Import translations in bulk
         * @description Up to 500 translations by message key and locale, written with
         *     provenance `import` (put the job and source system in
         *     `origin_detail`) in one transaction. Re-importing unchanged text
         *     is `unchanged`. Items fail on their own (`results[].error.code`:
         *     `message_not_found`, `locale_not_found`, `invalid_locale`,
         *     `forbidden` for a locale outside the caller's scope,
         *     `source_locale`, `structural_qa_failed` with `findings`,
         *     `invalid_message`, `review_forbidden`, `duplicate_item`, …)
         *     without failing the batch. Needs `translations.write` per
         *     locale. Problem codes: `too_many_items` (400).
         */
        post: operations["importTranslations"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Environments of a project
         * @description By name. Every project has `development`, `preview`, `staging`
         *     and `production`; they are created on first use. Needs
         *     `releases.read`.
         */
        get: operations["listEnvironments"];
        put?: never;
        /**
         * Add a custom environment
         * @description A QA stage of your own. Without `policy` it ships everything not
         *     rejected. Branch previews are not created here: they follow
         *     their branch, and their names (`pr-<n>`, `br-<hash>`) are
         *     reserved. Needs `releases.publish`. Problem codes:
         *     `environment_exists`, `too_many_branches` (409),
         *     `invalid_environment`, `invalid_policy` (400).
         */
        post: operations["createEnvironment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        /**
         * An environment, its policy and the release it serves
         * @description Needs `releases.read`.
         */
        get: operations["getEnvironment"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Change an environment's eligibility policy or approval requirement
         * @description The policy decides what the next publish ships and which
         *     releases may be promoted here; the release served keeps serving.
         *
         *     `approval` requires every publish and promote into the
         *     environment to be approved by `n` distinct people of `from`, none
         *     of them the requester (RFC 0006 §5.1): such a move answers `202`
         *     with a release request and moves no pointer. `clear_approval`
         *     switches the requirement off; leaving both out keeps it as it
         *     is. Changing who must approve is governance, not publishing, so
         *     it also needs `workflows.manage` (owner and admin by default). A
         *     request already pending keeps showing the requirement it was made
         *     under; its deploy must also meet the environment's requirement of
         *     the moment. Policy and approval change together, under one
         *     `If-Match`.
         *
         *     Needs `releases.publish`. Problem codes: `invalid_policy`,
         *     `invalid_request` (400: `approval` together with
         *     `clear_approval`), `invalid_approval` (422: `n` outside 1–10, not
         *     exactly one of `member`, `role` or `group`, or
         *     `distinct_from_requester` not true — self-approval is not
         *     offered), `approval_on_branch` (422: a branch environment's
         *     policy is fixed).
         */
        patch: operations["updateEnvironment"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/promotions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Point the environment at an existing release
         * @description Moves the pointer; nothing is rebuilt. The environment's policy
         *     must cover the policy the release was built under, so a
         *     development or preview release (which ships drafts) can't reach
         *     production; the default path is to publish to `staging` (approved
         *     text, like production) and promote that release to `production`.
         *     `release_ineligible`'s detail names both policies and what
         *     differs. Promoting the release already served changes nothing,
         *     so a retry is safe. A branch release is never promoted
         *     (`branch_release_not_promotable`): it holds text that exists only
         *     on its branch.
         *
         *     The environment's completeness requirement applies here exactly
         *     as it does to a publish, because a release that may not be
         *     published straight to production may not reach it by the side
         *     door either: a promotion that would not meet it is refused with
         *     `policy_not_met` and can be overridden with `force` and a
         *     `force_reason`, which the deployment records.
         *
         *     An environment with an `approval` requirement holds the promote
         *     as a release request: the answer is `202` with the request, no
         *     pointer moves, and the release is deployed once the requirement
         *     is met (see `release-requests`). A forced promote waits too:
         *     `force` overrides the completeness requirement, never the
         *     approval. A promote into an environment with an active staged
         *     rollout is refused (`rollout_active`): it would replace the
         *     stable side under installations the rollout compares with it;
         *     complete or abort the rollout first.
         *
         *     Needs `releases.publish`. Problem codes: `force_reason_required`,
         *     `invalid_force_reason` (400), `release_not_found` (404),
         *     `release_ineligible`, `branch_release_not_promotable`,
         *     `policy_not_met`, `rollout_active` (409), `storage_unavailable` (503).
         */
        post: operations["promoteRelease"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/release-previews": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What publishing to the environment would ship now (dry run)
         * @description Runs the build `publishRelease` runs, under the environment's
         *     policy, and stores nothing: no release, no artifacts, no
         *     deployment, no events (a default environment the project hasn't
         *     used yet is shown as it will be created). Returns per-locale
         *     message counts, the per-locale message IDs added, changed and
         *     removed compared with the release the environment serves now,
         *     and `new_artifacts`, what the publish would upload. A catalog
         *     that can't be released is a `200` with `releasable: false` and
         *     every `not_releasable` problem found (publishing would fail with
         *     `422 not_releasable`). The answer is only as current as the
         *     request: a translation saved in between changes what a publish
         *     ships. Needs `releases.read`. Problem codes:
         *     `storage_unavailable` (503).
         */
        post: operations["previewRelease"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollbacks": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Point the environment back at a release it served before
         * @description Without `release_id`, the newest release the environment served
         *     that is older than the current one: rolling back twice goes two
         *     steps back, so send `release_id` when a retry must not. Moves
         *     the pointer only. Needs `releases.publish`. Problem codes:
         *     `release_not_found` (404), `no_rollback_target`,
         *     `not_in_history` (409).
         */
        post: operations["rollbackEnvironment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/deployments": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        /**
         * The environment's history of pointer moves
         * @description Newest first. Needs `releases.read`.
         */
        get: operations["listDeployments"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        /**
         * The environment's staged rollouts
         * @description Newest first: the active one, if any, and the history of ended
         *     ones. Needs `releases.read`.
         */
        get: operations["listRollouts"];
        put?: never;
        /**
         * Start serving a candidate release to a share of installations
         * @description The environment's manifest gains a signed `rollout` member
         *     (runtimes/SPEC.md §1.4) naming the candidate, `percent` and a
         *     salt fixed for the rollout's life; a runtime activates the
         *     candidate when its installation's cohort falls below `percent`,
         *     and every other installation — and every runtime that predates
         *     rollouts — keeps the release the environment points at. The
         *     pointer does not move until the rollout is completed.
         *
         *     The candidate is held to what a promote into the environment is
         *     held to: a main-catalog release of the project the environment's
         *     policy covers, through the completeness requirement or forced
         *     with a `force_reason` that the completing deployment records.
         *     The environment must already serve a release with the same
         *     source locale, and may have one active rollout at a time. A
         *     rollout ends by itself only at `max_duration_seconds` (default 14 days),
         *     when it is aborted. An environment that requires release
         *     approvals refuses rollouts (`rollout_needs_approval`) until a
         *     release request can carry one; publish or promote there instead.
         *
         *     Needs `releases.publish`. Problem codes: `invalid_percent`,
         *     `invalid_max_duration`, `force_reason_required`,
         *     `invalid_force_reason`, `invalid_idempotency_key` (400),
         *     `release_not_found` (404), `rollout_active`,
         *     `rollout_no_stable`, `rollout_candidate_served`,
         *     `rollout_branch_environment`, `rollout_source_locale`,
         *     `rollout_needs_approval`, `release_ineligible`,
         *     `branch_release_not_promotable`, `policy_not_met` (409),
         *     `idempotency_key_reused` (422), `storage_unavailable` (503).
         */
        post: operations["startRollout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        /**
         * A rollout
         * @description Needs `releases.read`.
         */
        get: operations["getRollout"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Change the share of installations in the candidate
         * @description Any percent may follow any other, down as well as up (RFC 0006
         *     §15 Q5); the salt stays, so the installations in the candidate
         *     at a lower percent are among those at a higher one. Advancing
         *     needs no approval. Needs `releases.publish`. Problem codes:
         *     `invalid_percent` (400), `rollout_ended` (409).
         */
        patch: operations["advanceRollout"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}/completion": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Make the candidate the environment's release
         * @description The pointer moves to the candidate as a promote moves it — the
         *     deployment records the force the rollout started with, if any —
         *     and the manifest drops `rollout`. The completeness requirement
         *     is not asked again: it was asked when the rollout started, about
         *     the same immutable release. Completing an ended rollout is
         *     `rollout_ended`. Needs `releases.publish`. Problem codes:
         *     `rollout_ended` (409), `storage_unavailable` (503).
         */
        post: operations["completeRollout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}/abort": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * End the rollout and keep the environment's release
         * @description The manifest drops `rollout` and every installation returns to
         *     the release the environment points at on its next refresh.
         *     Aborting needs no approval and is never delayed (RFC 0006 §5.2).
         *     A rollback of the environment aborts its rollout too. Needs
         *     `releases.publish`. Problem codes: `rollout_ended` (409).
         */
        post: operations["abortRollout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/release-requests": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's release requests
         * @description Newest first, optionally in one `environment` and one `state`.
         *     Needs `releases.read`.
         */
        get: operations["listReleaseRequests"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        /**
         * A release request
         * @description What it would deploy, where, who asked, the requirement its
         *     approvers were asked for, what the completeness requirement said
         *     when it was made, and whether the requester forced it and why.
         *     Needs `releases.read`.
         */
        get: operations["getReleaseRequest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}/approvals": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Grant or deny a release request
         * @description A person's decision on the request's current approval — the one
         *     the project's release-approval workflow asked for — recorded as
         *     `POST …/approvals/{approval}/decisions` records it. Once enough
         *     distinct people other than the requester have granted it, the
         *     workflow deploys the release as the last of them: Release checks
         *     the requirement itself, runs the completeness requirement again
         *     and moves the pointer, and the edge serves it within seconds. A
         *     denial closes the request; nothing moves.
         *
         *     Human-only: an API token or an MCP agent is refused
         *     (`person_required`, 403) — no scope grants `approvals.decide`.
         *     The caller needs `approvals.decide` in the request's environment,
         *     must be of the requirement's party (`not_eligible`, 403), and
         *     must not be the requester (`own_text`, 403: four-eyes). Problem
         *     codes: `approval_not_requested` (409: the workflow has not asked
         *     for the approval yet; retry shortly), `release_request_closed`
         *     (409: deployed, denied, withdrawn or refused), `approval_closed`,
         *     `approval_superseded` (409), `invalid_approval` (422: a reason
         *     over 2,000 characters).
         */
        post: operations["decideReleaseRequest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}/withdrawal": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Take a pending release request back
         * @description Its requester, or anyone who may publish to the project. No
         *     pointer moves. A newer publish or promote into the same
         *     environment withdraws the pending request by itself. Needs
         *     `releases.publish`. Problem codes: `invalid_withdraw_reason`
         *     (400), `release_request_closed` (409).
         */
        post: operations["withdrawReleaseRequest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/releases": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Releases of a project
         * @description Newest first. Needs `releases.read`.
         */
        get: operations["listReleases"];
        put?: never;
        /**
         * Publish a release to an environment
         * @description Builds one artifact per locale and namespace from the active
         *     messages and the translations the environment's policy makes
         *     eligible, stores the ones storage doesn't have yet, records the
         *     immutable release and points the environment at it; the edge
         *     serves it within seconds. Publishing an unchanged catalog
         *     uploads nothing. A branch environment (`kind: branch`) is built
         *     from the main catalog plus its branch's overlay; it publishes
         *     itself when the branch changes, so publishing one by hand is
         *     rarely needed.
         *
         *     An environment can require the locales it ships to be complete.
         *     A publish that would not meet it is refused with
         *     `policy_not_met`, whose detail names the locales that are short
         *     and by how much. It can be overridden with `force` and a
         *     `force_reason`, which the deployment records; an override with
         *     no reason is `force_reason_required` and a reason with no
         *     override is `invalid_force_reason`.
         *
         *     An environment with an `approval` requirement holds the publish
         *     as a release request: the release is built and recorded, but the
         *     answer is `202` with the release's `id` and the request, and the
         *     environment keeps serving what it served. The release is deployed
         *     once the requirement is met (see `release-requests`). A forced
         *     publish waits too: `force` overrides the completeness
         *     requirement, never the approval, and the approvers see that it
         *     was forced and why. A retry with the same `Idempotency-Key`
         *     answers the same request. A publish into an environment with an
         *     active staged rollout is refused (`rollout_active`); complete or
         *     abort the rollout first.
         *
         *     Needs `releases.publish`. Problem codes: `invalid_environment`,
         *     `invalid_note`, `force_reason_required`, `invalid_force_reason`
         *     (400), `policy_not_met`, `rollout_active` (409), `not_releasable`
         *     (422), `storage_unavailable` (503).
         */
        post: operations["publishRelease"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/releases/{release}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        /**
         * A release, its counts and manifest digest
         * @description Needs `releases.read`.
         */
        get: operations["getRelease"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/releases/{release}/diff": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        /**
         * What changed since another release, per locale
         * @description Message IDs added, changed and removed in each locale, compared
         *     with `base` (default: the release's parent, what its environment
         *     served before it; without one, everything is added). Needs
         *     `releases.read`. Problem codes: `release_not_found` (404),
         *     `storage_unavailable` (503).
         */
        get: operations["getReleaseDiff"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/releases/{release}/manifest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        /**
         * The signed manifest of a release for an environment
         * @description Byte for byte what glossa-edge serves when `environment` serves
         *     this release (runtimes/SPEC.md §1.1): what `glossa pull
         *     --release` writes as a bundle's `manifest.json`. Needs
         *     `releases.read`.
         */
        get: operations["getReleaseManifest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/releases/{release}/artifacts/{digest}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
                /** @description The artifact's SHA-256, as the manifest names it. */
                digest: string;
            };
            cookie?: never;
        };
        /**
         * One artifact of a release, as stored
         * @description The exact bytes the manifest's `sha256` covers
         *     (runtimes/SPEC.md §1.2), for bundling
         *     (`glossa pull --release` writes `a/<sha256>.json`). Needs
         *     `releases.read`. Problem codes: `storage_unavailable` (503).
         */
        get: operations["getReleaseArtifact"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/release-signing-keys": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The public keys manifests are signed with
         * @description Configure runtimes with these (runtimes/SPEC.md §1.3). Active
         *     keys sign every new manifest; retired ones are still listed
         *     while runtimes may hold manifests only they signed. Rotation:
         *     a new key is added (manifests carry both signatures), runtimes
         *     move to it, the old one is retired. Needs `releases.read`.
         */
        get: operations["listReleaseSigningKeys"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/delivery-keys": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Publishable delivery keys, including revoked ones
         * @description Needs `releases.read`.
         */
        get: operations["listDeliveryKeys"];
        put?: never;
        /**
         * Create a publishable delivery key
         * @description The key runtimes fetch releases from glossa-edge with
         *     (`/v1/{key}/{environment}/manifest.json`). It is public by
         *     design — it ships in browser bundles — scoped to this project,
         *     read-only, and grants nothing on this API. Needs
         *     `releases.publish`. Problem codes: `invalid_key_name` (400).
         */
        post: operations["createDeliveryKey"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}/scope": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A delivery key `id` (not the key itself). */
                delivery_key: components["parameters"]["DeliveryKeyPath"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Change what a delivery key may read
         * @description Replaces the key's scope: the `environments` it reads and
         *     whether it reads branch previews. The key itself never changes,
         *     so a bundle that ships it keeps working; the edge follows within
         *     its key cache TTL (30 s by default) plus any CDN max-age. A
         *     revoked key takes no scope. Needs `releases.publish`. Problem
         *     codes: `invalid_key_scope` (400), `key_revoked` (409).
         */
        put: operations["setDeliveryKeyScope"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A delivery key `id` (not the key itself). */
                delivery_key: components["parameters"]["DeliveryKeyPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Revoke a delivery key
         * @description The edge answers 404 for it within its key cache TTL (30 s by
         *     default) plus any CDN max-age. It stays listed as revoked.
         *     Needs `releases.publish`. Problem codes: `key_revoked` (409).
         */
        delete: operations["revokeDeliveryKey"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/preview-origins": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Where the in-product editor may run
         * @description The origins of this project's preview deployments (RFC 0004
         *     §5.2). The list is short by design and never paged. Needs
         *     `catalog.read`: the authorize popup has to say where the editor
         *     may run, so everyone who can see the project can see it.
         */
        get: operations["listPreviewOrigins"];
        put?: never;
        /**
         * Register a preview origin
         * @description Says that the in-product editor may run on a page served from
         *     `origin`, and that CORS may answer it on the operations the
         *     overlay uses. Registering one is the decision that a person's
         *     permissions may be borrowed by a page served from there, so it
         *     needs `tokens.manage` — credential surface, not content — and
         *     a project keeps at most 20. `http` is accepted on loopback only,
         *     for a developer's own run. Problem codes: `invalid_origin` (400),
         *     `origin_registered`, `too_many_preview_origins` (409).
         */
        post: operations["registerPreviewOrigin"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/preview-origins/{preview_origin}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A registered preview origin's `id`. */
                preview_origin: components["parameters"]["PreviewOriginPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Stop allowing the editor on an origin
         * @description Removes the registration and, with it, every in-context grant
         *     minted for that origin: editor sessions on it end now, not when
         *     the last 15-minute grant expires. CORS stops answering the
         *     origin with the same request. Needs `tokens.manage`.
         */
        delete: operations["unregisterPreviewOrigin"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/in-context-grants": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Mint the in-product editor's credential
         * @description Called by Studio's `/in-context/authorize` popup, with the
         *     person's session (RFC 0004 §5.2). It checks that `origin` is a
         *     registered preview origin of this project and mints a bearer
         *     token that:
         *
         *     - acts as the person, so every edit the editor makes names them;
         *     - allows the person's own permissions intersected with
         *       `catalog.read`, `knowledge.read`, `translations.read`,
         *       `translations.write`, `intelligence.read` and
         *       `intelligence.translate`, locale scopes intact, and never more;
         *     - is bound to this project and to `origin`: a request from any
         *       other origin is refused;
         *     - lives 15 minutes and cannot be refreshed. The overlay keeps it
         *       in memory and renews it by opening the popup again.
         *
         *     Only a signed-in person may mint one — an API token doing so
         *     would launder a tenant credential into a person's — so this
         *     operation takes no bearer. `token` is returned once and never
         *     again. Problem codes: `invalid_origin` (400),
         *     `origin_not_registered` (403), `person_grant_only` (403),
         *     `no_in_context_permissions` (403).
         */
        post: operations["createInContextGrant"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-lookups": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Find translation-memory matches for a message
         * @description Parses `source` (MF1 by default) and matches its normalized form —
         *     placeholders by position, markup as tags — against active units
         *     of the locale pair: exact matches (same text and placeholder
         *     types) score 100, or 101 when the unit was approved for the same
         *     `message_key` in the same `namespace` of `project_id`; fuzzy
         *     matches (trigram similarity) score 50–99. Each target is renamed
         *     to the query's variable names. By default a lookup sees
         *     tenant-wide units and `project_id`'s; `all_projects` widens it to
         *     the tenant. `count_hits` records the lookup in each returned
         *     unit's `hit_count`. Each match carries its target in MF2
         *     (`target`) and in the syntax asked for (`target_text`: MF1 by
         *     default for an MF1 query, falling back to MF2 with
         *     `target_syntax_fallback` when MF1 can't express it). Needs `knowledge.read`. Problem codes:
         *     `invalid_query`, `invalid_locale`, `invalid_syntax`,
         *     `invalid_message`, `message_too_long` (400).
         */
        post: operations["lookupTranslationMemory"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-concordance": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Find active units containing a phrase
         * @description Case-insensitive substring search over the normalized source (or
         *     target) of active units, closest first — how a translator checks
         *     how a phrase was translated before. Scope as for lookups. Needs
         *     `knowledge.read`. Problem codes: `invalid_query`,
         *     `invalid_locale` (400).
         */
        get: operations["searchTranslationMemory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-units": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Translation-memory units, active or retired
         * @description Units are derived from approved translations; retired ones are
         *     their history. `translation` lists one translation's units.
         *     Needs `knowledge.read`.
         */
        get: operations["listTranslationMemoryUnits"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-units/{unit}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A translation-memory unit `id`. */
                unit: components["parameters"]["TMUnitPath"];
            };
            cookie?: never;
        };
        /**
         * A translation-memory unit
         * @description Needs `knowledge.read`.
         */
        get: operations["getTranslationMemoryUnit"];
        put?: never;
        post?: never;
        /**
         * Retire a unit
         * @description Takes the unit out of matching (`retired_reason: deleted`); it
         *     stays listed as history. Retiring a retired unit changes nothing.
         *     A later approval of its translation derives a new unit. Needs
         *     `knowledge.write`.
         */
        delete: operations["retireTranslationMemoryUnit"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/term-concepts": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Termbase concepts
         * @description `project` lists the concepts that apply to it (its own and the
         *     tenant-wide ones); `q` searches term texts (in `locale`, when
         *     given) and definitions. Needs `knowledge.read`. Problem codes:
         *     `invalid_query`, `invalid_locale` (400).
         */
        get: operations["listTermConcepts"];
        put?: never;
        /**
         * Add a concept with its terms
         * @description Tenant-wide, or for one project (`project_id`). Needs
         *     `knowledge.write`. Problem codes: `invalid_concept`,
         *     `invalid_term`, `invalid_term_status`, `invalid_part_of_speech`,
         *     `duplicate_term`, `invalid_locale`, `unknown_project` (400).
         */
        post: operations["createTermConcept"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/term-concepts/{concept}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        /**
         * A concept with its terms
         * @description Needs `knowledge.read`.
         */
        get: operations["getTermConcept"];
        /**
         * Replace a concept and its terms
         * @description The body replaces the concept's content and its whole term list
         *     (terms that stay keep their `id`); its scope never changes.
         *     Unchanged content is no new version. Needs `knowledge.write`.
         *     Problem codes as for creating.
         */
        put: operations["replaceTermConcept"];
        post?: never;
        /**
         * Delete a concept
         * @description Its history stays readable, ending in a `deleted` revision.
         *     `If-Match` is optional; when sent it must match. Needs
         *     `knowledge.write`.
         */
        delete: operations["deleteTermConcept"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/term-concepts/{concept}/revisions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        /**
         * A concept's history, newest first
         * @description Full snapshots, also after the concept was deleted. Needs `knowledge.read`.
         */
        get: operations["listTermConceptRevisions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/term-recognitions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Find termbase terms in a text
         * @description Word-based and locale-aware: case folded unless a term is
         *     case-sensitive, short inflectional endings tolerated
         *     (workspace → workspaces), scripts without spaces (Japanese,
         *     Chinese, Thai) matched as substrings, overlaps resolved
         *     leftmost-longest. With `syntax`, `text` is parsed as a message
         *     and recognition runs over its visible text (placeholders become
         *     U+FFFC), returned as `analyzed_text`; `start` and `end` are
         *     Unicode code point offsets into it. `target_locale` adds each
         *     concept's terms in that locale. Stores nothing. Needs
         *     `knowledge.read`. Problem codes: `invalid_query`,
         *     `invalid_locale`, `invalid_syntax`, `invalid_message` (400).
         */
        post: operations["recognizeTerms"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/terminology-checks": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Check a translation against the termbase
         * @description Terminology QA (intent §29.3): `term_missing` (warning) when a
         *     concept recognized in the source has none of its preferred or
         *     admitted target terms in the translation; `term_forbidden`
         *     (error for forbidden, warning for deprecated terms) for every
         *     forbidden or deprecated target term used. With `syntax`, both
         *     texts are parsed as messages and checked as visible text. Spans
         *     are code point offsets into `source_text` or `target_text`.
         *     Deterministic; stores nothing. Needs `knowledge.read`. Problem
         *     codes as for recognition.
         */
        post: operations["checkTerminology"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/terminology-findings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Terminology QA over a project's translations
         * @description Runs the checks of `POST …/terminology-checks` server-side over
         *     a project's translations in the given locales (`locale`,
         *     repeatable, 1 to 20), each against its message's current source,
         *     both as visible text, with the project's and the tenant-wide
         *     concepts — `glossa terms check` and `glossa check
         *     --terminology`. A page scans up to `page_size` translations of
         *     active messages in key order and lists those with findings
         *     (`items`, each with its message key); `checked` counts the
         *     translations it scanned per locale, so a client sums pages for
         *     totals. A page can list no items and still have a
         *     `next_page_token`. Filters: `state` (repeatable; default every
         *     state but `rejected`), `namespace`, `key_prefix`. Each page is
         *     one read per context (the termbase, the translations, their
         *     sources), never one per translation. Stores nothing. Needs
         *     `knowledge.read`, `translations.read` and `catalog.read`.
         *     Problem codes: `invalid_locale`, `too_many_locales`,
         *     `too_many_keys`, `invalid_state` (400).
         */
        get: operations["listProjectTerminologyFindings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/style-guides": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Style guides
         * @description Every guide, or only tenant-level ones (`tenant_only`), one
         *     project's, or one locale's. Needs `knowledge.read`.
         */
        get: operations["listStyleGuides"];
        put?: never;
        /**
         * Add the style guide for a scope
         * @description A scope is any combination of `project_id`, `locale` (which
         *     covers its descendants: `de` applies to `de-AT`) and `namespace`
         *     (which needs a project); none is the tenant's guide. Each scope
         *     has one guide. Needs `knowledge.write`. Problem codes:
         *     `style_guide_exists` (409), `invalid_style_guide`,
         *     `invalid_style_rule`, `namespace_needs_project`,
         *     `invalid_locale`, `unknown_project` (400).
         */
        post: operations["createStyleGuide"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/style-guides/{style_guide}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        /**
         * A style guide
         * @description Needs `knowledge.read`.
         */
        get: operations["getStyleGuide"];
        /**
         * Replace a style guide's content
         * @description Replaces the name, fields and rules; the scope never changes.
         *     Unchanged content is no new version. Needs `knowledge.write`.
         *     Problem codes: `invalid_style_guide`, `invalid_style_rule` (400).
         */
        put: operations["replaceStyleGuide"];
        post?: never;
        /**
         * Delete a style guide
         * @description Its versions stay readable, ending in a `deleted` one. `If-Match`
         *     is optional; when sent it must match. Needs `knowledge.write`.
         */
        delete: operations["deleteStyleGuide"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/style-guides/{style_guide}/versions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        /**
         * A style guide's versions, newest first
         * @description Full snapshots, also after the guide was deleted. Needs `knowledge.read`.
         */
        get: operations["listStyleGuideVersions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/effective-style-guide": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The style that applies to a project, locale and namespace
         * @description Every applicable guide merged field by field, the narrowest
         *     winning: a namespace beats a locale, a deeper locale a shallower
         *     one, a locale a project, a project the tenant. A narrower guide
         *     replaces a broader rule with the same `id`, or switches it off
         *     (`disabled`). `sources` names each guide version used, broadest
         *     first. Without `project`, only tenant-level guides apply; without
         *     `locale`, no locale guide does. Needs `knowledge.read`. Problem
         *     codes: `invalid_locale` (400).
         */
        get: operations["getEffectiveStyleGuide"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-providers": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Configured AI providers
         * @description The tenant's providers by name — never their API keys
         *     (`api_key_set` says whether one is stored). Needs
         *     `intelligence.read`.
         */
        get: operations["listAIProviders"];
        put?: never;
        /**
         * Configure an AI provider with the tenant's own key
         * @description `name` is what routing policies route to (the default routing
         *     uses `anthropic`). `api_key` is write-only: it is sealed at rest
         *     (AES-256-GCM, bound to the tenant and provider) and never
         *     returned. `base_url` must be https and may not point at private
         *     or loopback addresses unless the deployment allows it
         *     (`GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS`); `models` is an allow-list
         *     (empty allows any). Needs `intelligence.manage`. Problem codes:
         *     `invalid_provider` (400), `provider_name_taken` (409).
         */
        post: operations["createAIProvider"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-providers/{ai_provider}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An AI provider `id`. */
                ai_provider: components["parameters"]["AIProviderPath"];
            };
            cookie?: never;
        };
        /**
         * An AI provider
         * @description Never its key. Needs `intelligence.read`.
         */
        get: operations["getAIProvider"];
        put?: never;
        post?: never;
        /**
         * Remove an AI provider
         * @description Refused while a stored routing policy routes to it. Needs
         *     `intelligence.manage`. Problem code: `provider_in_use` (409).
         */
        delete: operations["deleteAIProvider"];
        options?: never;
        head?: never;
        /**
         * Change an AI provider
         * @description Absent members keep their value. `api_key` replaces the key,
         *     `clear_api_key` removes it. Renaming a provider a stored routing
         *     policy routes to is refused. Needs `intelligence.manage`.
         *     Problem codes: `invalid_provider` (400), `provider_name_taken`,
         *     `provider_in_use` (409).
         */
        patch: operations["updateAIProvider"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-settings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The tenant's AI settings
         * @description Consent to send text to AI providers (off until someone turns it
         *     on; who and when is kept), the concurrency cap on running jobs
         *     and the monthly budget in micro-USD (0 allows no provider calls:
         *     a hard stop). Defaults until saved, at version 0. Needs
         *     `intelligence.read`.
         */
        get: operations["getAISettings"];
        /**
         * Change the tenant's AI settings
         * @description Absent members keep their value; `If-Match` applies when sent.
         *     Needs `intelligence.manage`. Problem code: `invalid_settings`
         *     (400).
         */
        put: operations["putAISettings"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-prices": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The AI price table
         * @description USD per million tokens by `<provider>/<model>`: the deployment's
         *     defaults, the tenant's overrides and the effective table budgets
         *     are charged with (an unpriced model costs 0 and its spend is
         *     flagged `priced: false`). The ETag is the settings'. Needs
         *     `intelligence.read`.
         */
        get: operations["getAIPrices"];
        /**
         * Replace the tenant's price overrides
         * @description Needs `intelligence.manage`. Problem code: `invalid_prices` (400).
         */
        put: operations["putAIPrices"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-budget": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The monthly budget and this month's spend
         * @description The cap (set with `ai-settings`), what this calendar month (UTC)
         *     spent, what remains and the spend per provider and model. A call
         *     whose upper-bound estimate would pass the cap is refused before
         *     anything is sent. Needs `intelligence.read`.
         */
        get: operations["getAIBudget"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-spend": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The spend ledger
         * @description Every priced provider call since `since` (default: the start of
         *     this month), newest first. Needs `intelligence.read`.
         */
        get: operations["listAISpend"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-routing-policy": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The tenant's AI routing policy
         * @description Which provider and model each task (`translate`, `review`,
         *     `explain`, `assess`) runs on, per target locale, with ordered
         *     fallbacks. Without a stored policy the default applies
         *     (`source: default`: Anthropic Claude Sonnet 5 for translate and
         *     review, Claude Haiku 4.5 for the self-assessment). Needs
         *     `intelligence.read`.
         */
        get: operations["getAIRoutingPolicy"];
        /**
         * Replace the tenant's AI routing policy
         * @description Every route must name a configured provider whose model
         *     allow-list admits the model. Needs `intelligence.manage`.
         *     Problem code: `invalid_routing_policy` (400).
         */
        put: operations["putAIRoutingPolicy"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-routing-policy": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The AI routing policy in effect for a project
         * @description The project's own (`source: project`), else the tenant's, else
         *     the default. The `ETag` is the project's own policy's: `"0"`
         *     while it has none (as the tenant policy's is while the default
         *     applies). Needs `intelligence.read`.
         */
        get: operations["getProjectAIRoutingPolicy"];
        /**
         * Replace a project's AI routing policy
         * @description As the tenant's, for one project. Needs `intelligence.manage`.
         *     Problem code: `invalid_routing_policy` (400).
         */
        put: operations["putProjectAIRoutingPolicy"];
        post?: never;
        /**
         * Remove a project's AI routing policy
         * @description The tenant's (or the default) applies again. Needs `intelligence.manage`.
         */
        delete: operations["deleteProjectAIRoutingPolicy"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-settings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's AI settings
         * @description Namespace policy tags (`sensitive`: never sent to a provider,
         *     always translated by people; `legal` and `marketing`: riskier,
         *     reviewed first), the locales auto-translate is on for (none by
         *     default) and the review routing of suggestions by confidence.
         *     Defaults until saved, at version 0. Needs `intelligence.read`.
         */
        get: operations["getProjectAISettings"];
        /**
         * Change a project's AI settings
         * @description Absent members keep their value. `review.auto_approve` (off by
         *     default) is accepted only with `auto_approve_environments` that
         *     all exist and ship approved translations (Release's eligibility
         *     policies), and is re-checked for every suggestion: when an
         *     environment stops shipping approved text, suggestions are routed
         *     `approve_recommended` instead, with an `action_note`. Needs
         *     `intelligence.manage`. Problem codes: `invalid_namespace_tags`,
         *     `invalid_review_policy`, `invalid_locale` (400),
         *     `auto_approve_ineligible` (422).
         */
        put: operations["putProjectAISettings"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-fills": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Fill locales with AI ("Fill with AI", `glossa translate`)
         * @description Queues one job per message whose translation in each locale is
         *     in the state `select` names — `missing` (none, or rejected; the
         *     default), `outdated` (made against an older source revision) or
         *     `missing_or_outdated` — among the listed `keys` or every active
         *     message, narrowed by `namespace` and `key_prefix`. Clients don't
         *     need to list keys to fill outdated translations. `include_outdated`
         *     is the older spelling of `missing_or_outdated`; listed `keys`
         *     without `select` are filled when missing or outdated. The fill
         *     records the effective `select`. Messages in `sensitive`
         *     namespaces are skipped (`skipped.sensitive`), listed keys that
         *     are current (`skipped.up_to_date`) or in a state the select
         *     leaves out (`skipped.not_selected`). A job exists once per
         *     message, locale, source revision and knowledge fingerprint: an
         *     existing one is reused (`jobs_existing`), a failed, dead or
         *     cancelled one queued again. `force` (with `keys`) also queues a
         *     current translation, and queues an existing job again whatever
         *     its state, so the in-product editor can ask for a second opinion
         *     on text that is already there. The response's `job_ids` name the
         *     jobs queued or reused, for a caller following a handful of them.
         *     `warnings` say when jobs will do
         *     little: consent off (only exact translation-memory matches are
         *     reused), no budget, no provider. `POST …/ai-fill-previews`
         *     answers what a fill would do without queueing anything. Needs
         *     `intelligence.translate` for every locale. Problem codes:
         *     `too_many_locales`, `too_many_keys`, `invalid_locale`,
         *     `invalid_query` (an unknown `select`, one `include_outdated`
         *     contradicts, or `force` without `keys`) (400),
         *     `locale_not_found` (404).
         */
        post: operations["createAIFill"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-fill-previews": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What a fill would do, without doing it (`glossa translate --dry-run`)
         * @description Takes a fill request and answers, per locale, what `POST
         *     …/ai-fills` would do with it, writing nothing — no fill, no job,
         *     no translation-memory hit count, no spend, no event. The same
         *     checks, selection and limits apply; then each message is decided
         *     the way its job would be: `existing` jobs (queued, running or
         *     finished) are reused, `tm_exact` messages have an exact
         *     translation-memory match that is reused without a provider call
         *     (when it validates), and the rest either call a provider
         *     (`provider`, priced in `cost`) or are `refused` —
         *     `sensitive` (never queued), `provider_consent`, `no_route` (no
         *     route to an enabled provider allowing the model) or
         *     `budget_exceeded` (this month's spend plus the calls before it
         *     leave no room for the call's upper bound). `cost` uses the price
         *     table in effect: `estimated_micro_usd` expects one draft and one
         *     self-assessment per message (prompts at about three characters a
         *     token, a draft twice its source); `max_micro_usd` is the bound
         *     the budget guard reserves (every repair, each call's whole
         *     `max_tokens`); `unpriced` flags a model without a price. Needs
         *     `intelligence.translate` for every locale. Problem codes: those
         *     of `createAIFill`.
         */
        post: operations["previewAIFill"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-fills/{ai_fill}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A fill `id`. */
                ai_fill: components["parameters"]["AIFillPath"];
            };
            cookie?: never;
        };
        /**
         * A fill and its jobs' states
         * @description Needs `intelligence.read`.
         */
        get: operations["getAIFill"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-fills/{ai_fill}/cancellation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A fill `id`. */
                ai_fill: components["parameters"]["AIFillPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel a fill's queued jobs
         * @description Running jobs finish. Cancelling twice changes nothing. Needs
         *     `intelligence.translate` for the fill's locales.
         */
        post: operations["cancelAIFill"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * AI translation jobs
         * @description Newest first. A job is `queued` → `running` → `succeeded`
         *     (with a suggestion), `skipped` (the message changed, was
         *     translated meanwhile or is gone), `failed` (for good:
         *     `failure_code` says why — `provider_consent`, `sensitive`,
         *     `invalid_output`, `budget_exceeded`, `no_route`,
         *     `provider_error`, `invalid_source`), `dead` (transient failures
         *     exhausted its attempts) or `cancelled`. Needs
         *     `intelligence.read`.
         */
        get: operations["listAIJobs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-jobs/{ai_job}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A job `id`. */
                ai_job: components["parameters"]["AIJobPath"];
            };
            cookie?: never;
        };
        /**
         * A job with its audit ledger
         * @description `audit` is the agent's ledger: every tool result in order — what
         *     it looked up, what it sent and what came back. Needs
         *     `intelligence.read`.
         */
        get: operations["getAIJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-jobs/{ai_job}/cancellation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A job `id`. */
                ai_job: components["parameters"]["AIJobPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel a queued job
         * @description Only queued jobs can be cancelled; cancelling a cancelled job
         *     changes nothing. Needs `intelligence.translate` for its locale.
         *     Problem code: `job_not_cancellable` (409).
         */
        post: operations["cancelAIJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-suggestions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * AI suggestions
         * @description Newest first, each with its message's current `source` (read in
         *     one catalog query per page). Needs `intelligence.read` and
         *     `catalog.read`.
         */
        get: operations["listAISuggestions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        /**
         * An AI suggestion with its confidence explanation
         * @description `score` (0–1) prioritizes review; it is never a promise of
         *     correctness. `explanation` lists each factor and its
         *     contribution ("why this?"); `provenance` names the provider,
         *     model, prompt version, translation-memory units, terms and
         *     style-guide version; `source` is the message as it is now. Needs
         *     `intelligence.read` and `catalog.read`.
         */
        get: operations["getAISuggestion"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/acceptance": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Accept a suggestion, as is or edited
         * @description Writes the message's translation: a revision with origin `ai`
         *     or `translation_memory` and an `origin_detail` naming provider,
         *     model, prompt version, TM units, terms, style version, score and
         *     explanation. It is `approved` when the caller may review the
         *     locale, else what the project's review policy says. With `text`
         *     (MF2 by default) the edit is accepted instead and its structured
         *     diff (edit distance, terms and style fields changed) recorded for
         *     the metrics. Needs `intelligence.translate` and
         *     `translations.write` for the locale. Problem codes:
         *     `suggestion_decided`, `suggestion_outdated`,
         *     `translation_conflict` (409), `translation_rejected` (422),
         *     `invalid_message`, `invalid_syntax` (400).
         */
        post: operations["acceptAISuggestion"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/rejection": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Reject a suggestion
         * @description Nothing is written. Needs `intelligence.translate` for the
         *     locale. Problem codes: `suggestion_decided` (409),
         *     `invalid_reason` (400).
         */
        post: operations["rejectAISuggestion"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-review-queue": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The review queue, riskiest first
         * @description Pending suggestions ordered by risk, not by key: lowest score
         *     first, then the most `risk_tags` (legal and marketing
         *     namespaces, forbidden terms, max length, missing plural
         *     categories). `locale` (repeatable) narrows it. Each item carries
         *     its message's current `source` (key, namespace, authored text,
         *     canonical MF2, revision), read in one catalog query per page, so
         *     a reviewer needs no request per item. Needs `intelligence.read`
         *     and `catalog.read`.
         */
        get: operations["getAIReviewQueue"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-disclosures": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Which provider saw which message
         * @description Every provider call a job made, even a failed one, with exactly
         *     what the provider was sent (RFC 0003 §7), newest first. Needs
         *     `intelligence.read`.
         */
        get: operations["listAIDisclosures"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/ai-metrics": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Acceptance rate and edit distance per locale
         * @description People's decisions on the project's suggestions since `since`
         *     (default: 30 days ago), per locale: accepted (as is or edited),
         *     rejected, the acceptance rate and the mean edit distance of
         *     accepted suggestions (0 for those accepted as is). Needs
         *     `intelligence.read`.
         */
        get: operations["getAIMetrics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/ai-eval-baseline": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The translation agent's eval baseline
         * @description The tracked metrics of the golden-set evals per locale pair and
         *     overall (`all`), as committed with the server
         *     (`internal/intelligence/evals/testdata/baseline.json`): a prompt
         *     or model change may not regress them (RFC 0003 §4). Needs
         *     `intelligence.read`.
         */
        get: operations["getAIEvalBaseline"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/import-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Import jobs
         * @description Newest first. Needs `integration.read`.
         */
        get: operations["listImportJobs"];
        put?: never;
        /**
         * Create an import job (then upload its file)
         * @description Creates a job in `awaiting_upload`; `PUT` the file to its
         *     `upload_url` next (within 24 hours). Catalog formats (`xliff`,
         *     `json`, `po`) belong to a project; `tmx` and `tbx` import into
         *     a project, or tenant-wide without `project_id`.
         *
         *     **Modes.** `merge` (default) creates what is missing and updates
         *     what changed, but never replaces an approved translation, a
         *     message's source that differs from the file's, or a concept that
         *     differs: those are `conflict` results (and a translation of a
         *     message whose source differs is one too — it was made for other
         *     text). `overwrite` makes the stored state the file's (needs
         *     `integration.manage`). `dry_run` runs every check of a merge and
         *     reports its results without changing anything.
         *
         *     **Review states.** A file's states (XLIFF `final` → approved,
         *     `translated` → needs_review, `initial` → draft; PO fuzzy →
         *     needs_review; JSON: `options.state`, default needs_review) are
         *     kept only as far as the requester may decide them: an approval
         *     (or rejection) needs `translations.review` for the locale, or —
         *     approvals — a project that doesn't require review; otherwise
         *     the translation waits for review. Translations are written with
         *     provenance `import` and `origin_detail` `{job, file, format,
         *     requested_by}`.
         *
         *     **Keys.** XLIFF units and JSON keys are message keys. gettext
         *     keys messages by text: a PO entry's key is
         *     `[<msgctxt slug>.]<msgid slug>_<hash>` — the text folded to
         *     lowercase ASCII words joined by `_` (at most 40 characters) and
         *     the first 8 hex digits of SHA-256 over msgctxt, U+0004 and msgid
         *     (`Add to cart` → `add_to_cart_…`), so the same entry always
         *     gets the same key.
         *
         *     **Permissions.** Catalog formats need `integration.import`
         *     (translations of the locales in the requester's scope — a
         *     translator's) or `integration.manage` (also creating and
         *     revising messages; a JSON file in the source locale is a source
         *     catalog and needs it). TMX, TBX and `overwrite` need
         *     `integration.manage` (and `knowledge.write` for TMX and TBX).
         *     What the requester may do is recorded with the job and applied
         *     when it runs. A tenant-wide memory or termbase has its own
         *     routes, `tm-import-jobs` and `termbase-import-jobs`. Problem
         *     codes: `invalid_format`, `invalid_mode`, `invalid_options`,
         *     `project_required` (400), `locale_not_found` (404:
         *     `options.locale` isn't one of the project's locales).
         */
        post: operations["createImportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/import-jobs/{import_job}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        /**
         * An import job with its progress and counts
         * @description `processed_items` of `total_items` (entries, units or concepts
         *     of the file; 0 until known — a TMX file streams) and `summary`,
         *     the results so far by status and kind. A job ends `succeeded`,
         *     `failed` (`failure_code`: `invalid_file`, `unsupported_file`,
         *     `file_too_large` — the problem's line and column are its last
         *     result —, `source_locale_mismatch`, `target_locale_mismatch` —
         *     the file's translations are in a locale the project doesn't
         *     have; import it as one of the project's with `options.locale`
         *     —, `project_not_found`, `upload_expired`, `internal`) or
         *     `cancelled`. An import of a
         *     file this tenant already imported with the same options
         *     succeeds at once with that job's result (`reused_job_id`);
         *     dry runs are always run. Needs `integration.read`.
         */
        get: operations["getImportJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/import-jobs/{import_job}/file": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Upload an import job's file
         * @description The raw file (any `Content-Type`; the job's `format` says how it
         *     is read), streamed to object storage — once, by the job's
         *     requester, while it is `awaiting_upload`. The job is `queued`,
         *     or `succeeded` at once when it reuses an earlier result. Problem
         *     codes: `empty_file`, `upload_interrupted` (400: the body broke
         *     off), `upload_not_expected` (409), `file_too_large` (413; the
         *     limit is the deployment's `GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES`,
         *     64 MiB by default), `storage_unavailable` (503).
         */
        put: operations["uploadImportFile"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/import-jobs/{import_job}/cancellation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel an import job
         * @description A job waiting for its file or queued is cancelled at once; a
         *     running one stops after its current batch of 500
         *     (`cancel_requested`), and what it already applied stays
         *     applied. Cancelling a cancelled job changes nothing. The
         *     requester, or someone with `integration.manage`, may cancel.
         *     Problem code: `job_not_cancellable` (409: it has finished).
         */
        post: operations["cancelImportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/import-jobs/{import_job}/results": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        /**
         * An import job's per-item results
         * @description In file order: each message, then its translations; TM units;
         *     concepts. `status` is `created`, `updated`, `unchanged`,
         *     `conflict` (stored data differs and the mode keeps it: `code`
         *     `approved_translation_conflict`, `source_differs`,
         *     `concept_differs`) or `invalid` (`code` says why:
         *     `forbidden`, `message_not_found`, `invalid_message_key`,
         *     `structural_qa_failed`, `invalid_file`, …). Every result of a
         *     file carries where it is — `line`, `column` and `ref`, the
         *     item in the format's own terms — so each conflict can be found
         *     in the file. A dry run's results are what a merge would do.
         *     Needs `integration.read`.
         */
        get: operations["listImportResults"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/export-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Export jobs
         * @description Newest first. Needs `integration.read`.
         */
        get: operations["listExportJobs"];
        put?: never;
        /**
         * Export a catalog, translation memory or termbase
         * @description Queues the export; download its file when it has `succeeded`.
         *     Catalogs (`xliff`, `json`) belong to a project and write one
         *     file per locale in `options.locales` — several are zipped as
         *     `<locale>.<ext>` —, the project's active messages (in
         *     `options.namespaces`) with their translations in
         *     `options.states` (default `approved`). An XLIFF export without
         *     locales carries the source only; a JSON export defaults to the
         *     source locale and matches what `glossa pull` writes (sorted
         *     keys, two-space indent; `layout` flat or nested; `syntax` mf1 or
         *     mf2). `tmx` and `tbx` export a project's own memory or termbase,
         *     or without `project_id` everything the tenant holds (the
         *     workspace's own routes, `tm-export-jobs` and
         *     `termbase-export-jobs`, say so explicitly); `tmx`
         *     narrows by `source_locale` and target `locales`. Needs
         *     `integration.read` and `catalog.read` with
         *     `translations.read` (catalogs) or `knowledge.read`. Problem
         *     codes: `invalid_format` (400; `po` is import only),
         *     `invalid_options`, `project_required` (400),
         *     `locale_not_found` (404).
         */
        post: operations["createExportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/export-jobs/{export_job}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        /**
         * An export job
         * @description A job ends `succeeded` (with `file` and `download_url`),
         *     `failed` (`failure_code`: `not_representable` — the format can't
         *     express the catalog with these options, such as MF2-only
         *     messages in an MF1 JSON file or an XLIFF file without messages
         *     —, `project_not_found`, `internal`) or `cancelled`. Needs
         *     `integration.read`.
         */
        get: operations["getExportJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/export-jobs/{export_job}/cancellation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel a queued export
         * @description A queued export is cancelled at once; one that is running is
         *     written in one go and can't be. Cancelling a cancelled export
         *     changes nothing. The requester, or someone with
         *     `integration.manage`, may cancel. Problem code:
         *     `job_not_cancellable` (409: it is running or has finished).
         */
        post: operations["cancelExportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/export-jobs/{export_job}/file": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        /**
         * Download a finished export
         * @description The file, streamed from object storage, with its name in
         *     `Content-Disposition` (its media type is the job's
         *     `file.content_type`) and its SHA-256 as the `ETag`. Files are
         *     kept for the deployment's retention period
         *     (`GLOSSA_INTEGRATION_RETENTION`, 7 days by default). Needs
         *     `integration.read`. Problem codes: `export_not_ready` (409),
         *     `file_expired` (410), `storage_unavailable` (503).
         */
        get: operations["downloadExportFile"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-import-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's TMX imports
         * @description Tenant-wide TMX imports (without a project), newest first. Needs `integration.read`.
         */
        get: operations["listTMImportJobs"];
        put?: never;
        /**
         * Import a TMX file into the workspace's translation memory
         * @description Creates a TMX import job for the whole tenant (units without a
         *     project, matched from every project), waiting for its file:
         *     `PUT` it to the job's `upload_url` and follow it like any
         *     import (`import-jobs/{import_job}`). Units whose exact text is
         *     already active tenant-wide are `unchanged`, so a TM import only
         *     ever adds. Needs `integration.manage` and `knowledge.write`
         *     for the whole tenant. Problem codes: `invalid_mode` (400).
         */
        post: operations["createTMImportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/tm-export-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's TMX exports
         * @description Tenant-wide TMX exports (without a project), newest first. Needs `integration.read`.
         */
        get: operations["listTMExportJobs"];
        put?: never;
        /**
         * Export the workspace's translation memory as TMX
         * @description Queues a TMX export of every active unit the tenant holds (its
         *     own and every project's), narrowed by `options.source_locale`
         *     and target `options.locales`; download it from the job's
         *     `download_url` when it has `succeeded`. Needs
         *     `integration.read` and `knowledge.read`. Problem codes:
         *     `invalid_options` (400).
         */
        post: operations["createTMExportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/termbase-import-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's TBX imports
         * @description Tenant-wide TBX imports (without a project), newest first. Needs `integration.read`.
         */
        get: operations["listTermbaseImportJobs"];
        put?: never;
        /**
         * Import a TBX file into the workspace's termbase
         * @description Creates a TBX import job for the whole tenant (concepts without
         *     a project), waiting for its file: `PUT` it to the job's
         *     `upload_url` and follow it like any import. A concept with
         *     other content than the stored one is a `conflict`
         *     (`concept_differs`) unless `overwrite`. Needs
         *     `integration.manage` and `knowledge.write` for the whole
         *     tenant. Problem codes: `invalid_mode` (400).
         */
        post: operations["createTermbaseImportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/termbase-export-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's TBX exports
         * @description Tenant-wide TBX exports (without a project), newest first. Needs `integration.read`.
         */
        get: operations["listTermbaseExportJobs"];
        put?: never;
        /**
         * Export the workspace's termbase as TBX
         * @description Queues a TBX export of every concept the tenant holds (its own
         *     and every project's); TBX takes no options. Download it from
         *     the job's `download_url` when it has `succeeded`. Needs
         *     `integration.read` and `knowledge.read`. Problem codes:
         *     `invalid_options` (400).
         */
        post: operations["createTermbaseExportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/integrations/github/webhooks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Receive a GitHub webhook delivery
         * @description GitHub's own endpoint, not a Studio one. It carries no session:
         *     GitHub signs each delivery, and the signature is what
         *     authenticates it.
         *
         *     The body is read raw, capped at 5 MiB, and its
         *     `X-Hub-Signature-256` is checked with HMAC-SHA256 in constant
         *     time **before anything parses it**. A verified delivery is
         *     written to an inbox keyed by `X-GitHub-Delivery` and answered
         *     `202` at once, well inside GitHub's ten seconds; a worker
         *     processes it. A delivery id already seen inside the seven-day
         *     replay window is a no-op and also answers `202`, so GitHub's
         *     redeliveries are safe.
         *
         *     Handled events: `installation`, `installation_repositories`,
         *     `pull_request` (`opened`, `synchronize`, `reopened`, `closed`)
         *     and `check_run` (`rerequested`). Anything else is acknowledged
         *     and dropped. Nothing in Glossa's correctness depends on a
         *     delivery arriving: a merge lands through the default branch's
         *     push, not through this endpoint.
         *
         *     A delivery whose signature does not verify answers `401` with no
         *     detail. Problem codes: `invalid_webhook` (401),
         *     `webhook_too_large` (413), `github_not_configured` (503).
         */
        post: operations["receiveGitHubWebhook"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/github/install-intents": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Start installing the Glossa GitHub App
         * @description Issues a single-use `state`, bound to this workspace, to the
         *     person asking and to a short expiry, and returns the GitHub URL
         *     to send them to. Only the state's hash is stored, so a reader of
         *     the database cannot replay one.
         *
         *     Send the person to `install_url`; GitHub returns them with the
         *     `state`, an `installation_id`, a `code` and a `setup_action`,
         *     which go to `POST /v1/tenants/{tenant}/github/installations`.
         *
         *     Needs `integration.manage`. Problem codes:
         *     `github_not_configured` (503).
         */
        post: operations["startGitHubInstall"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/github/installations": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's GitHub installations
         * @description Each installation with the repositories the App can see through
         *     it, read from GitHub. An installation GitHub cannot be reached
         *     for still lists, with `repositories_unavailable` set and no
         *     repositories, so one unreachable account does not empty the
         *     page. A `suspended` or `revoked` installation lists without
         *     repositories, because GitHub would refuse the call anyway.
         *
         *     Needs `integration.read`. Problem codes:
         *     `github_not_configured` (503).
         */
        get: operations["listGitHubInstallations"];
        put?: never;
        /**
         * Finish an installation, with the person's ownership verified
         * @description The callback of `install-intents`. The `state` is verified and
         *     burned — it is single-use, so a failed attempt starts over
         *     rather than retrying — and then the `code` is redeemed for the
         *     person's own GitHub token, which is used once to check that they
         *     can actually see the `installation_id` they claim, and dropped.
         *     That is what stops someone claiming an installation of an
         *     account they have nothing to do with.
         *
         *     An installation maps to exactly one workspace. Re-running this
         *     for one this workspace already holds refreshes it; one another
         *     workspace holds is refused with `installation_already_claimed`,
         *     which never says which workspace that is.
         *
         *     Needs `integration.manage`. Problem codes:
         *     `invalid_install_state` (400: unknown, expired, already used, or
         *     `setup_action=request`, which means no installation was made),
         *     `install_state_not_yours` (403),
         *     `installation_not_visible` (403: the person's token cannot see
         *     it), `installation_already_claimed` (409),
         *     `github_unavailable` (503), `github_not_configured` (503).
         */
        post: operations["completeGitHubInstall"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/github/installations/{installation}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A GitHub installation `id` as Glossa issued it, not GitHub's number. */
                installation: components["parameters"]["InstallationPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Forget an installation and its Git connections
         * @description Removes the installation and its Git connections from Glossa. It
         *     does **not** uninstall the App: only GitHub can do that, on the
         *     account's or organization's Applications settings page. Until it
         *     is uninstalled there GitHub keeps sending webhooks, which Glossa
         *     then acknowledges and ignores.
         *
         *     Needs `integration.manage`. Problem codes:
         *     `github_not_configured` (503).
         */
        delete: operations["forgetGitHubInstallation"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/github/connections": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The workspace's Git connections
         * @description Every repository-to-project connection, optionally narrowed to
         *     one installation or one project.
         *
         *     Needs `integration.read`. Problem codes: `invalid_query` (400),
         *     `github_not_configured` (503).
         */
        get: operations["listGitConnections"];
        put?: never;
        /**
         * Connect a repository to a project and application
         * @description Ties one of an installation's repositories — by its numeric
         *     `repository_id`, so a rename or a transfer does not break the
         *     connection — to a project and one of its applications, with the
         *     repository's default branch and an optional monorepo `path`.
         *
         *     One repository can feed several projects, one per `path`; the
         *     same repository and path twice is `connection_exists`. The
         *     repository must be one the installation can actually see and the
         *     application one the project actually has, so a connection that
         *     could never work is refused here rather than failing quietly on
         *     the first pull request. `default_branch` may be left out, and
         *     GitHub's is used.
         *
         *     Needs `integration.manage`. Problem codes: `invalid_connection`
         *     (400), `application_not_found` (404), `repository_not_visible`
         *     (404), `connection_exists` (409), `installation_revoked` (409),
         *     `github_unavailable` (503), `github_not_configured` (503).
         */
        post: operations["createGitConnection"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/github/connections/{connection}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A Git connection `id`. */
                connection: components["parameters"]["ConnectionPath"];
            };
            cookie?: never;
        };
        /**
         * One Git connection
         * @description Needs `integration.read`. Problem codes:
         *     `github_not_configured` (503).
         */
        get: operations["getGitConnection"];
        put?: never;
        post?: never;
        /**
         * Remove a Git connection
         * @description The repository and the installation stay; only the link to this
         *     project goes.
         *
         *     Needs `integration.manage`. Problem codes:
         *     `github_not_configured` (503).
         */
        delete: operations["deleteGitConnection"];
        options?: never;
        head?: never;
        /**
         * Change a connection's project, application, branch or path
         * @description The repository is fixed: pointing a connection at another
         *     repository is a different connection, so create that one and
         *     delete this one.
         *
         *     Needs `integration.manage`. Problem codes: `invalid_connection`
         *     (400), `application_not_found` (404), `connection_exists` (409:
         *     another connection already covers that path),
         *     `github_not_configured` (503).
         */
        patch: operations["updateGitConnection"];
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/context-builds": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's usage uploads (builds), newest first
         * @description With how many usages each holds and how many of those name a key
         *     the catalog didn't know at upload (`unknown_keys`). Retention
         *     keeps the latest 3 builds per application, branch and source plus
         *     every current one, and deletes a closed branch's builds 7 days
         *     after it closed (RFC 0004 §2.3). Needs `catalog.read`. Problem
         *     codes: `unknown_application` (400).
         */
        get: operations["listContextBuilds"];
        put?: never;
        /**
         * Upload a build's usages (glossa context push)
         * @description The body is one `glossa.usages/v1` document: where one
         *     application's messages are used at one commit, as
         *     `@klarlabs-studio/glossa/unplugin` (`.glossa/usages.json`) and `glossa extract`
         *     write it (schema: `runtimes/testdata/schemas/usages.v1.schema.json`).
         *     It is validated by the schema's rules — members it doesn't define
         *     are ignored within v1, anything else it refuses is
         *     `invalid_usages` — and holds at most 100 000 usages and 20 MB.
         *
         *     Keys are resolved to message IDs now, so renaming a message later
         *     keeps its usages; a key the catalog doesn't know is stored and
         *     counted in `unknown_keys`. Whether the build is of the default
         *     branch is the project's `settings.default_branch`, not the
         *     uploader's say. `digest` is the SHA-256 of the document's
         *     RFC 8785 canonical form (without undefined members), and an upload
         *     is idempotent by application, commit, `source` and digest: the
         *     same document again answers `200` with the first upload's build
         *     and `Idempotent-Replayed: true`.
         *
         *     Uploads are rate-limited per tenant (10 a minute, bursts of 60).
         *     Needs `catalog.write` (developers, `write` tokens: CI). Problem
         *     codes: `invalid_usages`, `too_many_usages`, `invalid_source`,
         *     `unknown_application` (400), `payload_too_large` (413),
         *     `rate_limited` (429).
         */
        post: operations["createContextBuild"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/usages": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        /**
         * Where a message appears (its current usages)
         * @description The usages of the message the key names now, in the current
         *     builds: per application and source, the latest build of the
         *     default branch — or, with `branch`, that branch's latest build,
         *     falling back to the default branch's where the branch didn't
         *     rebuild. The default branch's usages come first, then by
         *     application, file and line; `truncated` says more than `limit`
         *     exist. Needs `catalog.read`. Problem codes: `invalid_branch` (400).
         */
        get: operations["listMessageUsages"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/usages": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The current usages on a route, in a component or in a file
         * @description The messages a route, component or file shows (RFC 0004 §8): the
         *     usages in the current builds (see `listMessageUsages`; `branch`
         *     selects a branch view), by build and position. Filters combine;
         *     without one, every current usage. Needs `catalog.read`. Problem
         *     codes: `invalid_branch` (400).
         */
        get: operations["listUsages"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/unused-messages": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Active messages no current build uses
         * @description By key. They are reported, never obsoleted: a dynamic key
         *     (`t(\`plan.${tier}\`)`) is invisible to every collector.
         *     `current_builds` is how many builds were considered — with none,
         *     nothing was uploaded yet and every message is listed — and
         *     `active_messages` and `unused_messages` give the project's context
         *     coverage. `branch` selects a branch view (see `listMessageUsages`).
         *     Needs `catalog.read`. Problem codes: `invalid_branch` (400).
         */
        get: operations["listUnusedMessages"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/captures": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Upload a build's captures (glossa capture --upload)
         * @description A `multipart/form-data` body: first a part named `manifest`
         *     (`application/json`), one `glossa.captures/v1` manifest as
         *     `glossa capture` writes it (schema:
         *     `runtimes/testdata/schemas/captures.v1.schema.json`), then one
         *     part per distinct image, named by the lowercase hex SHA-256 of
         *     its bytes as `captures[].image.sha256` references it, with
         *     `Content-Type: image/png`. Every referenced image has exactly one
         *     part and every part is referenced.
         *
         *     The manifest is validated by the schema's rules like a usages
         *     document (members it doesn't define are ignored within v1) and
         *     the server's: at most 500 captures, one per route, viewport and
         *     locale; at most 10 000 regions each; viewports of at most
         *     10 000 CSS pixels a side; every region `index` names an entry of
         *     its `renders`. Region boxes are stored as the whole CSS pixels
         *     that cover them. Each image must be the PNG its part name and
         *     manifest entry say — at most 10 MB and 40 megapixels — and is
         *     re-encoded without metadata before it is stored; the same pixels
         *     are stored once per project (`images_deduplicated`). The whole
         *     body is at most 200 MB: split a larger capture plan over several
         *     uploads (one per application, or per locale).
         *
         *     A capture may carry the **visual probe pass's findings**
         *     (RFC 0005 §5): what the page measured about itself while it was
         *     still open. They are validated here and stored as ordinary
         *     quality findings, at layer `visual`, in one check run of the
         *     build's branch and commit — not with the capture. A page may
         *     only report what a page can see, at severity `warning` (only the
         *     server can promote a finding, and only when the same fingerprint
         *     appears in two consecutive captures), about a region that really
         *     is on that capture. The server completes the two members the
         *     page cannot know: the **fingerprint**, computed over the catalog
         *     message the key resolved to, and **`locus.capture`**. At most
         *     500 findings per capture and 10 000 per upload
         *     (`too_many_findings`); a plan that finds more is split, as one
         *     over 200 MB is. The project's check policy decides what they are
         *     worth and whether they are computed at all: `visual: off` stores
         *     none. `findings` in the response says how many were stored, and
         *     `listCaptureFindings` reads them back.
         *
         *     Each finding is counted against the capture this application
         *     took last of the same route, viewport and locale, whatever
         *     branch that one came from: a fingerprint that capture showed too
         *     is a **second sighting** and carries `evidence.sightings: 2`,
         *     which is what makes it evidence a policy rule may raise to
         *     `error` (RFC 0005 §5.2). A first sighting is a warning no rule
         *     can raise, because headless Chrome's text metrics move with font
         *     availability and a build must not go red on a font. The count is
         *     the server's: nothing an upload says can influence it.
         *
         *     The upload records a build with `source` `capture`: whether it is
         *     of the default branch is the project's `settings.default_branch`.
         *     Region keys are resolved to message IDs now; the keys the catalog
         *     doesn't know are stored and listed in `unknown_keys`. An upload is
         *     idempotent by application, commit and the SHA-256 of the
         *     manifest's RFC 8785 canonical form (without undefined members):
         *     the same manifest again answers `200` with the first upload's
         *     build and `Idempotent-Replayed: true`, without reading its images.
         *
         *     A tenant keeps at most 2 GB of capture images (the deployment
         *     may set another limit). An upload whose new pixels would pass it
         *     is refused with `storage_quota_exceeded` and stores nothing;
         *     pixels the project already holds are deduplicated and cost
         *     nothing, and retention (RFC 0004 §2.3) frees the quota again as
         *     builds age out.
         *
         *     Uploads share the per-tenant limit of usage uploads (10 a minute,
         *     bursts of 60). Needs `catalog.write` (developers, `write` tokens:
         *     CI). Problem codes: `invalid_request` (not a multipart body),
         *     `invalid_captures` (a malformed body or manifest, or parts that
         *     don't match it), `too_many_captures`, `too_many_regions`,
         *     `too_many_findings`, `invalid_image`, `unknown_application` (400),
         *     `payload_too_large`, `image_too_large`,
         *     `storage_quota_exceeded` (413), `rate_limited` (429),
         *     `storage_unavailable` (503).
         */
        post: operations["createCaptures"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/captures/{capture}/image": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A capture `id`. */
                capture: components["parameters"]["CapturePath"];
            };
            cookie?: never;
        };
        /**
         * A capture's image
         * @description The re-encoded PNG, read through the API (the object store never
         *     hands out a URL). It is content-addressed and never changes:
         *     `ETag` is its SHA-256 and it may be cached privately for a year;
         *     `If-None-Match` with that `ETag` answers `304`. Needs
         *     `catalog.read`. Problem codes: `not_found` (404; also when
         *     retention deleted the image), `storage_unavailable` (503).
         */
        get: operations["getCaptureImage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/captures/{capture}/findings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A capture `id`. */
                capture: components["parameters"]["CapturePath"];
            };
            cookie?: never;
        };
        /**
         * The visual findings on one capture
         * @description What the visual probe pass found on this screenshot
         *     (RFC 0005 §5), in the one finding shape every layer emits, with
         *     `locus.capture` and `locus.region` naming the box on the image:
         *     Studio crops the stored image around the region and outlines it,
         *     and `glossa capture --check` reads the same list.
         *
         *     A capture's findings are read here and not through
         *     `listFindings`, which reads one check run — the run that saw
         *     this screenshot is the one that ingested it, while the project's
         *     newest run is usually a later check of the catalog, which never
         *     saw it.
         *
         *     `evidence.sightings` is how many consecutive captures of this
         *     route, viewport and locale have shown the finding. Below the
         *     two the flake-control rule asks for (RFC 0005 §5.2) it is a
         *     warning no policy rule can raise, however strict the policy.
         *
         *     Waivers are applied on read, as everywhere: a waived finding is
         *     still listed, at severity `waived`, naming the waiver that
         *     accepted it. The order is stable — the run, then the region,
         *     then the row — and the cursor is that order's key, so a page
         *     never shifts. A capture with no findings, and one that was never
         *     probed, both read as an empty list: only a malformed capture ID
         *     is a `404`. Needs `catalog.read`. Problem codes:
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listCaptureFindings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/messages/{message}/captures": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        /**
         * Where a message appears on screen (its current captures)
         * @description The captures in the current builds (see `listMessageUsages`;
         *     `branch` selects a branch view with the default branch's
         *     fallback) that show the message the key names now, each with the
         *     message's regions on it (`visible` false: it rendered zero-size
         *     or off-screen) and the API path of its image. The default
         *     branch's captures come first, then by application, route, locale
         *     and the widest viewport; `truncated` says more than `limit`
         *     exist. Boxes are CSS pixels from the page's top left; the image
         *     is `image.width / viewport.width` times larger (the device scale
         *     factor). Needs `catalog.read`. Problem codes: `invalid_branch`
         *     (400).
         */
        get: operations["listMessageCaptures"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-runs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's check runs, newest first
         * @description One run is one evaluation of one ref against one policy version
         *     (RFC 0005 §2.2): the pull-request check, Studio, the dashboard
         *     and `glossa findings` read the stored run rather than
         *     recomputing the same thing four times. `branch` selects the runs
         *     of a branch or an environment, `commit` the runs of one commit
         *     (a branch moves; the commit a verdict was about does not), and
         *     `conclusion` and `trigger` narrow further; they combine.
         *
         *     `counts` are what the run concluded, with the waivers that were
         *     live while it graded, and nothing rewrites them afterwards — a
         *     verdict is history. What the same findings look like today, with
         *     today's waivers, is `listFindings`. Needs `catalog.read`.
         *     Problem codes: `invalid_query` (400: an unknown conclusion or
         *     trigger), `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listCheckRuns"];
        put?: never;
        /**
         * Record a check run and the findings it produced
         * @description A check that ran somewhere else is recorded here (RFC 0005 §9):
         *     `glossa check` in a product's CI computes the layers against the
         *     project it already read, and posts what it found so that
         *     Studio's quality view, `listFindings`, the summary and the
         *     findings-by-day rollup see it. Without this, a product whose CI
         *     gates on `glossa check` leaves every one of those empty — which
         *     is the state M4 exists to end.
         *
         *     **The findings are `glossa.finding/v1`, minus the members a
         *     reporter cannot know.** Two are deliberately absent, exactly as
         *     they are on a capture upload (`createCaptures`):
         *
         *     - the **`fingerprint`**, which hashes the catalog message ID the
         *       key resolved to. A reporter has the key; the server has the
         *       catalog. A fingerprint minted over a key is not the one the
         *       waiver list, `listFindings` and the pull-request check
         *       compute for the same finding, so every waiver against it
         *       would silently stop applying. The ingest resolves each
         *       `locus.key` to its message ID and computes the fingerprint
         *       itself; a `fingerprint` member sent anyway is not read and
         *       never stored.
         *     - **`locus.capture` and `locus.region`**, which only the server
         *       that minted a capture's ID can pair. A check run is of a
         *       catalog, not of a screenshot; visual findings arrive with
         *       their capture.
         *
         *     **The run does not grade itself.** `severity` is what the layer
         *     emitted, and the project's stored check policy decides what that
         *     is worth here — in this locale, this namespace, this
         *     `environment` — through the same evaluator every other stored
         *     finding goes through. A finding a rule switches off is not
         *     stored, because `off` means the project does not compute it; an
         *     advisory layer is clamped back to `warning` however strict the
         *     rule. `severity: waived` is refused (`invalid_finding`): whether
         *     a finding is waived is decided from the project's live waivers,
         *     here and again on read. There is no `conclusion`, no `counts`
         *     and no `policy_version` in the request: a caller that could
         *     assert those could declare its own build green.
         *
         *     At most 10 000 findings in one run (`too_many_findings`, RFC
         *     0005 §10); a run with more is **refused, not truncated** — a
         *     silently shortened run is a report that lies about what was
         *     checked. `layers` is what actually ran, so a reader can tell
         *     "clean" from "not looked at", and a run that names none is a
         *     run that looked at nothing.
         *
         *     `trigger` says who asked: `cli` (`glossa check`),
         *     `pull_request` (the check on a pull request) or `api` (the
         *     default — anything else, including MCP). `capture` and `write`
         *     are the server's own jobs and cannot be claimed.
         *
         *     Needs `catalog.write` — the permission a CI token already holds
         *     beside `catalog.read` (RFC 0004 §6.3), and the one that already
         *     carries the authority to change what a check concludes, because
         *     it uploads the messages, the usages and the captures the layers
         *     grade. Problem codes: `too_many_findings`, `invalid_finding`,
         *     `invalid_request` (400).
         */
        post: operations["createCheckRun"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-runs/{check_run}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A check run `id`. */
                check_run: components["parameters"]["CheckRunPath"];
            };
            cookie?: never;
        };
        /**
         * One check run with its counts
         * @description The run, the layers it actually computed — a layer the policy
         *     switched off is not in the list, so a reader can tell "clean"
         *     from "not looked at" — and the counts it concluded. `waived` is
         *     counted on its own and is never part of `errors` or `warnings`,
         *     because a number that goes down without the product getting
         *     better is the failure mode of every suppression system
         *     (RFC 0005 §14 decision 5). Its findings are
         *     `listFindings?run=…`. Needs `catalog.read`.
         */
        get: operations["getCheckRun"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/findings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A check run's findings, with today's waivers applied
         * @description Every finding has one shape, whatever layer found it
         *     (`glossa.finding/v1`, RFC 0005 §2.1): a layer says what is wrong
         *     with which message in which locale, and Context fills the
         *     locus's file, line, route, component, capture and region in at
         *     report time, which is what turns almost every finding into a
         *     pull-request annotation instead of only the unknown keys.
         *
         *     A finding belongs to a run, so the list reads exactly one: the
         *     one `run` names, or the newest run of `branch` or `commit`, or
         *     the project's newest. Listing every run's copy of the same
         *     problem would report it once per run and say nothing more.
         *     `run` is the run the page came from — absent when nothing has
         *     been checked yet, and the page is then empty rather than a
         *     `404`.
         *
         *     Waivers are applied on read (RFC 0005 §2.3). A waived finding is
         *     **still computed and still reported**, at severity `waived`,
         *     naming the waiver that accepted it, and counted on its own; it
         *     is never hidden and never deleted. It comes back as an ordinary
         *     finding once the source revision it was computed against has
         *     moved past the one its waiver was made against — the German
         *     somebody waived is not the German that now ships. `counts` are
         *     the whole run as it stands now, whatever the filters select, so
         *     waiving something changes them while the run's own stored
         *     verdict stays what it was.
         *
         *     Findings are immutable: a run is one evaluation, and the next
         *     writes new rows rather than editing the last one's. There is no
         *     update.
         *
         *     The order is stable — errors, then warnings, then the waived,
         *     and within each by layer, locale, message key and id — and the
         *     cursor is that order's key, so a page never shifts. MCP's read
         *     tools consume this shape (RFC 0005 §7.3): it is the contract,
         *     not an implementation detail. Needs `catalog.read`. Problem
         *     codes: `invalid_query` (400: an unknown layer or severity),
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listFindings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/waivers": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The findings this project accepts, and why
         * @description Newest first, each with what it accepts: the layer, code, locale
         *     and message of the most recent stored finding carrying its
         *     fingerprint. Those are empty where no stored finding carries it
         *     any more — an unexamined waiver, which is exactly what a
         *     dashboard should show. `layer`, `code` and `message` filter on
         *     them; `active` selects the waivers that stand now (not revoked,
         *     not expired) or only those that don't. Needs `catalog.read`.
         *     Problem codes: `invalid_query` (400: an unknown layer),
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listWaivers"];
        put?: never;
        /**
         * Accept a finding, with a reason
         * @description Some findings are correct and still fine: "Login" is the German
         *     term, and the Japanese button *is* two lines by design
         *     (RFC 0005 §2.3). A waiver names the finding by its
         *     `fingerprint` — not by a finding id, so it survives the run that
         *     found it — and says why.
         *
         *     **The reason is required and non-empty.** A blank or
         *     whitespace-only one is refused with `waiver_reason_required`;
         *     there is no way to waive a finding without saying why, because a
         *     suppression nobody had to justify is technical debt with no
         *     paper trail. There is deliberately no inline code suppression
         *     (`// glossa:ignore …`): most findings are about a *translation*,
         *     which has no line of code to put a comment on.
         *
         *     A waiver never deletes anything. The finding it accepts is still
         *     computed, still listed and counted on its own, at severity
         *     `waived` — and it comes back on its own once
         *     `source_revision` moves. `source_revision` defaults to the one
         *     the most recent stored finding with that fingerprint was
         *     computed against, which is the finding the person is looking at.
         *
         *     `scope` `project` accepts the finding everywhere in the project;
         *     `branch` accepts it on `ref` only. The project keeps one live
         *     waiver per fingerprint and reach, so waiving the same finding
         *     again is not a second waiver: it restates the reason, the
         *     expiry and the source revision, and answers `200` instead of
         *     `201`.
         *
         *     Needs `catalog.write` — the permission that already carries the
         *     authority to change what a project's check concludes, since it
         *     uploads the messages, usages and captures the layers grade.
         *     `translations.review` would be the wrong one: it is
         *     locale-scoped, and the `structure`, `completeness` and `source`
         *     layers produce findings with no locale at all. Problem codes:
         *     `waiver_reason_required`, `invalid_fingerprint`,
         *     `invalid_waiver_scope`, `waiver_branch_required`,
         *     `waiver_expiry_in_the_past`, `invalid_waiver` (400).
         */
        post: operations["createWaiver"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/waivers/{waiver}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A waiver `id`. */
                waiver: components["parameters"]["WaiverPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Take a waiver back
         * @description The findings it accepted are ordinary findings again from the
         *     next read; nothing is deleted and the revoked waiver stays as
         *     history, with who made it, when, and why. Revoking one twice
         *     answers `204` all the same. Needs `catalog.write`.
         */
        delete: operations["revokeWaiver"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-policy": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The project's check policy
         * @description What `glossa check` and the Glossa pull-request check decide by
         *     (RFC 0005 §4). The server is the source of truth: `glossa check`
         *     fetches this document and caches it at `.glossa/policy.json`,
         *     and `glossa.yaml`'s `check:` block and the flags are local
         *     overrides the pull-request check ignores. A developer can
         *     tighten or loosen their own loop; they cannot change what CI
         *     decides.
         *
         *     A project that has never saved one reads as the documented
         *     default — every locale must be complete, an untranslated key in
         *     one is an error, errors fail — at `version` `0`. `created_by`
         *     and `created_at` are absent for a policy nobody saved through
         *     this endpoint. Needs `catalog.read`, because a policy is about
         *     the catalog's messages and the callers that must read it — the
         *     CLI, the pull-request check, Studio and MCP — hold exactly that.
         */
        get: operations["getCheckPolicy"];
        put?: never;
        /**
         * Save a new version of the check policy
         * @description A policy is never edited in place: every save is a new version,
         *     one past the last, and every check run records the version it
         *     graded itself against (RFC 0005 §4.3). `version`,
         *     `effective_from` and `grace_until` are the server's answers to
         *     "when did this become true" and are refused in a request
         *     (`invalid_check_policy`): a caller that could set them could
         *     rewrite history or unpin an open pull request mid-grace.
         *
         *     **`dry_run` answers the impact preview and stores nothing.** The
         *     failure mode this exists for is obvious and avoidable: somebody
         *     adds `terminology: error`, and forty open pull requests go red
         *     for something their authors didn't do. The preview says how many
         *     stored findings change severity, which refs would newly fail and
         *     how many open pull requests are among them, per rule. Studio
         *     shows it before the save; `glossa policy diff` prints it. The
         *     same preview is computed for a real save, so the audit trail
         *     carries the number the author saw.
         *
         *     **`grace_days` pins the pull requests that predate the save** to
         *     the version they were opened under, until the grace ends; a new
         *     pull request grades against the new version at once, and merging
         *     is never blocked by a rule the branch predates. Absent, it is
         *     14 days, because the default should protect the people who did
         *     not cause the change; `0` pins nothing, which is what a policy
         *     that only loosens wants.
         *
         *     Needs `catalog.write` — the permission that already carries the
         *     authority to change what a project's check concludes. Problem
         *     codes: `invalid_check_policy` (400: an unknown layer or
         *     severity, a rule raising the model-decided `linguistic` layer to
         *     `error`, a locale the project doesn't have, or a request that
         *     set the server's bookkeeping), `check_policy_conflict` (409: the
         *     project changed while the write was being prepared — read the
         *     policy again and decide).
         */
        post: operations["saveCheckPolicy"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-policy/versions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * How the check policy got to be what it is
         * @description Every saved version, newest first, with the document it was and
         *     who saved it when. A policy is an organizational decision about
         *     a project (RFC 0005 §4.2), so "who tightened `terminology` to
         *     `error`, and when?" has an answer that outlives the version that
         *     carried it.
         *
         *     The list starts at the project's first save through this API: a
         *     policy set through the project's settings, or never set at all,
         *     has no versions. Needs `catalog.read`. Problem codes:
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listCheckPolicyVersions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-policy/versions/{version}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A check-policy version number, as `listCheckPolicyVersions` gives it. */
                version: components["parameters"]["CheckPolicyVersionPath"];
            };
            cookie?: never;
        };
        /**
         * One version of the check policy
         * @description The document as it was saved, with who saved it and when. It is
         *     what a check run that records `policy_version` was graded
         *     against, so a verdict from three weeks ago can be read against
         *     the policy that produced it rather than the policy that happens
         *     to be live now. Needs `catalog.read`.
         */
        get: operations["getCheckPolicyVersion"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-policy/export": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The check policy in its canonical form
         * @description The document alone, with none of the state around it — exactly
         *     what `importCheckPolicy` takes. A project can't keep its policy
         *     in Git (RFC 0005 §4.2: one commit must have one verdict, and a
         *     policy change must not be a merge conflict), so export and
         *     import are what keep it reviewable: `glossa policy export`
         *     writes this file, a person reads the diff, `glossa policy
         *     import` sends it back.
         *
         *     The server's bookkeeping is left out, because importing it
         *     elsewhere would be asserting another project's history. Needs
         *     `catalog.read`.
         */
        get: operations["exportCheckPolicy"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/check-policy/import": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Import a check policy as a new version
         * @description Takes a canonical document — what `exportCheckPolicy` returns,
         *     from this project or another — validates it and saves it as the
         *     next version, with the same grace and the same impact preview as
         *     `saveCheckPolicy`. It is the same write with the document as the
         *     whole body, so a file can be posted as it is.
         *
         *     `grace_days` and `dry_run` ride in the query, for the same
         *     reason: the body is the file. Needs `catalog.write`. Problem
         *     codes: `invalid_check_policy` (400), `check_policy_conflict`
         *     (409).
         */
        post: operations["importCheckPolicy"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/quality-summary": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * The seven numbers of a project's localization health
         * @description Seven numbers and no more (RFC 0005 §8), because a dashboard
         *     nobody reads is worse than a check that fails: coverage;
         *     outstanding findings by layer and severity, plus waived; AI
         *     acceptance rate and mean edit distance; the review queue's depth
         *     and age; context coverage — the share of active messages with a
         *     usage and with a visible region; lead time from a source change
         *     to a published translation; and the pull-request check's pass
         *     rate and time to a conclusion. Plus the one trend M4 keeps,
         *     findings by layer per day.
         *
         *     Every number is computed from the owning context's own tables on
         *     **this** request and cached for 60 seconds (`computed_at`,
         *     `expires_at`). There is no time-series store behind it: a
         *     summary is a measurement of at most a minute ago, not a history.
         *
         *     The document is pivoted the way it is read: one `project` health
         *     row and one `locales[]` row per locale, each carrying the same
         *     numbers, rather than seven lists a client would have to join by
         *     locale code. A locale row also says which layers are
         *     **available** for it, so an unsupported layer never reads as a
         *     green one (intent §41).
         *
         *     **A number that could not be computed is absent, never zero**,
         *     and `unmeasured` names every one that is missing with the
         *     reason — a source this deployment does not run, a permission the
         *     caller does not hold, a project nothing has ever checked, an
         *     environment that has published nothing in the window. `findings`
         *     absent means nothing was ever checked; `findings.errors: 0`
         *     means a run looked and found none. Inside a number the same rule
         *     holds: a review queue of depth `0` is measured and has no `age`,
         *     a window with no concluded check has no `pass_rate`, and a layer
         *     the newest run did not compute carries no counts.
         *
         *     `locale` narrows the rows to one locale; a locale no number
         *     mentioned still gets its row, because "this locale has nothing"
         *     is an answer. `checks` is not per locale — a pull request is
         *     about a commit, not a language — and neither is `context`: a
         *     usage is a place in the product's code and a region a box on a
         *     screenshot.
         *
         *     The caller needs `catalog.read`. Each source then checks its own
         *     permission (`translations.read`, `intelligence.read`,
         *     `releases.read`, `integration.read`), and one the caller does
         *     not hold costs that number and not the page: a translator
         *     without `intelligence.read` still sees coverage and findings,
         *     and is told which numbers were not theirs to see. Problem codes:
         *     `invalid_query` (400: an unknown locale or environment name).
         */
        get: operations["getQualitySummary"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/linguistic-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's linguistic-QA jobs, newest first
         * @description `state` narrows to `queued`, `running`, `succeeded`, `failed` or
         *     `cancelled`. A list does not follow a running job — one page
         *     should not fan out into a poll per row — so a job's state here
         *     is as of its last read; `getLinguisticJob` brings one up to
         *     date. Needs `catalog.read`. Problem codes: `invalid_query`
         *     (400), `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listLinguisticJobs"];
        put?: never;
        /**
         * Review translations with a model (the linguistic layer)
         * @description The `linguistic` layer is the one layer that needs a model, and
         *     that makes it **a job**, triggered explicitly or on a batch, and
         *     never a check: `glossa check` never calls an AI provider
         *     (RFC 0005 §14 decision 2), which is what keeps the check free,
         *     offline-capable and deterministic. A check *reports* the
         *     linguistic findings a job stored; it never computes one.
         *
         *     **Its findings are advisory.** They are `warning`, and a check
         *     policy may not raise them to `error` — a rule that names the
         *     layer at `error` is refused when the policy is saved
         *     (`invalid_check_policy`), and a wildcard rule that raises
         *     everything without naming it is clamped back to `warning` when
         *     the finding is graded. A model's opinion never fails a build; a
         *     real mistranslation gates through a human in the review queue,
         *     which is where it belongs. Codes: `meaning-divergence`,
         *     `tone-mismatch`, `grammar-suspected`, `inconsistent-phrasing`.
         *
         *     The job inherits M2's rules whole (RFC 0003 §3.1, §7): the
         *     provider port and routing policy, the tenant's **sending
         *     consent**, the **`sensitive` namespace rule** — a namespace
         *     tagged `sensitive` is never sent to a provider, and the messages
         *     under one are counted in `skipped_sensitive` rather than
         *     silently dropped — and the existing per-tenant **AI budget**.
         *     A job refused by one of those is created and answered as
         *     `failed`, with `failure_code` saying which: `provider_consent`,
         *     `budget_exceeded`, `sensitive`, `no_route`. That is how
         *     Intelligence's own jobs report the same four refusals, and it
         *     keeps the refusal on the record instead of in a 4xx nobody
         *     kept.
         *
         *     Its findings are recorded in one check run of `ref`, readable
         *     through `listFindings?run=…` once `check_run` is set. They are
         *     tenant data and never leave the control plane for the edge.
         *
         *     Needs `catalog.write`: the job records a check run and spends
         *     the tenant's AI budget. Problem codes:
         *     `linguistic_unavailable` (503: the deployment wires no
         *     reviewer), `linguistic_layer_off` (409: the project's policy
         *     switches the layer off, so it does not compute it and does not
         *     pay for it), `invalid_linguistic_scope` (400).
         */
        post: operations["createLinguisticJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/linguistic-jobs/{linguistic_job}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A linguistic-QA job `id`. */
                linguistic_job: components["parameters"]["LinguisticJobPath"];
            };
            cookie?: never;
        };
        /**
         * One linguistic-QA job, brought up to date
         * @description A job that has been handed over is followed on read: a review
         *     that has finished has its findings recorded, once, and
         *     `check_run` then names the run they are in. Recording is
         *     idempotent — a job that already names a run never records a
         *     second — so polling a finished job cannot double a project's
         *     findings. Needs `catalog.read`.
         */
        get: operations["getLinguisticJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/linguistic-jobs/{linguistic_job}/cancellation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A linguistic-QA job `id`. */
                linguistic_job: components["parameters"]["LinguisticJobPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Stop a linguistic-QA job that has not finished
         * @description Only a job that is still `queued` or `running` can be stopped.
         *     Cancelling is not a way to undo findings: a finding a job
         *     already recorded stays, and is waived rather than deleted
         *     (RFC 0005 §14 decision 5). Needs `catalog.write`. Problem code:
         *     `linguistic_job_not_cancellable` (409).
         */
        post: operations["cancelLinguisticJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Branches of a project, by name
         * @description `state` lists only `open`, `merged` or `closed` branches. `name`
         *     is the way to find a branch whose `id` you don't have (an
         *     unknown name is an empty page, not a 404). Needs `catalog.read`.
         *     Problem codes: `invalid_branch_state` (400).
         */
        get: operations["listBranches"];
        put?: never;
        /**
         * Open a branch, or record what CI knows about it
         * @description Addressed by `name`, because a branch that doesn't exist yet has
         *     no `id`: the first call creates it (`201`), later ones record its
         *     head commit, its pull request number and its preview URL
         *     (`200`). It proposes nothing — `pushBranchMessages` does that —
         *     and a closed branch reopens. Opening a branch opens its preview
         *     environment (`pr-<number>`, or `br-<hash>` without a pull
         *     request). Needs `catalog.write`. Problem codes: `invalid_branch`,
         *     `invalid_push`, `invalid_preview_url` (400), `branch_merged`
         *     (409).
         */
        post: operations["upsertBranch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branch-pushes": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Push a branch's messages (CI, glossa push --branch)
         * @description The branch's catalog, up to 10000 messages in one transaction —
         *     a whole catalog, not a batch, so `complete` can be trusted. The
         *     branch is created by its first push, and a closed one reopens.
         *
         *     Nothing live changes: a key the project doesn't have becomes a
         *     `proposed` message the branch owns, changed source for a live
         *     key becomes a *source proposal*, and a key whose live source
         *     already says this is `unchanged`. Two open branches proposing
         *     the same new key with different source are a `key_conflict` for
         *     both. With `complete`, the keys the branch proposed before and
         *     no longer has are withdrawn, and the project's live keys the
         *     push lacks are reported in `removed` — reported only: a branch
         *     never obsoletes anything.
         *
         *     The answer is the branch's status report, the same one
         *     `getBranch` returns, plus one result per item in request order.
         *     Items fail on their own (`items[].error.code`, as for
         *     `upsertMessages`) without failing the push. Needs
         *     `catalog.write`. Problem codes: `too_many_branch_items`,
         *     `invalid_branch`, `invalid_push` (400), `branch_merged` (409).
         */
        post: operations["pushBranchMessages"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches/{branch}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        /**
         * A branch's status report
         * @description What the branch proposes as its last push left it: its new keys,
         *     its source proposals, the live keys its last complete push
         *     lacked (`removed`), the key conflicts it shares with other open
         *     branches, and how many current translations per locale merging
         *     it will make outdated. This is what the PR check reports. Needs
         *     `catalog.read` (`outdated` also needs `translations.read`).
         */
        get: operations["getBranch"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches/{branch}/proposals": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        /**
         * What a branch proposes, key by key
         * @description By key: the source the branch pushed for it, the message it
         *     names, and for a source change the revision it was proposed
         *     against. Needs `catalog.read`.
         */
        get: operations["listBranchProposals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches/{branch}/closure": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Close a branch (its pull request was closed unmerged)
         * @description Its preview environment is destroyed, so the edge answers 404
         *     for it, and its proposed messages stay proposed for 14 days
         *     before they become obsolete — reopening the branch, or pushing
         *     their keys again, brings them back with their translations and
         *     history. Idempotent: closing a closed branch changes nothing.
         *     Needs `catalog.write`. Problem codes: `branch_merged` (409).
         */
        post: operations["closeBranch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches/{branch}/merge": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Mark a branch merged
         * @description Bookkeeping and cleanup, not activation: the **default branch's
         *     push** makes proposed messages active and proposals source
         *     revisions, so nothing depends on this call arriving. It destroys
         *     the branch's preview environment and starts the 14-day clock on
         *     whatever the default branch didn't bring in. A merged branch
         *     takes no more pushes. Idempotent. Needs `catalog.write`.
         */
        post: operations["mergeBranch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/branches/{branch}/preview": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Record where CI deployed the branch's preview
         * @description `glossa preview register --url`. The URL is shown in Studio and
         *     in the pull request comment; it is where the in-product editor
         *     runs. An empty `url` clears it. Needs `catalog.write`. Problem
         *     codes: `invalid_preview_url` (400).
         */
        put: operations["setBranchPreview"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/workflow-definitions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Workflow definitions
         * @description The tenant's live definitions, by name, each at its latest
         *     version. With `project`, the ones that project may bind: the
         *     tenant's and its own. Deleted definitions are not listed; their
         *     versions stay readable. Needs `workflows.read`. Problem codes:
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listWorkflowDefinitions"];
        put?: never;
        /**
         * Create a workflow definition
         * @description The body is the `glossa.workflow/v1` document itself (RFC 0006
         *     §2.3) — what `glossa workflow push` sends and the workflow
         *     editor saves. It is compiled and linted before anything is
         *     stored: an unknown guard or action, an unreachable state, a
         *     non-final dead end, non-determinism or a delayed transition is
         *     `invalid_workflow` (422) with every finding, and nothing is
         *     saved. A saved document becomes version 1; the response carries
         *     lint's informational notes. With `project` the definition is
         *     that project's alone; without, every project of the tenant may
         *     bind it.
         *
         *     Needs `workflows.manage`. Problem codes: `invalid_workflow`
         *     (422), `workflow_definition_exists` (409: a live definition with
         *     this name in this scope), `workflow_limit_reached` (409: the
         *     tenant's 50 live definitions, RFC 0006 §9.6).
         */
        post: operations["createWorkflowDefinition"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/workflow-definitions/{workflow_definition}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        /**
         * A workflow definition
         * @description Its identity and latest version; the `ETag` is that version, for
         *     `If-Match` on the next save. A deleted definition is still
         *     readable, with `deleted_at`. Needs `workflows.read`.
         */
        get: operations["getWorkflowDefinition"];
        put?: never;
        post?: never;
        /**
         * Delete a workflow definition
         * @description Its bindings go with it, so it is never selected again; its
         *     versions stay, so whatever ran on them stays explainable.
         *     Deleting the default `review` definition is allowed and means "no
         *     workflow" — M4's behaviour (RFC 0006 §2.3). Needs
         *     `workflows.manage`.
         */
        delete: operations["deleteWorkflowDefinition"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/workflow-definitions/{workflow_definition}/versions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        /**
         * A definition's versions, newest first
         * @description Every version with its document, also after the definition was
         *     deleted. Versions are immutable. Needs `workflows.read`. Problem
         *     codes: `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listWorkflowDefinitionVersions"];
        put?: never;
        /**
         * Save the next version of a definition
         * @description The body is the whole `glossa.workflow/v1` document, compiled and
         *     linted exactly as on create. `If-Match` is the definition's
         *     `ETag` — the version the author edited — so a save that another
         *     save overtook is refused (412) instead of silently replacing
         *     their change. Running instances stay on the version they started
         *     with. A version keeps its definition's `name` and `subject`: a new
         *     name is a new definition.
         *
         *     Needs `workflows.manage`. Problem codes: `invalid_workflow`
         *     (422, also for a renamed definition or a changed subject),
         *     `precondition_failed` (412), `precondition_required` (428).
         */
        post: operations["saveWorkflowDefinitionVersion"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/workflow-definitions/{workflow_definition}/versions/{version}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
                /** @description A workflow definition's version number. */
                version: components["parameters"]["WorkflowVersionPath"];
            };
            cookie?: never;
        };
        /**
         * One version of a definition
         * @description The document as it was saved, with who saved it and when — what
         *     an instance that names this version ran on. Needs
         *     `workflows.read`.
         */
        get: operations["getWorkflowDefinitionVersion"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/workflow-definition-lints": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Lint a workflow document without saving it
         * @description What `glossa workflow lint` and the editor ask before a save: the
         *     same compile and lint a save runs, and every finding, with
         *     `valid` saying whether a save would be accepted. Stores nothing,
         *     so an invalid document is a `200` with `valid: false`, not a
         *     `422`. Needs `workflows.read`.
         */
        post: operations["lintWorkflowDefinition"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-bindings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's workflow bindings
         * @description In creation order, which is what "later" means when two bindings
         *     are equally specific. A project with none has no workflow: no
         *     instance is created, and it behaves as it did before M5. Needs
         *     `workflows.read`. Problem codes: `invalid_page_size`,
         *     `invalid_page_token` (400).
         */
        get: operations["listWorkflowBindings"];
        put?: never;
        /**
         * Bind a definition to the project
         * @description Optionally narrowed to some locales and one namespace. Bindings
         *     resolve with the check policy's precedence rule (RFC 0005 §4.1):
         *     the one naming more fields wins, a tie goes to the later one.
         *     Changing a binding is deleting it and binding again. Needs
         *     `workflows.manage`. Problem codes: `workflow_binding_exists`
         *     (409: a binding with this selector), `invalid_workflow_binding`
         *     (422: a locale that is not BCP 47, or a namespace that cannot be
         *     one), `workflow_definition_out_of_scope` (422: another project's
         *     definition), `not_found` (404: the definition, or the project).
         */
        post: operations["createWorkflowBinding"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-bindings/{workflow_binding}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow binding `id`. */
                workflow_binding: components["parameters"]["WorkflowBindingPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Remove a binding
         * @description New instances resolve without it from now on; running instances
         *     stay on the version they started with. Needs `workflows.manage`.
         */
        delete: operations["deleteWorkflowBinding"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-resolution": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * Which definition applies to a subject
         * @description The binding a new instance for this subject would be created
         *     under, and the definition version it would start on (the bound
         *     definition's latest). `bound: false` means no binding applies:
         *     no instance, M4's behaviour. Needs `workflows.read`. Problem
         *     codes: `invalid_query` (400: a locale that is not BCP 47).
         */
        get: operations["resolveWorkflow"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-instances": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        /**
         * A project's workflow instances
         * @description An instance exists while work is in flight under a binding
         *     (RFC 0006 §2.5): created by the first trigger, `finished` when it
         *     reaches a final state, its transition log kept. Filters:
         *     `definition`, `status`, `locale`, and the subject — a `message`
         *     by key, or any subject by `subject_id` (not both). Needs
         *     `workflows.read`. Problem codes: `invalid_query`,
         *     `invalid_page_size`, `invalid_page_token` (400),
         *     `workflow_instances_unavailable` (503: this server runs no
         *     instance store).
         */
        get: operations["listWorkflowInstances"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        /**
         * A workflow instance
         * @description Its definition version, subject, state and status. The `ETag` is
         *     the definition version the instance runs on: what a rebase's
         *     `If-Match` names. Needs `workflows.read`. Problem codes:
         *     `workflow_instances_unavailable` (503).
         */
        get: operations["getWorkflowInstance"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}/rebase": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Move a running instance to a newer version of its definition
         * @description A running instance stays on the version it started with until a
         *     workflow manager moves it (RFC 0006 §2.3). The rebase keeps the
         *     instance's state by name: the target version — `version`, or the
         *     definition's latest when the body names none — must be newer, and
         *     must have that state as one an instance can wait in. A rebase
         *     changes what happens next, never what already happened: it runs
         *     no entry action again (the assignments and approvals the state
         *     asked for stand), and a pending due date keeps its time. An
         *     instance that has not started yet starts on the new version at
         *     its next event.
         *
         *     The rebase is recorded in the instance's transition log as event
         *     `rebase` (its one action says which versions), and published as
         *     `workflow.instance.rebased` naming who did it. `If-Match` is the
         *     instance's `ETag`: the version it runs on.
         *
         *     Needs `workflows.manage` in the project. Problem codes:
         *     `workflow_instance_finished` (409: nothing left to run),
         *     `invalid_workflow_rebase` (422: the version is not newer, or the
         *     definition has no such version), `workflow_rebase_state_missing`
         *     (422: the target has no such state), `workflow_rebase_state_final`
         *     (422: the state is final in the target), `precondition_failed`
         *     (412), `precondition_required` (428),
         *     `workflow_instances_unavailable` (503).
         */
        post: operations["rebaseWorkflowInstance"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}/transitions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        /**
         * An instance's transition log
         * @description Every event the instance received, oldest first: the state it
         *     moved from and to, the guards it evaluated, the actions it ran
         *     and what became of them, and the actor whose event caused it —
         *     actions run as that actor, never as Workflow's own principal
         *     (RFC 0006 §2.5). An event the instance no longer accepted is
         *     `ignored`; one whose action was refused for permission is
         *     `refused` and left the instance where it was. Needs
         *     `workflows.read`. Problem codes: `invalid_page_size`,
         *     `invalid_page_token` (400), `workflow_instances_unavailable`
         *     (503).
         */
        get: operations["listWorkflowTransitions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignments": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Assignments, or my work
         * @description What the caller may see. With `assignments.manage`, every
         *     assignment in their project scope — or, with `mine=true`, only
         *     their own. Without it, only their own: given to them directly,
         *     to a role they hold, to a group they are in or to their vendor.
         *     That is "my work", and it is all a vendor's member (visibility
         *     `assigned`) ever sees here. Filters: `project`; a unit by
         *     `message` key (with `project`) and/or `locale`; `state`.
         *     Needs `assignments.read` (`assignments.manage` for others'
         *     work). Problem codes: `invalid_query`, `invalid_page_size`,
         *     `invalid_page_token` (400).
         */
        get: operations["listAssignments"];
        put?: never;
        /**
         * Give translation units to someone
         * @description One assignment for a batch of a project's translation units —
         *     the job a translator works through, not one string at a time.
         *     The assignee is exactly one member, role, group or vendor
         *     (`assignee`); a vendor's members see these units, and only
         *     these, while it is open and for 30 days after it is done (RFC
         *     0006 §3.3). Units are named by message key and locale. Needs
         *     `assignments.manage` in the project. Problem codes:
         *     `invalid_assignment` (422: no units, too many, a key the
         *     project does not have, a due date in the past),
         *     `unknown_party` (422: no such member, group or vendor),
         *     `workflow_limit_reached` (409: the assignee already holds 1,000
         *     open assignments, RFC 0006 §9.6), `idempotency_key_reused`
         *     (422), `invalid_idempotency_key` (400), `not_found` (404: the
         *     project).
         */
        post: operations["createAssignment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignment-reports": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Vendors' quality report
         * @description The quality numbers of completed assignments, by assignee and
         *     locale (RFC 0006 §3.4), computed on read from the assignments,
         *     the catalog, the translations' revision logs and the findings,
         *     as the caller: it says nothing they could not read unit by unit.
         *     At most 2000 assignments and 5000 units are read; `truncated`
         *     says a bound stopped it. Filters: `project`, `vendor` (a vendor
         *     `id`) and `since` (assignments completed at or after it).
         *     Needs `assignments.read` in the project scope; a member whose
         *     visibility is `assigned` is refused. Problem codes:
         *     `invalid_query` (400), `workflow_instances_unavailable` (503, a
         *     server built without the contexts a report reads).
         */
        get: operations["getAssignmentReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignments/{assignment}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        /**
         * An assignment
         * @description With `assignments.manage`, any in the caller's project scope;
         *     otherwise only one given to the caller, with
         *     `assignments.read` — someone else's is not found.
         */
        get: operations["getAssignment"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignments/{assignment}/acceptance": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Take an open assignment on
         * @description Only its assignee, holding the assignment's permission for every
         *     unit. Problem codes: `forbidden` (403: not given to you, or
         *     missing the permission), `assignment_state` (409: not `open`).
         */
        post: operations["acceptAssignment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignments/{assignment}/completion": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Claim an assignment done
         * @description A claim, not a decision: it raises `assignment.completed` and the
         *     project's workflow decides what follows; no review state changes
         *     here (RFC 0006 §3.1). Only its assignee, holding the assignment's
         *     permission for every unit. Problem codes: `forbidden` (403),
         *     `assignment_state` (409: not live).
         */
        post: operations["completeAssignment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/assignments/{assignment}/decline": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Hand an assignment back
         * @description By its assignee, or by someone with `assignments.manage` taking
         *     it back; raises `assignment.declined`. Problem codes: `forbidden`
         *     (403), `assignment_state` (409: not live), `invalid_assignment`
         *     (422: a reason over 2,000 characters).
         */
        post: operations["declineAssignment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/approvals": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Approvals, with their decisions
         * @description The approvals inbox. Filters: `project`; a translation unit by
         *     `message` key (with `project`) and/or `locale`; `subject`;
         *     `state`. Needs `workflows.read`, in the caller's project scope.
         *     Problem codes: `invalid_query`, `invalid_page_size`,
         *     `invalid_page_token` (400).
         */
        get: operations["listApprovals"];
        put?: never;
        /**
         * Ask for a translation unit to be approved
         * @description Asks `n` distinct people of `from` (a member, a role or a group —
         *     never a vendor) to grant it. Four-eyes always applies: the author
         *     of the unit's latest text cannot count. A newer request for the
         *     same unit replaces the pending one. Needs `assignments.manage`
         *     in the project. Problem codes: `invalid_approval` (422: `n` out
         *     of range, a vendor asked, a key the project does not have),
         *     `unknown_party` (422), `not_found` (404: the project).
         */
        post: operations["createApproval"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/approvals/{approval}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An approval `id`. */
                approval: components["parameters"]["ApprovalPath"];
            };
            cookie?: never;
        };
        /**
         * An approval, with its decisions
         * @description Needs `workflows.read`, in the caller's project scope.
         */
        get: operations["getApproval"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/approvals/{approval}/decisions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An approval `id`. */
                approval: components["parameters"]["ApprovalPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Grant or deny an approval
         * @description A person's decision, appended to the approval and raised as
         *     `approval.granted` or `approval.denied`; the project's workflow
         *     moves the translation (RFC 0006 §3.2). Human-only: an API token
         *     or an MCP agent is refused (`person_required`, 403) — no scope
         *     grants `approvals.decide`. The caller needs `approvals.decide`
         *     for the unit's locale, must be in the approval's eligible party
         *     (`not_eligible`, 403), and must not be the author of the text
         *     under approval (`own_text`, 403: four-eyes). Only the newest
         *     approval of a unit takes decisions. Problem codes:
         *     `approval_closed` (409: already granted or denied),
         *     `approval_superseded` (409: a newer request replaced it),
         *     `invalid_approval` (422: a reason over 2,000 characters).
         */
        post: operations["decideApproval"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/projects/{project}/audit-imports": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Record v0.3's history as imported audit entries
         * @description `glossa import --from v0 --v0-db` sends v0.3's `audit_log`, read
         *     from a restored backup, in batches (RFC 0006 §7.2). Each row
         *     becomes an imported audit entry `v0.translation.changed` in the
         *     tenant's audit trail, in its own chain segment marked as
         *     imported, with actor `v0:<user id>` — never a member of this
         *     platform — and a reference to the imported translation. History
         *     is carried as history: nothing is replayed as a revision, and no
         *     provenance is fabricated.
         *
         *     **No text.** A row carries a translation's before and after only
         *     as SHA-256 digests of their UTF-8 bytes: an audit entry never
         *     holds message or translation text (§6.1). The body has no member
         *     that could carry one, and a member it does not name is ignored.
         *
         *     Writing the tenant's audit trail is the owner's: it needs
         *     `audit.import`, which only `owner` holds, no API token scope
         *     grants and no background principal may be given, by a
         *     principal limited to no project — the trail is the
         *     organisation's, not one project's (RFC 0006 §7.2). Every row is
         *     recorded under the project in the path. Answers
         *     `audit_import_unavailable` (503) on a server that does not run
         *     the importer. Problem codes: `invalid_request` (400),
         *     `invalid_entry` (422: a row is not shaped like v0.3 history;
         *     nothing was recorded), `audit_import_unavailable` (503).
         */
        post: operations["importV0History"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-entries": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * The tenant's audit entries
         * @description The tenant's trail (RFC 0006 §6.1), newest first (`order=asc`
         *     for chain order). An entry is content-free: identifiers and
         *     selectors verbatim, everything else as its shape; never message
         *     or translation text, an email or a secret.
         *
         *     Filters, all optional and combined: `from`/`to` (when it
         *     happened, `[from, to)`), `first_sequence`/`last_sequence` (its
         *     place in the chain, inclusive), `actor` (`person:<id>`,
         *     `token:<id>`, `system:<id>`, `unknown`, or a v0.3 actor),
         *     `action` (an event type such as
         *     `localization.translation.revised`, or a direct action such as
         *     `identity.person.signed_in`), `project`, `source` (`outbox`,
         *     `direct`, `import`), `aggregate_type` and `aggregate_id`.
         *
         *     Needs `audit.read` (owner and admin; no API token scope grants
         *     it). A principal limited to some projects sees only the entries
         *     of those projects — not the tenant-level entries (sign-ins,
         *     members, tokens, vendors, groups), which belong to the
         *     organisation, not to a project. A member whose visibility is
         *     `assigned` is refused. Problem codes: `invalid_query`,
         *     `invalid_page_size`, `invalid_page_token` (400).
         */
        get: operations["listAuditEntries"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-entries/{sequence}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description The entry's place in the tenant's chain. */
                sequence: number;
            };
            cookie?: never;
        };
        /**
         * One audit entry
         * @description Needs `audit.read`. An entry outside a project-scoped caller's
         *     projects — and a tenant-level entry, for such a caller — is not
         *     found, exactly like one that does not exist.
         */
        get: operations["getAuditEntry"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-export-jobs": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        /**
         * Audit export jobs
         * @description Newest first. Needs `audit.export`, by a principal limited to no
         *     project. Problem codes: `invalid_page_size`,
         *     `invalid_page_token` (400).
         */
        get: operations["listAuditExportJobs"];
        put?: never;
        /**
         * Export a range of the audit trail
         * @description Queues a `glossa.audit/v1` export (RFC 0006 §6.2; the format is
         *     platform/README.md's *Audit export format*): two objects,
         *     `entries.jsonl` and a `manifest.json` signed with the
         *     deployment's audit key, downloadable when the job has
         *     `succeeded` and verifiable offline with `glossa audit verify
         *     <dir> --public-key …` (the key is published at
         *     `/.well-known/glossa-audit-keys.json`).
         *
         *     The range is either a time range, `from` and `to` — the entries
         *     that happened in `[from, to)`, at most 31 days (§9.6); a `to` in
         *     the future is cut to now, so an export never claims a range that
         *     has not happened yet — or a sequence range, `first_sequence`
         *     and optionally `last_sequence` (the chain's head when omitted,
         *     fixed when the job is made), at most 1,000,000 entries. An
         *     export is always one unbroken segment of the tenant's chain, so
         *     it is the tenant's whole trail for the range and never one
         *     project's: a project's entries are not a chain on their own and
         *     could not be verified. A time range whose entries are not one
         *     unbroken segment — imported v0.3 history appended in the middle
         *     of it, whose `occurred_at` lies years back — fails with
         *     `range_not_contiguous`; export it by sequence instead.
         *
         *     Needs `audit.export`, which only `owner` holds by default and no
         *     API token scope grants, by a principal limited to no project.
         *     Making the job, and its end, are themselves recorded in the
         *     trail (`audit.export.requested`, `audit.export.completed`).
         *     Problem codes: `invalid_range` (400), `range_too_long`,
         *     `sequence_out_of_range` (422: past the chain's head),
         *     `idempotency_key_reused` (422), `audit_export_unavailable` (503:
         *     this deployment has no audit key, or exports are off).
         */
        post: operations["createAuditExportJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        /**
         * An audit export job
         * @description A job ends `succeeded` — with the chain segment it holds
         *     (`first_sequence`, `last_sequence`, `entry_count`,
         *     `first_prev_hash`, `last_hash`), the key that signed it, and
         *     both files' digests and where to download them — or `failed`
         *     (`failure_code`: `range_not_contiguous`, `internal`). Needs
         *     `audit.export`, by a principal limited to no project.
         */
        get: operations["getAuditExportJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}/file": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        /**
         * Download an audit export's entries.jsonl
         * @description The export's lines, byte for byte, streamed from object storage,
         *     with `Content-Disposition: attachment; filename="entries.jsonl"`
         *     and their SHA-256 — the manifest's `entries.sha256` — as the
         *     `ETag`. Save it beside the manifest as `entries.jsonl`. Files are
         *     kept for the deployment's retention period
         *     (`GLOSSA_AUDIT_EXPORT_RETENTION`, 7 days by default); the job
         *     stays. Needs `audit.export`, by a principal limited to no
         *     project. Problem codes: `export_not_ready` (409), `file_expired`
         *     (410), `storage_unavailable` (503).
         */
        get: operations["downloadAuditExportEntries"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}/manifest": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        /**
         * Download an audit export's signed manifest.json
         * @description The signed manifest, byte for byte as the job wrote it (RFC 8785
         *     canonical JSON), with `Content-Disposition: attachment;
         *     filename="manifest.json"` and its SHA-256 as the `ETag`. Kept
         *     and refused like the entries (`export_not_ready` 409,
         *     `file_expired` 410, `storage_unavailable` 503). Needs
         *     `audit.export`, by a principal limited to no project.
         */
        get: operations["downloadAuditExportManifest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        /** @description An opaque identifier. */
        Id: string;
        /**
         * Format: date-time
         * @description RFC 3339, UTC.
         */
        Timestamp: string;
        /** Format: email */
        Email: string;
        /**
         * @description A BCP 47 language tag. Stored and returned canonicalized
         *     (`en_us` → `en-US`, `iw` → `he`).
         * @example de
         * @example pt-BR
         * @example zh-Hant-TW
         */
        Locale: string;
        /**
         * @description `owner` everything; `admin` everything except owner changes;
         *     `developer` catalog, translations, releases, tokens;
         *     `translator` translates; `reviewer` translates and reviews.
         *     Translators and reviewers can be limited to `locales`.
         * @enum {string}
         */
        Role: "owner" | "admin" | "developer" | "translator" | "reviewer";
        /**
         * @description `read` reads the tenant; `write` pushes messages and
         *     translations; `publish` creates releases; `admin` manages the
         *     tenant, members and tokens (never owners); `workflows` saves and
         *     binds workflow definitions (`workflows.manage`, RFC 0006 §4.2) —
         *     opt-in, implied by no other scope. Every scope implies `read`.
         *     No scope grants review or `approvals.decide`: those are a
         *     person's decisions.
         * @enum {string}
         */
        Scope: "read" | "write" | "publish" | "admin" | "workflows";
        /** @description RFC 9457 problem details. */
        Problem: {
            /**
             * Format: uri
             * @example urn:glossa:problem:slug_taken
             */
            type: string;
            title: string;
            status: number;
            /** @description Stable machine-readable code. */
            code: string;
            detail?: string;
            instance?: string;
            errors?: components["schemas"]["FieldError"][];
        };
        FieldError: {
            /** @description JSON Pointer into the request body. */
            pointer: string;
            detail: string;
        };
        Meta: {
            /**
             * @description How people can sign in here, strongest first: `passkey` when
             *     a WebAuthn relying party is configured, `password` always,
             *     `magic_link` when the server sends email.
             */
            sign_in_methods: ("passkey" | "password" | "magic_link")[];
            /**
             * @description Whether the server sends email. Without it magic links,
             *     email verification and password reset by email are
             *     unavailable (`email_disabled`), a password account works
             *     without a verified address, and invitations wait: only a
             *     verified address accepts one.
             */
            email_delivery: boolean;
            /**
             * Format: uri
             * @description glossa-edge's public base URL, what runtimes are configured
             *     with (`{edge_url}/v1/{key}/{environment}/manifest.json`).
             *     Absent when the deployment doesn't announce it
             *     (`GLOSSA_EDGE_PUBLIC_URL`).
             */
            edge_url?: string;
        };
        EmailRequest: {
            email: components["schemas"]["Email"];
        };
        DeviceAuthorizationRequest: {
            /** @description What the device calls itself, shown to the person who approves it — e.g. `glossa CLI on build-01`. */
            client_name: string;
        };
        DeviceAuthorization: {
            /** @description The device's secret for polling. Never shown to a person. */
            device_code: string;
            /** @description Shown to the person, as `XXXX-XXXX`. */
            user_code: string;
            /**
             * Format: uri
             * @description Studio's page where the person enters the code.
             */
            verification_uri: string;
            /**
             * Format: uri
             * @description The same page with the code filled in.
             */
            verification_uri_complete: string;
            /** @description Seconds until both codes expire. */
            expires_in: number;
            /** @description Seconds to wait between polls. */
            interval: number;
        };
        DeviceAuthorizationView: {
            user_code: string;
            client_name: string;
            /** Format: date-time */
            requested_at: string;
            /** Format: date-time */
            expires_at: string;
        };
        DeviceApproval: {
            user_code: string;
            /** @enum {string} */
            decision: "approved" | "denied";
        };
        DeviceSessionRequest: {
            device_code: string;
        };
        DeviceSession: {
            /** @description `glossa_dev_…`: the person's session as a bearer. */
            access_token: string;
            /** @enum {string} */
            token_type: "Bearer";
            /**
             * Format: date-time
             * @description When the session ends unless the person signs out sooner.
             */
            expires_at: string;
            /** Format: uuid */
            person_id: string;
        };
        TokenRedemption: {
            /** @description The token from the emailed link. */
            token: string;
        };
        Registration: {
            email: components["schemas"]["Email"];
            password: string;
            display_name?: string;
        };
        PasswordSignIn: {
            email: components["schemas"]["Email"];
            password: string;
            totp_code?: string;
        };
        PasswordReset: {
            token: string;
            password: string;
        };
        /** @description The `PublicKeyCredential` from the browser, serialized as JSON. */
        WebAuthnResponse: {
            [key: string]: unknown;
        };
        PasskeyRegistration: {
            /** @description A label such as "MacBook Touch ID". */
            name?: string;
            credential: components["schemas"]["WebAuthnResponse"];
        };
        PasskeyOptions: {
            /** @description WebAuthn options to pass to `navigator.credentials`. */
            options: {
                [key: string]: unknown;
            };
        };
        Passkey: {
            /** @description The credential ID, base64url. */
            id: string;
            name: string;
            created_at: components["schemas"]["Timestamp"];
            /** @description The last sign-in with it; absent until it signs in. */
            last_used_at?: components["schemas"]["Timestamp"];
        };
        PasskeyList: {
            items: components["schemas"]["Passkey"][];
            next_page_token?: string;
        };
        TotpEnrollment: {
            /** @description Base32 secret, for manual entry. */
            secret: string;
            /** @description `otpauth://` URI, for a QR code. */
            otpauth_uri: string;
        };
        TotpCode: {
            code: string;
        };
        Person: {
            id: components["schemas"]["Id"];
            email: components["schemas"]["Email"];
            display_name?: string;
            email_verified: boolean;
            totp_enabled: boolean;
            individual_tenant_id: components["schemas"]["Id"];
            created_at: components["schemas"]["Timestamp"];
        };
        Session: {
            person: components["schemas"]["Person"];
            /** @description Send as `X-CSRF-Token` on unsafe requests. */
            csrf_token: string;
            expires_at: components["schemas"]["Timestamp"];
        };
        Me: {
            person: components["schemas"]["Person"];
            csrf_token: string;
            memberships: components["schemas"]["Membership"][];
        };
        Membership: {
            member_id: components["schemas"]["Id"];
            tenant: components["schemas"]["Tenant"];
            roles: components["schemas"]["Role"][];
            locales: components["schemas"]["Locale"][];
        };
        Tenant: {
            id: components["schemas"]["Id"];
            /** @enum {string} */
            kind: "individual" | "organization";
            slug: string;
            name: string;
            created_at: components["schemas"]["Timestamp"];
        };
        TenantList: {
            items: components["schemas"]["Tenant"][];
            next_page_token?: string;
        };
        CreateTenant: {
            slug: string;
            name: string;
        };
        Member: {
            id: components["schemas"]["Id"];
            email: components["schemas"]["Email"];
            /** @description Set once the invitation is accepted. */
            person_id?: components["schemas"]["Id"];
            display_name?: string;
            /** @enum {string} */
            status: "invited" | "active";
            roles: components["schemas"]["Role"][];
            /** @description Locales a translator or reviewer works on; empty means all. */
            locales: components["schemas"]["Locale"][];
            /** @description The projects the member's roles apply to (RFC 0006 §4.1); empty means every project. */
            projects: components["schemas"]["Id"][];
            /** @description The vendor the member works for (RFC 0006 §3.3); absent for the organization's own people. */
            vendor_id?: components["schemas"]["Id"];
            visibility: components["schemas"]["Visibility"];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        /**
         * @description `all` (the default) reads whatever the member's roles allow;
         *     `assigned` reads only the translation units of assignments given
         *     to them — open, or completed within 30 days — and what
         *     translating them needs, and writes translations in those units
         *     and nothing else (RFC 0006 §3.3). Only a translator can be
         *     `assigned`, and a vendor's member always is.
         * @enum {string}
         */
        Visibility: "all" | "assigned";
        MemberList: {
            items: components["schemas"]["Member"][];
            next_page_token?: string;
        };
        AddMember: {
            email: components["schemas"]["Email"];
            roles: components["schemas"]["Role"][];
            locales?: components["schemas"]["Locale"][];
            /**
             * @description Limits the member's roles to these projects (RFC 0006 §4.1).
             *     Omitted or empty, every project — or, for an inviter limited
             *     to some, exactly theirs. An owner is never limited.
             */
            projects?: components["schemas"]["Id"][];
            /** @description Invites the person as this vendor's member; they must be `visibility: assigned`. */
            vendor_id?: components["schemas"]["Id"];
            visibility?: components["schemas"]["Visibility"];
        };
        /**
         * @description The member's access (`roles`, `locales`) or their restriction
         *     (`projects`, `vendor_id`, `visibility`) — one or the other per
         *     request.
         */
        UpdateMember: {
            roles?: components["schemas"]["Role"][];
            locales?: components["schemas"]["Locale"][];
            /** @description The member's project scope; empty means every project. */
            projects?: components["schemas"]["Id"][];
            /** @description A vendor `id`, or the empty string to take the member off their vendor. */
            vendor_id?: string;
            visibility?: components["schemas"]["Visibility"];
        };
        Token: {
            id: components["schemas"]["Id"];
            name: string;
            /** @description The first characters of the secret, e.g. `glossa_api_Ab3x`. */
            hint: string;
            scopes: components["schemas"]["Scope"][];
            /** @description The projects the token may act on (RFC 0006 §4.1); empty means every project. */
            projects: components["schemas"]["Id"][];
            /** @description `person:<id>` or `token:<id>`. */
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            expires_at?: components["schemas"]["Timestamp"];
            last_used_at?: components["schemas"]["Timestamp"];
            revoked_at?: components["schemas"]["Timestamp"];
        };
        TokenList: {
            items: components["schemas"]["Token"][];
            next_page_token?: string;
        };
        CreateToken: {
            name: string;
            scopes: components["schemas"]["Scope"][];
            /**
             * @description Limits the token to these projects (RFC 0006 §4.1): a project
             *     outside them answers as one that does not exist. Omitted or
             *     empty, every project — or, for a creator who is themselves
             *     limited to some projects, exactly theirs. Naming a project
             *     outside the creator's own is `scope_exceeds_grant` (403).
             */
            projects?: components["schemas"]["Id"][];
            expires_at?: components["schemas"]["Timestamp"];
        };
        Group: {
            id: components["schemas"]["Id"];
            name: string;
            /** @description Member `id`s, sorted. */
            members: components["schemas"]["Id"][];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        GroupList: {
            items: components["schemas"]["Group"][];
            next_page_token?: string;
        };
        GroupName: {
            name: string;
        };
        Vendor: {
            id: components["schemas"]["Id"];
            name: string;
            /** @description Free text — a name, an address. Shown, never mailed to. */
            contact?: string;
            /** @description The locales the vendor offers. They describe it and grant nothing. */
            locales: components["schemas"]["Locale"][];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        VendorList: {
            items: components["schemas"]["Vendor"][];
            next_page_token?: string;
        };
        CreateVendor: {
            name: string;
            contact?: string;
            locales?: components["schemas"]["Locale"][];
        };
        /** @description Members omitted keep their value. */
        UpdateVendor: {
            name?: string;
            contact?: string;
            locales?: components["schemas"]["Locale"][];
        };
        CreatedToken: {
            token: components["schemas"]["Token"];
            /** @description The token value (`glossa_api_…`). Shown only in the first response. */
            secret?: string;
        };
        Slug: string;
        /**
         * @description Authoring syntax: ICU MessageFormat 1 or Unicode MessageFormat 2.
         * @enum {string}
         */
        Syntax: "mf1" | "mf2";
        /**
         * @description What `glossa check` and the Glossa pull-request check decide by
         *     (RFC 0004 §6.4). Both read this one policy, so the terminal and
         *     the pull request never disagree.
         *
         *     A pull request fails when the check finds something at or above
         *     `fail_on`. A key with no translation in a locale that must be
         *     complete is a `missing_translations` finding; a key with no
         *     translation in any other locale is always a warning.
         */
        CheckPolicy: {
            /**
             * @description Which locales must be complete: every one of the project's
             *     (`all`, the default), only those in `locales` (`listed`), or
             *     none of them. `listed` with an empty `locales` is stored as
             *     `none`.
             * @enum {string}
             */
            require_complete: "all" | "listed" | "none";
            /**
             * @description The locales `require_complete: listed` names; empty
             *     otherwise. Every one must be a locale the project has
             *     (`invalid_check_policy`).
             */
            locales?: components["schemas"]["Locale"][];
            /**
             * @description The lowest severity that fails the check: `error` (the
             *     default), `warning` — a warning fails it too — or `never`,
             *     where the check only ever reports.
             * @enum {string}
             */
            fail_on: "error" | "warning" | "never";
            /**
             * @description What an untranslated key in a locale that must be complete
             *     is: an `error` (the default), which fails the check, or a
             *     `warning`, which does not unless `fail_on` is `warning`.
             *     Teams that translate after merging set this to `warning` and
             *     keep every locale required: the check still lists what is
             *     untranslated without blocking the pull request.
             * @enum {string}
             */
            missing_translations: "error" | "warning";
        };
        ProjectSettings: {
            default_syntax: components["schemas"]["Syntax"];
            /**
             * @description New translations wait for review (`needs_review`) unless a
             *     reviewer approves them as they write. When false, new text is
             *     `approved` on write.
             */
            review_required: boolean;
            /**
             * @description The repository's default branch (`main` unless set): usage
             *     uploads of it are what every view of the current usages falls
             *     back to (RFC 0004 §2.2). A valid Git branch name
             *     (`invalid_branch`). Always present in responses; absent in a
             *     write, the project keeps its current one.
             */
            default_branch?: string;
            /**
             * @description The project's check policy. Always present in responses (the
             *     default when the project has never set one); absent in a
             *     write, the project keeps its current one.
             */
            check_policy?: components["schemas"]["CheckPolicy"];
        };
        Project: {
            id: components["schemas"]["Id"];
            slug: components["schemas"]["Slug"];
            name: string;
            source_locale: components["schemas"]["Locale"];
            settings: components["schemas"]["ProjectSettings"];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        ProjectList: {
            items: components["schemas"]["Project"][];
            next_page_token?: string;
        };
        CreateProject: {
            slug: components["schemas"]["Slug"];
            name: string;
            source_locale: components["schemas"]["Locale"];
            settings?: components["schemas"]["ProjectSettings"];
        };
        UpdateProject: {
            slug?: components["schemas"]["Slug"];
            name?: string;
            settings?: components["schemas"]["ProjectSettings"];
        };
        /** @enum {string} */
        Platform: "web" | "api" | "ios" | "android" | "other";
        Application: {
            id: components["schemas"]["Id"];
            slug: components["schemas"]["Slug"];
            name: string;
            platform: components["schemas"]["Platform"];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        ApplicationList: {
            items: components["schemas"]["Application"][];
            next_page_token?: string;
        };
        CreateApplication: {
            slug: components["schemas"]["Slug"];
            name: string;
            platform: components["schemas"]["Platform"];
        };
        UpdateApplication: {
            slug?: components["schemas"]["Slug"];
            name?: string;
            platform?: components["schemas"]["Platform"];
        };
        /**
         * @description A dotted path of `[a-z0-9_-]` segments, unique in the project.
         * @example checkout.payment.submit
         */
        MessageKey: string;
        /** @description Groups messages into separately loadable bundles. Default `default`. */
        Namespace: string;
        /**
         * @description `proposed`: new on an open branch; excluded from non-branch
         *     releases (RFC 0004 §4.1).
         * @enum {string}
         */
        MessageState: "active" | "proposed" | "obsolete";
        /**
         * @description A message in the Unicode MessageFormat 2 data model, exactly as
         *     messageformat/testdata/unicode/data-model/message.schema.json
         *     defines it — the canonical form releases ship.
         */
        MF2Message: {
            [key: string]: unknown;
        };
        Argument: {
            name: string;
            /** @enum {string} */
            type: "string" | "number" | "integer" | "percent" | "currency" | "date" | "time" | "datetime" | "unit" | "select";
            /** @description The annotating function, if any. */
            function?: string;
            selector?: {
                /** @enum {string} */
                kind: "plural" | "ordinal" | "exact" | "string";
                /** @description Literal variant keys; the catch-all `*` always exists. */
                keys: string[];
            };
        };
        MarkupElement: {
            name: string;
            /** @enum {string} */
            kind: "open" | "standalone" | "close";
        };
        MessageContent: {
            /** @description What the author wrote. */
            text: string;
            syntax: components["schemas"]["Syntax"];
            model: components["schemas"]["MF2Message"];
            /** @description Derived from the model on every change. */
            arguments: components["schemas"]["Argument"][];
            markup: components["schemas"]["MarkupElement"][];
        };
        Message: {
            id: components["schemas"]["Id"];
            key: components["schemas"]["MessageKey"];
            namespace: components["schemas"]["Namespace"];
            description: string;
            /** @description Rendered length limit that translation QA checks. */
            max_length?: number;
            state: components["schemas"]["MessageState"];
            source: components["schemas"]["MessageContent"];
            /** @description The current source revision. */
            source_revision: number;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        MessageList: {
            items: components["schemas"]["Message"][];
            next_page_token?: string;
        };
        NamespaceSummary: {
            name: components["schemas"]["Namespace"];
            /** @description Messages in the namespace that are `active`. */
            active_messages: number;
            /** @description Messages in the namespace that are `obsolete`. */
            obsolete_messages: number;
        };
        NamespaceList: {
            items: components["schemas"]["NamespaceSummary"][];
            next_page_token?: string;
        };
        CreateMessage: {
            key: components["schemas"]["MessageKey"];
            text: string;
            syntax?: components["schemas"]["Syntax"];
            namespace?: components["schemas"]["Namespace"];
            description?: string;
            max_length?: number;
        };
        UpdateMessage: {
            namespace?: components["schemas"]["Namespace"];
            description?: string;
            /** @description `0` removes the limit. */
            max_length?: number;
        };
        ReviseSource: {
            text: string;
            syntax?: components["schemas"]["Syntax"];
        };
        RenameMessage: {
            key: components["schemas"]["MessageKey"];
        };
        SourceRevision: {
            revision: number;
            text: string;
            syntax: components["schemas"]["Syntax"];
            model: components["schemas"]["MF2Message"];
            /** @description `person:<id>` or `token:<id>`. */
            author: string;
            created_at: components["schemas"]["Timestamp"];
        };
        SourceRevisionList: {
            items: components["schemas"]["SourceRevision"][];
            next_page_token?: string;
        };
        MessagePreviewRequest: {
            /** @description The message as authored. */
            source: string;
            syntax?: components["schemas"]["Syntax"];
            /** @description Decides which plural keys MF1 accepts, and formatting. */
            locale: components["schemas"]["Locale"];
            /**
             * @description Argument values to format with, by name (without `$`): strings,
             *     numbers or booleans. Omit to only parse.
             */
            values?: {
                [key: string]: unknown;
            };
            /** @description Isolate placeholders with Unicode bidi marks (the MF2 default, `true`). */
            bidi_isolation?: boolean;
        };
        MessagePreviewError: {
            /** @enum {string} */
            stage: "parse" | "format";
            /** @description The MessageFormat kernel's stable error code: `mf1-syntax-error`, `syntax-error`, `unresolved-variable`, … */
            code: string;
            /** @description For humans; wording may change. */
            message: string;
        };
        MessagePreview: {
            /** @description Whether the source parsed into a valid message. */
            valid: boolean;
            message?: components["schemas"]["MF2Message"];
            /** @description The canonical model in MF2 syntax. */
            mf2?: string;
            arguments: components["schemas"]["Argument"][];
            markup: components["schemas"]["MarkupElement"][];
            /** @description The message formatted with `values`, when they were sent and it is valid. */
            formatted?: string;
            errors: components["schemas"]["MessagePreviewError"][];
        };
        ItemError: {
            /** @description Stable machine code. */
            code: string;
            detail: string;
            findings?: components["schemas"]["QAFinding"][];
        };
        MessageUpsertItem: {
            key: string;
            text: string;
            syntax?: components["schemas"]["Syntax"];
            /** @description Omitted: keep (new messages: `default`). */
            namespace?: string;
            /** @description Omitted: keep. */
            description?: string;
            /** @description Omitted: keep. */
            max_length?: number;
            /**
             * @description The source revision the client last saw (0: the message must
             *     not exist yet). A stale one fails the item with
             *     `source_revision_conflict` instead of overwriting.
             */
            base_revision?: number;
        };
        MessageUpsert: {
            items: components["schemas"]["MessageUpsertItem"][];
        };
        MessageUpsertItemResult: {
            key: string;
            /** @enum {string} */
            status: "created" | "revised" | "updated" | "unchanged" | "failed";
            message?: components["schemas"]["Message"];
            error?: components["schemas"]["ItemError"];
        };
        MessageUpsertResult: {
            results: components["schemas"]["MessageUpsertItemResult"][];
        };
        /**
         * @description A Git branch name, unique per project (`feature/checkout-copy`).
         *     It may hold `/`, so URLs address a branch by its `id`, never by
         *     its name; `listBranches?name=…` finds the `id`.
         */
        BranchName: string;
        /**
         * @description `open` while the branch is being worked on: it has a preview
         *     environment, and its proposals live. `merged` and `closed` end
         *     it — the environment is destroyed, and what the default branch
         *     never brought in becomes obsolete 14 days later.
         * @enum {string}
         */
        BranchState: "open" | "merged" | "closed";
        Branch: {
            id: components["schemas"]["Id"];
            name: components["schemas"]["BranchName"];
            state: components["schemas"]["BranchState"];
            /** @description The pull request the branch has, when it has one. */
            pr_number?: number;
            /** @description The commit last pushed. */
            head_commit?: string;
            /** @description Where CI deployed the branch's preview. */
            preview_url?: string;
            /** @description When the branch was closed or merged; absent while it is open. */
            closed_at?: components["schemas"]["Timestamp"];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        BranchList: {
            items: components["schemas"]["Branch"][];
            next_page_token?: string;
        };
        UpsertBranch: {
            name: components["schemas"]["BranchName"];
            /** @description Omitted: keep. */
            pr_number?: number;
            /** @description Omitted: keep. */
            head_commit?: string;
            /** @description Omitted: keep; "" clears it. */
            preview_url?: string;
        };
        BranchPreview: {
            /** @description An absolute http(s) URL; "" clears it. */
            url: string;
        };
        BranchPush: {
            branch: components["schemas"]["BranchName"];
            /** @description Omitted: keep. */
            pr_number?: number;
            /** @description Omitted: keep. */
            head_commit?: string;
            /**
             * @description The items are the branch's whole catalog: keys it proposed
             *     before and no longer has are withdrawn, and the project's
             *     live keys it lacks are reported in `removed`. A partial push
             *     only adds.
             * @default false
             */
            complete: boolean;
            /** @description `base_revision` is ignored: a branch never overwrites anyone's edit. */
            items: components["schemas"]["MessageUpsertItem"][];
        };
        BranchItemResult: {
            key: string;
            /**
             * @description `new_key`: the branch proposes the key's message.
             *     `source_proposal`: it proposes new source for a live
             *     message. `unchanged`: the live source already says this.
             *     `key_conflict`: another open branch proposes the same new
             *     key with different source.
             * @enum {string}
             */
            status: "new_key" | "source_proposal" | "unchanged" | "key_conflict" | "failed";
            message?: components["schemas"]["Message"];
            error?: components["schemas"]["ItemError"];
        };
        KeyConflict: {
            key: components["schemas"]["MessageKey"];
            /** @description The other open branches proposing this key with different source. */
            branches: components["schemas"]["BranchName"][];
        };
        /** @description What a branch proposes, and what merging it would do. */
        BranchStatus: {
            branch: components["schemas"]["Branch"];
            /** @description Keys the project doesn't have; their messages are `proposed`. */
            new_keys: components["schemas"]["MessageKey"][];
            /** @description Live keys whose source the branch changes. */
            source_proposals: components["schemas"]["MessageKey"][];
            /** @description Live keys the last complete push lacked. Reported only; nothing is obsoleted. */
            removed: components["schemas"]["MessageKey"][];
            conflicts: components["schemas"]["KeyConflict"][];
            /**
             * @description Per locale, how many current translations the branch's
             *     source proposals will make outdated when it merges. Empty
             *     without `translations.read`.
             */
            outdated: {
                [key: string]: number;
            };
        };
        BranchPushResult: components["schemas"]["BranchStatus"] & {
            /** @description One result per pushed item, in request order. */
            items: components["schemas"]["BranchItemResult"][];
        };
        /** @description What one branch proposes for one key. */
        Proposal: {
            key: components["schemas"]["MessageKey"];
            /**
             * @description `new_key`: the branch owns the key's `proposed` message
             *     (shared with every other branch proposing the same source
             *     for it). `source_change`: the message is live and the branch
             *     proposes new source for it, against `base_revision`.
             * @enum {string}
             */
            kind: "new_key" | "source_change";
            message_id: components["schemas"]["Id"];
            source: components["schemas"]["MessageContent"];
            /** @description For a source change, the revision it was proposed against. */
            base_revision?: number;
            /** @description `person:<id>`, `token:<id>` or `system:<name>`. */
            author?: string;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        ProposalList: {
            items: components["schemas"]["Proposal"][];
            next_page_token?: string;
        };
        /**
         * @description Derived from the locale's (likely) script.
         * @enum {string}
         */
        Direction: "ltr" | "rtl";
        ProjectLocale: {
            code: components["schemas"]["Locale"];
            direction: components["schemas"]["Direction"];
            is_source: boolean;
            created_at: components["schemas"]["Timestamp"];
        };
        LocaleList: {
            items: components["schemas"]["ProjectLocale"][];
            next_page_token?: string;
        };
        AddLocale: {
            code: components["schemas"]["Locale"];
        };
        FallbackGraph: {
            /** @description A locale (or `*`) to its ordered fallback locales: `{"de-AT": ["de"], "*": ["en"]}`. */
            fallback: {
                [key: string]: components["schemas"]["Locale"][];
            };
        };
        PutFallbackGraph: {
            fallback: {
                [key: string]: components["schemas"]["Locale"][];
            };
        };
        /** @enum {string} */
        ReviewState: "draft" | "needs_review" | "approved" | "rejected";
        /** @enum {string} */
        Origin: "human" | "ai" | "agent" | "translation_memory" | "machine_translation" | "import" | "adaptation";
        QAFinding: {
            /** @description Stable finding code, e.g. `missing-argument`, `max-length-exceeded`. */
            code: string;
            /** @enum {string} */
            severity: "error" | "warning";
            /** @description The argument or markup element concerned. */
            subject?: string;
            detail?: string;
            /** @description For humans; wording may change. */
            message: string;
        };
        QAProblem: components["schemas"]["Problem"] & {
            findings?: components["schemas"]["QAFinding"][];
        };
        Translation: {
            id: components["schemas"]["Id"];
            message_id: components["schemas"]["Id"];
            locale: components["schemas"]["Locale"];
            text: string;
            syntax: components["schemas"]["Syntax"];
            model: components["schemas"]["MF2Message"];
            state: components["schemas"]["ReviewState"];
            origin: components["schemas"]["Origin"];
            /** @description Who wrote the current text. */
            author: string;
            /**
             * @description Present only when the caller holds `translations.review` for
             *     the locale. `true`: no one else could review it, so the caller
             *     may approve or reject text they wrote, or write it as approved
             *     (recorded as a self-review). `false`: someone else must
             *     (`own_text`), and a write asking for `approved` lands as
             *     `needs_review`. Only the single-message translation list, and
             *     the responses of a write and a review, carry it.
             */
            review_by_author_allowed?: boolean;
            /** @description The source revision the text was made against. */
            source_revision: number;
            /** @description The message's current source revision as Localization knows it. */
            current_source_revision: number;
            /** @description `source_revision < current_source_revision`. */
            outdated: boolean;
            warnings: components["schemas"]["QAFinding"][];
            revision: number;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        TranslationList: {
            items: components["schemas"]["Translation"][];
            next_page_token?: string;
        };
        /** @description A translation with the message it belongs to. */
        ProjectTranslation: components["schemas"]["Translation"] & {
            key: components["schemas"]["MessageKey"];
            namespace: components["schemas"]["Namespace"];
            message_state: components["schemas"]["MessageState"];
        };
        ProjectTranslationList: {
            items: components["schemas"]["ProjectTranslation"][];
            next_page_token?: string;
        };
        ReviewStateCounts: {
            draft: number;
            needs_review: number;
            approved: number;
            rejected: number;
        };
        LocaleStats: {
            code: components["schemas"]["Locale"];
            direction: components["schemas"]["Direction"];
            is_source: boolean;
            /** @description Active messages with a usable (not rejected) translation. */
            translated: number;
            /** @description Active messages without one: untranslated or rejected. */
            missing: number;
            /** @description Usable translations made against an older source revision. */
            outdated: number;
            /** @description Translations of active messages per review state. */
            states: components["schemas"]["ReviewStateCounts"];
        };
        TranslationStats: {
            /** @description The project's active messages. */
            messages: number;
            locales: components["schemas"]["LocaleStats"][];
        };
        PutTranslation: {
            text: string;
            syntax?: components["schemas"]["Syntax"];
            state?: components["schemas"]["ReviewState"];
            origin?: components["schemas"]["Origin"];
            /** @description Provenance specifics — model, prompt version, TM match, source job… */
            origin_detail?: {
                [key: string]: unknown;
            };
            source_revision?: number;
        };
        ReviewTranslation: {
            state: components["schemas"]["ReviewState"];
        };
        TranslationRevision: {
            revision: number;
            /** @enum {string} */
            kind: "content" | "review";
            /**
             * @description The author approved or rejected their own text because no one
             *     else could review it (RFC 0006 §15 Q6). No text is stored with
             *     the flag.
             */
            self_review: boolean;
            text: string;
            syntax: components["schemas"]["Syntax"];
            state: components["schemas"]["ReviewState"];
            origin: components["schemas"]["Origin"];
            origin_detail: {
                [key: string]: unknown;
            };
            author: string;
            source_revision: number;
            findings: components["schemas"]["QAFinding"][];
            created_at: components["schemas"]["Timestamp"];
        };
        TranslationRevisionList: {
            items: components["schemas"]["TranslationRevision"][];
            next_page_token?: string;
        };
        TranslationImportItem: {
            key: string;
            locale: string;
            text: string;
            syntax?: components["schemas"]["Syntax"];
            state?: components["schemas"]["ReviewState"];
            origin_detail?: {
                [key: string]: unknown;
            };
            source_revision?: number;
        };
        TranslationImport: {
            items: components["schemas"]["TranslationImportItem"][];
        };
        TranslationImportItemResult: {
            key: string;
            locale: string;
            /** @enum {string} */
            status?: "created" | "revised" | "reviewed" | "unchanged";
            translation?: components["schemas"]["Translation"];
            error?: components["schemas"]["ItemError"];
        };
        TranslationImportResult: {
            results: components["schemas"]["TranslationImportItemResult"][];
        };
        /**
         * @description Which translations ship to an environment: those in `states`
         *     (never `rejected`), and outdated ones (made against an older
         *     source revision) only with `include_outdated`. Production and
         *     staging start with `approved`, the others with everything.
         */
        EnvironmentPolicy: {
            states: ("draft" | "needs_review" | "approved")[];
            include_outdated: boolean;
        };
        Environment: {
            name: components["schemas"]["EnvironmentName"];
            kind: components["schemas"]["EnvironmentKind"];
            /** @description The branch a `branch` environment previews; absent for a standard one. */
            branch?: components["schemas"]["BranchName"];
            policy: components["schemas"]["EnvironmentPolicy"];
            /** @description Present when a publish or promote here needs approval (RFC 0006 §5.1). */
            approval?: components["schemas"]["EnvironmentApproval"];
            current_release_id?: components["schemas"]["Id"];
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        /**
         * @description `standard` serves the main catalog (the default environments and
         *     custom ones). `branch` is one open branch's preview: the main
         *     catalog plus that branch's overlay, under a fixed policy
         *     (everything not rejected, outdated included), published again
         *     when the branch changes. Its releases can't be promoted
         *     (`branch_release_not_promotable`), because they hold text that
         *     exists only on the branch, and a project has at most 50 of them
         *     (`too_many_branches`). Opening, publishing and destroying one
         *     follows its branch; nothing creates one by hand.
         * @enum {string}
         */
        EnvironmentKind: "standard" | "branch";
        /**
         * @description `development`, `preview`, `staging`, `production` or a custom
         *     name (not `a`). `pr-<number>` and `br-<8 hex>` are reserved for
         *     branch environments.
         */
        EnvironmentName: string;
        EnvironmentList: {
            items: components["schemas"]["Environment"][];
            next_page_token?: string;
        };
        CreateEnvironment: {
            name: components["schemas"]["EnvironmentName"];
            policy?: components["schemas"]["EnvironmentPolicy"];
        };
        UpdateEnvironment: {
            policy: components["schemas"]["EnvironmentPolicy"];
            /** @description Sets the approval requirement; omitted keeps it as it is. */
            approval?: components["schemas"]["EnvironmentApproval"];
            /** @description Switches the approval requirement off. Not together with `approval`. */
            clear_approval?: boolean;
        };
        Promotion: {
            release_id: components["schemas"]["Id"];
            /**
             * @description Promote although the environment's completeness requirement
             *     is not met (`policy_not_met`). Promotion is gated exactly as
             *     publishing is: a release that may not be published straight
             *     to production may not reach it by the side door either. It
             *     needs a `force_reason`, and the deployment records both.
             */
            force?: boolean;
            /**
             * @description Why the requirement was set aside. Required with `force`
             *     (`force_reason_required`) and refused without it
             *     (`invalid_force_reason`).
             */
            force_reason?: string;
        };
        Rollback: {
            release_id?: components["schemas"]["Id"];
        };
        Deployment: {
            /** @description Counts the environment's deployments from 1. */
            number: number;
            release_id: components["schemas"]["Id"];
            previous_release_id?: components["schemas"]["Id"];
            /** @enum {string} */
            action: "publish" | "promote" | "rollback";
            /** @description `person:<id>` or `token:<id>`. */
            author: string;
            created_at: components["schemas"]["Timestamp"];
            /**
             * @description True where the environment's completeness requirement was
             *     not met and somebody went ahead anyway. The exception is the
             *     record: such a deployment always carries its reason.
             */
            forced: boolean;
            /** @description Why it went ahead; present exactly when `forced`. */
            force_reason?: string;
        };
        /**
         * @description Exactly one of `member` (a member `id`), `role` or `group` (a
         *     group `id` or name, lower-case) — who may approve. A vendor
         *     delivers work and does not sign it off, so it is not a party
         *     here.
         */
        EnvironmentApprovalParty: {
            member?: string;
            role?: components["schemas"]["Role"];
            group?: string;
        };
        /**
         * @description `n` distinct people of `from`, none of them the requester, must
         *     grant a publish or promote into the environment before its
         *     pointer moves. `distinct_from_requester` is always true:
         *     self-approval is not offered (RFC 0006 §15 q6).
         */
        EnvironmentApproval: {
            n: number;
            from: components["schemas"]["EnvironmentApprovalParty"];
            distinct_from_requester: boolean;
        };
        /**
         * @description `pending` waits for approvals; the others are final. `refused`:
         *     approved, but the completeness requirement, run again at deploy
         *     time, refused it — `reason` says why.
         * @enum {string}
         */
        ReleaseRequestState: "pending" | "deployed" | "denied" | "withdrawn" | "refused";
        /** @description What the environment's completeness requirement said when the request was made. */
        GateVerdict: {
            met: boolean;
            /** @description What the requirement asked for and the release lacks; absent when met. */
            unmet?: string[];
        };
        ReleaseRequest: {
            id: components["schemas"]["Id"];
            environment: components["schemas"]["EnvironmentName"];
            release_id: components["schemas"]["Id"];
            /**
             * @description What the deploy records.
             * @enum {string}
             */
            action: "publish" | "promote";
            /** @description Who asked (`person:…`, `token:…`). They never count toward the approval. */
            requester: string;
            approval: components["schemas"]["EnvironmentApproval"];
            gate: components["schemas"]["GateVerdict"];
            /**
             * @description The requester overrode the completeness requirement. Force
             *     overrides the gate, never the approval: the approvers see it.
             */
            forced: boolean;
            /** @description Why; present exactly when `forced`. */
            force_reason?: string;
            state: components["schemas"]["ReleaseRequestState"];
            /** @description Who closed it: the last approver, the denier, or who withdrew it. */
            decided_by?: string;
            decided_at?: components["schemas"]["Timestamp"];
            /** @description Why it was refused or withdrawn. */
            reason?: string;
            created_at: components["schemas"]["Timestamp"];
        };
        ReleaseRequestList: {
            items: components["schemas"]["ReleaseRequest"][];
            next_page_token?: string;
        };
        /** @description A publish or promote held for approval. No pointer moved. */
        ReleaseHeld: {
            /** @description The release the request would deploy: the one just recorded by a publish, or the one a promote named. */
            id: components["schemas"]["Id"];
            release_request_id: components["schemas"]["Id"];
            release_request: components["schemas"]["ReleaseRequest"];
        };
        ReleaseRequestWithdrawal: {
            reason?: string;
        };
        Rollout: {
            id: components["schemas"]["Id"];
            environment: components["schemas"]["EnvironmentName"];
            /** @description The candidate. */
            release_id: components["schemas"]["Id"];
            /** @description What the environment served when the rollout started. */
            stable_release_id: components["schemas"]["Id"];
            percent: number;
            /** @enum {string} */
            status: "active" | "completed" | "aborted";
            /**
             * @description How an ended rollout ended.
             * @enum {string}
             */
            end?: "completed" | "aborted" | "expired" | "rolled_back";
            max_duration_seconds: number;
            expires_at: components["schemas"]["Timestamp"];
            /** @description The candidate was started past the completeness requirement. */
            forced: boolean;
            /** @description Why; present exactly when `forced`. */
            force_reason?: string;
            started_by: string;
            started_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            ended_by?: string;
            ended_at?: components["schemas"]["Timestamp"];
        };
        RolloutList: {
            items: components["schemas"]["Rollout"][];
            next_page_token?: string;
        };
        StartRollout: {
            release_id: components["schemas"]["Id"];
            /** @description The share of installations in the candidate, 0–100 (`invalid_percent`). */
            percent: number;
            /**
             * @description When the rollout is aborted unless a person ended it first:
             *     one hour to 90 days (`invalid_max_duration`); 14 days when
             *     omitted.
             */
            max_duration_seconds?: number;
            /**
             * @description Start although the environment's completeness requirement
             *     refuses the candidate (`policy_not_met`). It needs a
             *     `force_reason`, which the completing deployment records.
             */
            force?: boolean;
            /** @description Required with `force` (`force_reason_required`) and refused without it (`invalid_force_reason`). */
            force_reason?: string;
        };
        AdvanceRollout: {
            /** @description 0–100 (`invalid_percent`). */
            percent: number;
        };
        /**
         * @description One row of v0.3's `audit_log`. No text: the translation's before
         *     and after are SHA-256 digests of their UTF-8 bytes.
         */
        V0HistoryEntry: {
            /** @description v0.3's `audit_log` id: the idempotency key. */
            v0_id: string;
            /** @description As the plan names it (`v0.translation.changed`). */
            action: string;
            /** @description `v0:<uuid>`, `v0:ai:<label>`, `v0:system:<label>` or `v0:unknown`; never a member of this platform. */
            actor: string;
            occurred_at: components["schemas"]["Timestamp"];
            /** @description The translation's message, when the row still resolves to one. */
            key?: components["schemas"]["MessageKey"];
            locale?: components["schemas"]["Locale"];
            /** @description Why the row resolves to no translation. */
            unresolved?: string;
            before_sha256?: string;
            after_sha256?: string;
        };
        V0HistoryImport: {
            /** @description The dump the rows were read from, as the restore marker names it. */
            restore: string;
            restore_sha256: string;
            entries: components["schemas"]["V0HistoryEntry"][];
        };
        /**
         * @description Where an entry came from: a domain event (`outbox`), an act that
         *     never reaches the outbox such as a sign-in or an MCP tool call
         *     (`direct`), or v0.3's imported history (`import`).
         * @enum {string}
         */
        AuditSource: "outbox" | "direct" | "import";
        /**
         * @description One entry of the tenant's hash chain, as its `glossa.audit/v1`
         *     export line holds it (RFC 0006 §6.1). Content-free: `summary`
         *     keeps identifiers and selectors verbatim and records everything
         *     else as its shape (`"string(len=27)"`).
         */
        AuditEntry: {
            /**
             * Format: int64
             * @description Its place in the tenant's chain.
             */
            sequence: number;
            /** @description The act recorded: the outbox event's id, or a direct write's own. */
            event_id: string;
            source: components["schemas"]["AuditSource"];
            /** @description The event type (`release.published`) or direct action (`identity.person.signed_in`). */
            action: string;
            /** @description `person:<id>`, `token:<id>`, `system:<id>`, `unknown`, or a v0.3 actor (`v0:<id>`). */
            actor: string;
            occurred_at: components["schemas"]["Timestamp"];
            aggregate_type: string;
            aggregate_id: string;
            project_id?: components["schemas"]["Id"];
            locale?: string;
            /** @description The act without its content. */
            summary: {
                [key: string]: unknown;
            };
            request_id?: string;
            trace_id?: string;
            /** @description The previous entry's hash (64 zero digits for the first). */
            prev_hash: string;
            /** @description sha256(prev_hash ‖ JCS(entry)). */
            hash: string;
        };
        AuditEntryList: {
            items: components["schemas"]["AuditEntry"][];
            next_page_token?: string;
        };
        /**
         * @description Exactly one range: a time range (`from` and `to`), or a
         *     sequence range (`first_sequence`, and `last_sequence` or the
         *     chain's head).
         */
        AuditExportJobCreate: {
            from?: components["schemas"]["Timestamp"];
            to?: components["schemas"]["Timestamp"];
            /** Format: int64 */
            first_sequence?: number;
            /** Format: int64 */
            last_sequence?: number;
        };
        /** @enum {string} */
        AuditExportJobState: "queued" | "running" | "succeeded" | "failed";
        AuditExportFile: {
            /** @description `entries.jsonl` or `manifest.json`. */
            path: string;
            sha256: string;
            /** Format: int64 */
            bytes: number;
            /** @description Where to download it; present while it is kept. */
            download_url?: string;
        };
        AuditExportJob: {
            id: components["schemas"]["Id"];
            state: components["schemas"]["AuditExportJobState"];
            /** @description A time range's start, as asked. */
            from?: components["schemas"]["Timestamp"];
            /** @description A time range's end, as asked, or cut to when the job was made. */
            to?: components["schemas"]["Timestamp"];
            /**
             * Format: int64
             * @description The first entry; for a time range, once the job has run.
             */
            first_sequence?: number;
            /**
             * Format: int64
             * @description The last entry; for a time range, once the job has run. `first_sequence − 1` for an empty export.
             */
            last_sequence?: number;
            /** Format: int64 */
            entry_count?: number;
            /** @description Where the export joins the chain before it. */
            first_prev_hash?: string;
            /** @description The head the next export's `first_prev_hash` continues from. */
            last_hash?: string;
            /** @description The audit key that signed the manifest. */
            key_id?: string;
            entries?: components["schemas"]["AuditExportFile"];
            manifest?: components["schemas"]["AuditExportFile"];
            failure_code?: string;
            failure_message?: string;
            attempts: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            started_at?: components["schemas"]["Timestamp"];
            finished_at?: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            /** @description When retention deletes the files. */
            expires_at: components["schemas"]["Timestamp"];
            files_deleted_at?: components["schemas"]["Timestamp"];
        };
        AuditExportJobList: {
            items: components["schemas"]["AuditExportJob"][];
            next_page_token?: string;
        };
        /** @description A `glossa.audit/v1` manifest (platform/README.md, *Audit export format*), as signed. */
        AuditExportManifest: {
            [key: string]: unknown;
        };
        V0HistoryReport: {
            recorded: number;
            /** @description Rows an earlier import recorded already (same `v0_id`). */
            existing: number;
        };
        DeploymentList: {
            items: components["schemas"]["Deployment"][];
            next_page_token?: string;
        };
        PublishRelease: {
            environment: components["schemas"]["EnvironmentName"];
            note?: string;
            /**
             * @description Publish although the environment's completeness requirement
             *     is not met (`policy_not_met`). It needs a `force_reason`,
             *     and the deployment records both, so the exception is part of
             *     the history. A gate with no escape hatch gets routed around
             *     by switching the requirement off, which leaves no record at
             *     all.
             */
            force?: boolean;
            /**
             * @description Why the requirement was set aside. Required with `force`
             *     (`force_reason_required`) and refused without it
             *     (`invalid_force_reason`).
             */
            force_reason?: string;
        };
        ReleaseLocale: {
            code: components["schemas"]["Locale"];
            direction: components["schemas"]["Direction"];
        };
        ReleaseLocaleCounts: {
            messages: number;
            /** @description Outdated translations shipped (the policy allowed them). */
            outdated: number;
        };
        ReleaseCounts: {
            /** @description Source messages. */
            messages: number;
            artifacts: number;
            bytes: number;
            /** @description Artifacts this publish uploaded; the rest were already stored. */
            new_artifacts: number;
            /** @description Per locale code. */
            locales: {
                [key: string]: components["schemas"]["ReleaseLocaleCounts"];
            };
        };
        Release: {
            id: components["schemas"]["Id"];
            /** @description Counts the project's releases from 1. */
            version: number;
            /** @description What its environment served before it. */
            parent_id?: components["schemas"]["Id"];
            environment: components["schemas"]["EnvironmentName"];
            policy: components["schemas"]["EnvironmentPolicy"];
            /**
             * @description SHA-256 of the RFC 8785 canonical JSON of what every
             *     environment's manifest of this release carries (`sourceLocale`,
             *     `locales`, `fallback`, `artifacts`). Equal digests ship
             *     exactly the same text.
             */
            manifest_digest: string;
            source_locale: components["schemas"]["Locale"];
            locales: components["schemas"]["ReleaseLocale"][];
            counts: components["schemas"]["ReleaseCounts"];
            note?: string;
            /** @description `person:<id>` or `token:<id>`. */
            author: string;
            created_at: components["schemas"]["Timestamp"];
        };
        ReleaseList: {
            items: components["schemas"]["Release"][];
            next_page_token?: string;
        };
        LocaleDiff: {
            locale: components["schemas"]["Locale"];
            added: string[];
            changed: string[];
            removed: string[];
        };
        ReleaseDiff: {
            release_id: components["schemas"]["Id"];
            base_release_id?: components["schemas"]["Id"];
            locales: components["schemas"]["LocaleDiff"][];
        };
        /** @description One reason the catalog can't be released. */
        ReleaseProblem: {
            /** @enum {string} */
            code: "not_releasable";
            detail: string;
            /** @description The message key, when one message is the cause. */
            key?: string;
            /** @description The locale, when one locale or translation is the cause. */
            locale?: string;
        };
        ReleasePreview: {
            environment: components["schemas"]["EnvironmentName"];
            /** @description The environment's policy, which the build used. */
            policy: components["schemas"]["EnvironmentPolicy"];
            /** @description The release the environment serves now, which `changes` compare with; absent when it serves none (everything is added). */
            base_release_id?: components["schemas"]["Id"];
            /** @description `false` when `problems` lists why publishing would fail. */
            releasable: boolean;
            problems: components["schemas"]["ReleaseProblem"][];
            /** @description The release's manifest digest; equal to the served release's when nothing would change. Present when releasable. */
            manifest_digest?: string;
            source_locale?: components["schemas"]["Locale"];
            locales?: components["schemas"]["ReleaseLocale"][];
            /** @description What the release would ship; `new_artifacts` is what publishing would upload. Present when releasable. */
            counts?: components["schemas"]["ReleaseCounts"];
            /** @description Per locale, compared with `base_release_id`. Present when releasable. */
            changes?: components["schemas"]["LocaleDiff"][];
        };
        /** @description A `glossa.manifest/v1` manifest (runtimes/testdata/schemas/manifest.schema.json), as served. */
        ReleaseManifest: {
            [key: string]: unknown;
        };
        /** @description A `glossa.artifact/v1` artifact (runtimes/testdata/schemas/artifact.schema.json), as stored. */
        ReleaseArtifact: {
            [key: string]: unknown;
        };
        SigningKey: {
            /** @description The manifest's `signatures[].keyId`. */
            key_id: string;
            /** @enum {string} */
            algorithm: "Ed25519";
            /** @description The raw 32-byte public key, base64url without padding. */
            public_key: string;
            /** @description Whether it signs new manifests. */
            active: boolean;
        };
        SigningKeys: {
            keys: components["schemas"]["SigningKey"][];
        };
        DeliveryKey: {
            id: components["schemas"]["Id"];
            name: string;
            /** @description Publishable by design; shown on every read. */
            key: string;
            scope: components["schemas"]["DeliveryKeyScope"];
            /** @description `person:<id>` or `token:<id>`. */
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            revoked_at?: components["schemas"]["Timestamp"];
        };
        /**
         * @description What the key reads at the edge: the environments on its
         *     allowlist and, with `branches`, every branch preview. Anything
         *     outside it answers 404, exactly like an unknown key. Branch
         *     previews hold unreleased copy, so a key that ships in a
         *     production bundle must not reach them.
         */
        DeliveryKeyScope: {
            /**
             * @description Environments by name — never a branch environment
             *     (`pr-<n>`, `br-<hash>`): those are reached through
             *     `branches`. Empty only when `branches` is true.
             */
            environments: components["schemas"]["EnvironmentName"][];
            /** @description A preview key, for preview deployments only; it reads every branch environment. */
            branches: boolean;
        };
        DeliveryKeyList: {
            items: components["schemas"]["DeliveryKey"][];
            next_page_token?: string;
        };
        /**
         * @description A web origin: scheme, host and port, with the default port
         *     dropped and nothing else — no path, query, fragment or
         *     userinfo. `https` everywhere; `http` only on `localhost`,
         *     `127.0.0.1`, `[::1]` or a `.localhost` name, which is a
         *     developer's own machine and never a deployment anyone else
         *     reaches. Canonicalized on write, so `HTTPS://App.Example.com:443`
         *     is stored and compared as `https://app.example.com`.
         * @example https://preview.example.com
         * @example http://localhost:5173
         */
        WebOrigin: string;
        PreviewOrigin: {
            id: components["schemas"]["Id"];
            origin: components["schemas"]["WebOrigin"];
            /** @description What people call this deployment. */
            label: string;
            /**
             * @description True for a plain-http loopback origin: a developer's local
             *     run, never a shared deployment.
             */
            development: boolean;
            /** @description `person:<id>` or `token:<id>`. */
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
        };
        PreviewOriginList: {
            items: components["schemas"]["PreviewOrigin"][];
        };
        CreatePreviewOrigin: {
            origin: components["schemas"]["WebOrigin"];
            label?: string;
        };
        CreateInContextGrant: {
            /**
             * @description The origin the editor runs on. It must be registered for
             *     this project, and it is the only origin the grant works
             *     from.
             */
            origin: components["schemas"]["WebOrigin"];
        };
        /**
         * @description The in-product editor's credential. `token` is shown exactly
         *     once, here; nothing stores it but the page's memory.
         */
        InContextGrant: {
            /** @description The bearer credential. Never put it in `localStorage`. */
            token: string;
            expires_at: components["schemas"]["Timestamp"];
            project_id: components["schemas"]["Id"];
            origin: components["schemas"]["WebOrigin"];
            /** @description Who the editor acts as; every edit names them. */
            person_id: components["schemas"]["Id"];
            /**
             * @description What the grant allows: the person's own permissions
             *     intersected with `catalog.read`, `knowledge.read`,
             *     `translations.read`, `translations.write`,
             *     `intelligence.read` and `intelligence.translate`, and never
             *     more. Studio shows this list before minting.
             */
            permissions: components["schemas"]["InContextPermission"][];
        };
        InContextPermission: {
            /** @enum {string} */
            permission: "catalog.read" | "knowledge.read" | "translations.read" | "translations.write" | "intelligence.read" | "intelligence.translate";
            /**
             * @description The locales the permission holds for; absent means every
             *     locale. A translator limited to some locales in Studio is
             *     limited to the same ones in the editor.
             */
            locales?: components["schemas"]["Locale"][];
        };
        GitHubOIDCExchange: {
            /**
             * @description The compact JWS GitHub Actions issued for audience `glossa`
             *     (`ACTIONS_ID_TOKEN_REQUEST_URL?audience=glossa`). It is the
             *     request's only credential and is never stored or logged.
             */
            id_token: string;
            /**
             * @description Which project to authenticate for, needed only when the
             *     repository feeds several — a monorepo with one Git
             *     connection per path. Naming a project the repository does
             *     not feed is `repository_not_connected`, the same answer as
             *     an unconnected repository.
             */
            project_id?: components["schemas"]["Id"];
        };
        /**
         * @description A GitHub Actions run's credential. `token` is shown exactly
         *     once, here; CI keeps it for the length of the job and nothing
         *     stores it afterwards.
         */
        CIToken: {
            /** @description The bearer credential. Never echo it into a log. */
            token: string;
            expires_at: components["schemas"]["Timestamp"];
            tenant_id: components["schemas"]["Id"];
            /** @description The only project this token may act on. */
            project_id: components["schemas"]["Id"];
            /**
             * @description What the token allows, and the whole of it: `catalog.read`
             *     and `catalog.write`.
             */
            permissions: ("catalog.read" | "catalog.write")[];
            /**
             * Format: int64
             * @description The `repository_id` claim the decision rested on — GitHub's
             *     immutable number, echoed back so CI can see what it
             *     authenticated as.
             */
            repository_id: number;
            /** @description "owner/name" when the token was issued. A label; nothing is keyed on it. */
            repository?: string;
        };
        CreateDeliveryKey: {
            /** @description What uses it, e.g. "web" or "go-emails". */
            name: string;
            /** @description Omitted: `production` only. Change it later with `setDeliveryKeyScope`. */
            scope?: components["schemas"]["DeliveryKeyScope"];
        };
        /**
         * @description A translation-memory unit, derived from an approved translation.
         *     `source` and `target` are canonical MF2; `source_normalized` is
         *     what matching compares (placeholders by position, markup as
         *     tags) and `signature` the placeholders' types by position.
         */
        TMUnit: {
            id: components["schemas"]["Id"];
            /** @description Absent for a tenant-wide unit. */
            project_id?: components["schemas"]["Id"];
            /** @enum {string} */
            origin: "translation" | "import";
            translation_id?: components["schemas"]["Id"];
            /** @description The translation revision the unit reflects. */
            translation_revision?: number;
            message_id?: components["schemas"]["Id"];
            message_key?: components["schemas"]["MessageKey"];
            namespace?: components["schemas"]["Namespace"];
            source_locale: components["schemas"]["Locale"];
            target_locale: components["schemas"]["Locale"];
            source: string;
            target: string;
            target_model: components["schemas"]["MF2Message"];
            source_normalized: string;
            /** @example 1:number/plural,2:string */
            signature: string;
            /** @enum {string} */
            state: "active" | "retired";
            hit_count: number;
            last_hit_at?: components["schemas"]["Timestamp"];
            /** @description Who wrote the text. */
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            retired_at?: components["schemas"]["Timestamp"];
            /** @enum {string} */
            retired_reason?: "superseded" | "unapproved" | "overwritten" | "deleted";
            retired_by?: string;
        };
        TMUnitList: {
            items: components["schemas"]["TMUnit"][];
            next_page_token?: string;
        };
        TMLookup: {
            /** @description The source message. */
            source: string;
            syntax?: components["schemas"]["Syntax"];
            source_locale: components["schemas"]["Locale"];
            target_locale: components["schemas"]["Locale"];
            /** @description The project the message belongs to. */
            project_id?: components["schemas"]["Id"];
            /**
             * @description Also match other projects' units.
             * @default false
             */
            all_projects: boolean;
            message_key?: components["schemas"]["MessageKey"];
            namespace?: components["schemas"]["Namespace"];
            /** @default 5 */
            limit: number;
            /** @default 50 */
            min_score: number;
            /** @default false */
            count_hits: boolean;
            /** @description The syntax of each match's `target_text`; by default the query's `syntax`. */
            target_syntax?: components["schemas"]["Syntax"];
        };
        TMMatch: {
            score: number;
            /** @enum {string} */
            kind: "context" | "exact" | "fuzzy";
            /** @description The unit's target in MF2, its variables renamed to the query's by position. */
            target: string;
            /** @description The same target in the syntax the lookup asked for (`target_syntax`, by default the query's `syntax`), written from its model by the MessageFormat kernel. When MF1 was asked for but can't express the target (markup, MF2-only options), this is its MF2 and `target_syntax_fallback` is true. */
            target_text: string;
            /** @description The syntax `target_text` is in. */
            target_syntax: components["schemas"]["Syntax"];
            /** @description True when `target_text` is MF2 because MF1 can't express the target. */
            target_syntax_fallback: boolean;
            target_model: components["schemas"]["MF2Message"];
            /** @description False when a target variable had no counterpart and kept its name. */
            variables_adapted: boolean;
            unit: components["schemas"]["TMUnit"];
        };
        TMLookupResult: {
            source_normalized: string;
            matches: components["schemas"]["TMMatch"][];
        };
        /** @description A match as a unit's workspace shows it: what was remembered and how well it fits, never which unit it is — there is no unit id, and `message_key` is left out for a member with visibility `assigned`. */
        UnitTMMatch: {
            score: number;
            /** @enum {string} */
            kind: "context" | "exact" | "fuzzy";
            /** @description The remembered source, normalized: what a fuzzy match is compared with. */
            source_normalized: string;
            /** @description The remembered target in MF2, its variables renamed to the unit's by position. */
            target: string;
            /** @description The same target in the syntax asked for; MF2 with `target_syntax_fallback` when MF1 can't express it. */
            target_text: string;
            target_syntax: components["schemas"]["Syntax"];
            target_syntax_fallback: boolean;
            /** @description False when a target variable had no counterpart and kept its name. */
            variables_adapted: boolean;
            /** @description True for a unit the project owns, false for a tenant-wide one. */
            project_scoped: boolean;
            /** @description The key of the message the match was learned from. Never returned to a member with visibility `assigned`. */
            message_key?: components["schemas"]["MessageKey"];
        };
        UnitTMMatches: {
            /** @description The unit's own source, normalized. */
            source_normalized: string;
            items: components["schemas"]["UnitTMMatch"][];
        };
        /** @description An AI suggestion as a unit's workspace shows it. The job, provider, model, calls, usage, cost and provenance of `AISuggestion` are not part of it. */
        UnitAISuggestion: {
            id: components["schemas"]["Id"];
            locale: components["schemas"]["Locale"];
            source_revision: number;
            /** @description The translation in canonical MF2 syntax. */
            message: string;
            findings: components["schemas"]["QAFinding"][];
            term_findings: components["schemas"]["AITermFinding"][];
            /** Format: double */
            score: number;
            explanation: components["schemas"]["AIConfidenceFactor"][];
            action: components["schemas"]["AIAction"];
            action_note?: string;
            risk_tags: string[];
            status: components["schemas"]["AISuggestionStatus"];
            /** @description The message's source has been revised since the suggestion was made. */
            outdated: boolean;
            /** @description Whether the caller may accept or reject it (`acceptAISuggestion`); false for a member with visibility `assigned`. */
            decidable: boolean;
            created_at: components["schemas"]["Timestamp"];
        };
        UnitAISuggestions: {
            items: components["schemas"]["UnitAISuggestion"][];
        };
        TMConcordanceMatch: {
            /** @description Trigram word similarity of the phrase to the side searched. */
            similarity: number;
            unit: components["schemas"]["TMUnit"];
        };
        TMConcordance: {
            matches: components["schemas"]["TMConcordanceMatch"][];
        };
        /** @enum {string} */
        TermStatus: "preferred" | "admitted" | "deprecated" | "forbidden";
        /** @enum {string} */
        PartOfSpeech: "noun" | "verb" | "adjective" | "adverb" | "proper_noun" | "phrase" | "other";
        Term: {
            id: components["schemas"]["Id"];
            locale: components["schemas"]["Locale"];
            text: string;
            status: components["schemas"]["TermStatus"];
            part_of_speech?: components["schemas"]["PartOfSpeech"];
            case_sensitive: boolean;
            note?: string;
        };
        TermInput: {
            locale: components["schemas"]["Locale"];
            text: string;
            status?: components["schemas"]["TermStatus"];
            part_of_speech?: components["schemas"]["PartOfSpeech"];
            /** @default false */
            case_sensitive: boolean;
            note?: string;
        };
        /** @description A termbase concept (intent §19.2) with its terms per locale. */
        TermConcept: {
            id: components["schemas"]["Id"];
            /** @description Absent for a tenant-wide concept. */
            project_id?: components["schemas"]["Id"];
            definition: string;
            domain: string;
            note: string;
            /** @description A product concept this concept stands for (opaque). */
            product_ref: string;
            terms: components["schemas"]["Term"][];
            version: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_by: string;
            updated_at: components["schemas"]["Timestamp"];
        };
        TermConceptList: {
            items: components["schemas"]["TermConcept"][];
            next_page_token?: string;
        };
        CreateTermConcept: {
            /** @description Scopes the concept to one project. */
            project_id?: components["schemas"]["Id"];
            definition?: string;
            domain?: string;
            note?: string;
            product_ref?: string;
            terms: components["schemas"]["TermInput"][];
        };
        ReplaceTermConcept: {
            definition?: string;
            domain?: string;
            note?: string;
            product_ref?: string;
            terms: components["schemas"]["TermInput"][];
        };
        /** @enum {string} */
        RevisionAction: "created" | "updated" | "deleted";
        TermConceptRevision: {
            version: number;
            action: components["schemas"]["RevisionAction"];
            author: string;
            created_at: components["schemas"]["Timestamp"];
            concept: components["schemas"]["TermConcept"];
        };
        TermConceptRevisionList: {
            items: components["schemas"]["TermConceptRevision"][];
            next_page_token?: string;
        };
        TermRecognitionRequest: {
            text: string;
            /** @description Parse `text` as a message in this syntax and recognize its visible text. */
            syntax?: components["schemas"]["Syntax"];
            locale: components["schemas"]["Locale"];
            target_locale?: components["schemas"]["Locale"];
            /** @description Adds the project's concepts to the tenant-wide ones. */
            project_id?: components["schemas"]["Id"];
        };
        TermHit: {
            concept_id: components["schemas"]["Id"];
            definition: string;
            term: components["schemas"]["Term"];
            /** @description Code point offset into `analyzed_text`. */
            start: number;
            /** @description Exclusive. */
            end: number;
            /** @description The words matched, as written. */
            text: string;
            /** @description The concept's terms in `target_locale`, allowed ones first. */
            targets?: components["schemas"]["Term"][];
        };
        TermRecognition: {
            analyzed_text: string;
            hits: components["schemas"]["TermHit"][];
        };
        TerminologyCheckRequest: {
            source: string;
            source_locale: components["schemas"]["Locale"];
            target: string;
            target_locale: components["schemas"]["Locale"];
            /** @description Parse both texts as messages in this syntax. */
            syntax?: components["schemas"]["Syntax"];
            project_id?: components["schemas"]["Id"];
        };
        TermFinding: {
            /** @enum {string} */
            code: "term_missing" | "term_forbidden";
            /** @enum {string} */
            severity: "error" | "warning";
            concept_id: components["schemas"]["Id"];
            term_id: components["schemas"]["Id"];
            /** @enum {string} */
            side: "source" | "target";
            /** @description Code point offset into `source_text` or `target_text`. */
            start: number;
            end: number;
            text: string;
            /** @description The concept's allowed target terms, preferred first. */
            suggestions: string[];
            /** @description For humans; wording may change. */
            message: string;
        };
        TerminologyCheck: {
            source_text: string;
            target_text: string;
            findings: components["schemas"]["TermFinding"][];
        };
        TranslationTerminologyFindings: {
            message_id: components["schemas"]["Id"];
            message_key: components["schemas"]["MessageKey"];
            namespace: string;
            locale: components["schemas"]["Locale"];
            state: components["schemas"]["ReviewState"];
            /** @description The message's current source revision: the one `source_text` is, and so the one these findings were computed against. A waiver on a terminology finding is measured against it (RFC 0005 §2.3) — change the source and the waived finding comes back. */
            source_revision: number;
            /** @description The source's visible text, which `source` spans point into. */
            source_text: string;
            /** @description The translation's visible text, which `target` spans point into. */
            target_text: string;
            findings: components["schemas"]["TermFinding"][];
        };
        ProjectTerminologyFindings: {
            /** @description The page's translations with findings, by message key and locale. */
            items: components["schemas"]["TranslationTerminologyFindings"][];
            /** @description The translations this page checked, by locale. */
            checked: {
                [key: string]: number;
            };
            next_page_token?: string;
        };
        StyleFormality: {
            /** @enum {string} */
            register?: "formal" | "informal" | "neutral";
            /**
             * @example Sie
             * @example du
             * @example vous
             */
            pronoun?: string;
        };
        StylePunctuation: {
            /** @example „“ */
            quotes?: string;
            nested_quotes?: string;
            /** @enum {string} */
            dash?: "hyphen" | "en" | "em";
            space_before_unit?: boolean;
            space_before_punctuation?: boolean;
            serial_comma?: boolean;
            ellipsis?: string;
        };
        StyleNumbers: {
            decimal_separator?: string;
            grouping_separator?: string;
            notes?: string;
        };
        StyleDates: {
            /** @description A CLDR date pattern. */
            format?: string;
            notes?: string;
        };
        /** @description Structured style. Every leaf is optional; unset inherits from a broader guide. */
        StyleFields: {
            formality?: components["schemas"]["StyleFormality"];
            /** @description Tone tags; a narrower guide's list replaces a broader one's. */
            tone?: string[];
            punctuation?: components["schemas"]["StylePunctuation"];
            numbers?: components["schemas"]["StyleNumbers"];
            dates?: components["schemas"]["StyleDates"];
        };
        StyleRule: {
            /** @description Identity across scopes. */
            id: string;
            /** @description Required unless `disabled`. */
            title?: string;
            rationale?: string;
            good?: string[];
            bad?: string[];
            /** @description Switches off a broader guide's rule with this id. */
            disabled?: boolean;
        };
        StyleGuide: {
            id: components["schemas"]["Id"];
            project_id?: components["schemas"]["Id"];
            locale?: components["schemas"]["Locale"];
            namespace?: components["schemas"]["Namespace"];
            name: string;
            fields: components["schemas"]["StyleFields"];
            rules: components["schemas"]["StyleRule"][];
            version: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_by: string;
            updated_at: components["schemas"]["Timestamp"];
        };
        StyleGuideList: {
            items: components["schemas"]["StyleGuide"][];
            next_page_token?: string;
        };
        CreateStyleGuide: {
            project_id?: components["schemas"]["Id"];
            locale?: components["schemas"]["Locale"];
            namespace?: components["schemas"]["Namespace"];
            name?: string;
            fields?: components["schemas"]["StyleFields"];
            rules?: components["schemas"]["StyleRule"][];
        };
        ReplaceStyleGuide: {
            name?: string;
            fields?: components["schemas"]["StyleFields"];
            rules?: components["schemas"]["StyleRule"][];
        };
        StyleGuideVersion: {
            version: number;
            action: components["schemas"]["RevisionAction"];
            author: string;
            created_at: components["schemas"]["Timestamp"];
            style_guide: components["schemas"]["StyleGuide"];
        };
        StyleGuideVersionList: {
            items: components["schemas"]["StyleGuideVersion"][];
            next_page_token?: string;
        };
        StyleGuideSource: {
            style_guide_id: components["schemas"]["Id"];
            version: number;
            project_id?: components["schemas"]["Id"];
            locale?: components["schemas"]["Locale"];
            namespace?: components["schemas"]["Namespace"];
        };
        EffectiveStyleGuide: {
            fields: components["schemas"]["StyleFields"];
            rules: components["schemas"]["StyleRule"][];
            /** @description The guide versions merged, broadest first. */
            sources: components["schemas"]["StyleGuideSource"][];
        };
        /**
         * Format: int64
         * @description Money in millionths of a US dollar (1 USD = 1000000).
         */
        MicroUSD: number;
        /**
         * @description The API a provider speaks: the Anthropic Messages API, an
         *     OpenAI-compatible `/chat/completions` endpoint (OpenAI, Mistral,
         *     self-hosted servers) or Gemini.
         * @enum {string}
         */
        AIProviderKind: "anthropic" | "openai_compatible" | "gemini";
        /** @description What routing policies route to, e.g. `anthropic` or `mistral-eu`. */
        AIProviderName: string;
        AIProvider: {
            id: components["schemas"]["Id"];
            name: components["schemas"]["AIProviderName"];
            kind: components["schemas"]["AIProviderKind"];
            /** @description The endpoint; absent for the kind's default. */
            base_url?: string;
            /** @description The model allow-list; empty allows any model. */
            models: string[];
            enabled: boolean;
            /** @description Whether a key is stored. The key itself is never returned. */
            api_key_set: boolean;
            version: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_by: string;
            updated_at: components["schemas"]["Timestamp"];
        };
        AIProviderList: {
            items: components["schemas"]["AIProvider"][];
            next_page_token?: string;
        };
        CreateAIProvider: {
            name: components["schemas"]["AIProviderName"];
            kind: components["schemas"]["AIProviderKind"];
            /** @description Required for `openai_compatible`; https only unless the deployment allows private endpoints. */
            base_url?: string;
            models?: string[];
            /** @default true */
            enabled: boolean;
            /** @description Sealed at rest; never returned. */
            api_key?: string;
        };
        UpdateAIProvider: {
            name?: components["schemas"]["AIProviderName"];
            base_url?: string;
            models?: string[];
            enabled?: boolean;
            /** @description Replaces the key. */
            api_key?: string;
            /** @description Removes the key. */
            clear_api_key?: boolean;
        };
        AISettings: {
            /**
             * @description The tenant's explicit permission to send text to AI providers
             *     (RFC 0003 §7). Off by default; without it only exact
             *     translation-memory matches are reused.
             */
            provider_consent: boolean;
            consent_changed_by?: string;
            consent_changed_at?: components["schemas"]["Timestamp"];
            /** @description The tenant's running jobs across replicas. */
            max_concurrent_jobs: number;
            /** @description Provider spend per calendar month (UTC); 0 allows none. */
            monthly_budget_micro_usd: components["schemas"]["MicroUSD"];
            /** @description 0 until saved. */
            version: number;
            updated_by?: string;
            updated_at?: components["schemas"]["Timestamp"];
        };
        UpdateAISettings: {
            provider_consent?: boolean;
            max_concurrent_jobs?: number;
            monthly_budget_micro_usd?: components["schemas"]["MicroUSD"];
        };
        AIPrice: {
            /**
             * Format: double
             * @description USD per million input tokens.
             */
            input_per_mtok: number;
            /** Format: double */
            output_per_mtok: number;
            /** Format: double */
            cache_read_per_mtok?: number;
            /** Format: double */
            cache_write_per_mtok?: number;
        };
        /** @description `<provider>/<model>` to its price. */
        AIPriceTable: {
            [key: string]: components["schemas"]["AIPrice"];
        };
        AIPrices: {
            defaults: components["schemas"]["AIPriceTable"];
            overrides: components["schemas"]["AIPriceTable"];
            effective: components["schemas"]["AIPriceTable"];
            /** @description The settings' version. */
            version: number;
        };
        PutAIPrices: {
            overrides: {
                [key: string]: components["schemas"]["AIPrice"];
            };
        };
        AIProviderSpend: {
            provider: string;
            model: string;
            cost_micro_usd: components["schemas"]["MicroUSD"];
            calls: number;
            /** Format: int64 */
            input_tokens: number;
            /** Format: int64 */
            output_tokens: number;
        };
        AIBudget: {
            monthly_budget_micro_usd: components["schemas"]["MicroUSD"];
            spent_micro_usd: components["schemas"]["MicroUSD"];
            remaining_micro_usd: components["schemas"]["MicroUSD"];
            calls: number;
            month_start: components["schemas"]["Timestamp"];
            by_provider: components["schemas"]["AIProviderSpend"][];
        };
        AIUsage: {
            /** Format: int64 */
            input_tokens: number;
            /** Format: int64 */
            output_tokens: number;
            /** Format: int64 */
            cache_read_tokens?: number;
            /** Format: int64 */
            cache_write_tokens?: number;
        };
        AISpendEntry: {
            id: components["schemas"]["Id"];
            job_id?: components["schemas"]["Id"];
            project_id?: components["schemas"]["Id"];
            task: components["schemas"]["AITask"];
            provider: string;
            model: string;
            usage: components["schemas"]["AIUsage"];
            cost_micro_usd: components["schemas"]["MicroUSD"];
            /** @description false when the price table has no price for the model (cost 0). */
            priced: boolean;
            occurred_at: components["schemas"]["Timestamp"];
        };
        AISpendList: {
            items: components["schemas"]["AISpendEntry"][];
            next_page_token?: string;
        };
        /** @enum {string} */
        AITask: "translate" | "review" | "explain" | "assess";
        AIRoute: {
            provider: components["schemas"]["AIProviderName"];
            model: string;
            max_tokens: number;
            /** Format: double */
            temperature?: number;
            /** @enum {string} */
            effort?: "low" | "medium" | "high" | "max";
        };
        AIRoutingRule: {
            task: components["schemas"]["AITask"];
            /** @description Target locales the rule applies to (a language covers its regions); empty applies to all. */
            locales?: components["schemas"]["Locale"][];
            /** @description The preferred route first, then fallbacks. */
            routes: components["schemas"]["AIRoute"][];
        };
        AIRoutingPolicy: {
            rules: components["schemas"]["AIRoutingRule"][];
        };
        AIRoutingPolicyView: {
            policy: components["schemas"]["AIRoutingPolicy"];
            /**
             * @description Where the policy in effect comes from.
             * @enum {string}
             */
            source: "default" | "tenant" | "project";
            /** @description The stored policy's version; 0 for the default. */
            version: number;
            updated_by?: string;
            updated_at?: components["schemas"]["Timestamp"];
        };
        /** @enum {string} */
        AINamespaceTag: "sensitive" | "legal" | "marketing";
        AIReviewPolicy: {
            /** @description Off by default. */
            auto_approve: boolean;
            /**
             * Format: double
             * @description Scores at or above it are `auto_approve` (when on).
             */
            auto_approve_min: number;
            /**
             * Format: double
             * @description Scores at or above it are `approve_recommended`; below, `review_required`.
             */
            recommend_min: number;
            /** @description Explanation factors that require review whatever the score (`missing_plural_categories` always does). */
            force_review?: string[];
            /** @description The environments auto-approved text ships to; each must ship approved translations. */
            auto_approve_environments?: components["schemas"]["EnvironmentName"][];
        };
        AIProjectSettings: {
            /** @description A namespace to its policy tags. */
            namespace_tags: {
                [key: string]: components["schemas"]["AINamespaceTag"][];
            };
            auto_translate_locales: components["schemas"]["Locale"][];
            review: components["schemas"]["AIReviewPolicy"];
            /** @description 0 until saved. */
            version: number;
            updated_by?: string;
            updated_at?: components["schemas"]["Timestamp"];
        };
        UpdateAIProjectSettings: {
            namespace_tags?: {
                [key: string]: components["schemas"]["AINamespaceTag"][];
            };
            auto_translate_locales?: components["schemas"]["Locale"][];
            review?: components["schemas"]["AIReviewPolicy"];
        };
        /**
         * @description Which messages a fill translates, by their translation's state in each locale: `missing` (none, or rejected), `outdated` (made against an older source revision) or either.
         * @enum {string}
         */
        AIFillSelect: "missing" | "outdated" | "missing_or_outdated";
        CreateAIFill: {
            locales: components["schemas"]["Locale"][];
            namespace?: components["schemas"]["Namespace"];
            key_prefix?: string;
            keys?: components["schemas"]["MessageKey"][];
            /**
             * @description The older spelling of `select: missing_or_outdated`.
             * @default false
             */
            include_outdated: boolean;
            /** @description Default `missing`; `missing_or_outdated` with `include_outdated` or listed `keys`. */
            select?: components["schemas"]["AIFillSelect"];
            /**
             * @description Translate the listed `keys` even where the translation is
             *     already current, instead of skipping them as
             *     `skipped.up_to_date`. Someone looking at a message in the
             *     in-product editor wants a second opinion on the text that is
             *     there, which is the one case a selection can't express. It
             *     needs `keys` (never a whole project), queues the job again
             *     when one exists for the same message, locale, source
             *     revision and knowledge, and is ignored by auto-translate.
             * @default false
             */
            force: boolean;
        };
        /** @enum {string} */
        AIJobState: "queued" | "running" | "succeeded" | "skipped" | "failed" | "dead" | "cancelled";
        /** @enum {string} */
        AITrigger: "message_created" | "translation_outdated" | "locale_added" | "fill";
        AIFill: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            /** @enum {string} */
            trigger: "fill" | "locale_added";
            locales: components["schemas"]["Locale"][];
            namespace?: string;
            key_prefix?: string;
            keys?: string[];
            include_outdated?: boolean;
            /** @description The effective selection. */
            select: components["schemas"]["AIFillSelect"];
            /** @description Jobs queued, new or queued again. */
            jobs_created: number;
            /** @description Jobs that already existed for the same message, locale, source revision and knowledge. */
            jobs_existing: number;
            /**
             * @description The jobs this fill queued or reused, so a client can follow
             *     them with `GET …/ai-jobs/{ai_job}` instead of watching the
             *     whole list. Present only on the fill's own response, and
             *     only for a small fill (at most 200 jobs); a bigger one is
             *     followed with `GET …/ai-jobs?fill=<id>`.
             */
            job_ids?: components["schemas"]["Id"][];
            /** @description Messages left out, by reason: `sensitive`, `up_to_date`, `not_selected`, `limit`. */
            skipped: {
                [key: string]: number;
            };
            /** @description The fill's jobs by state. */
            job_states: {
                [key: string]: number;
            };
            /** @description `provider_consent_off`, `no_budget`, `no_provider`. */
            warnings: string[];
            requested_by: string;
            created_at: components["schemas"]["Timestamp"];
        };
        AICostEstimate: {
            estimated_micro_usd: components["schemas"]["MicroUSD"];
            max_micro_usd: components["schemas"]["MicroUSD"];
            /** @description A route's model has no price; its calls count as 0. */
            unpriced: boolean;
        };
        AIFillPreviewLocale: {
            locale: components["schemas"]["Locale"];
            /** @description The messages a fill would queue (or reuse) a job for, in key order. */
            keys: components["schemas"]["MessageKey"][];
            /** @description Jobs that exist and would be reused, not run again. */
            existing: number;
            /** @description Messages an exact translation-memory match covers: no provider call. */
            tm_exact: number;
            /** @description Messages that would call a provider. */
            provider: number;
            /** @description Messages that would not reach a provider, by reason: `sensitive` (never queued), `provider_consent`, `no_route`, `budget_exceeded` (queued, then failing). */
            refused: {
                [key: string]: number;
            };
            /** @description Messages left out, by reason: `up_to_date`, `not_selected`, `limit`. */
            skipped: {
                [key: string]: number;
            };
            cost: components["schemas"]["AICostEstimate"];
        };
        AIFillPreview: {
            project_id: components["schemas"]["Id"];
            /** @description The effective selection. */
            select: components["schemas"]["AIFillSelect"];
            locales: components["schemas"]["AIFillPreviewLocale"][];
            /** @description As a fill's: `provider_consent_off`, `no_budget`, `no_provider`. */
            warnings: string[];
            cost: components["schemas"]["AICostEstimate"];
        };
        AIAuditEntry: {
            /** @description `tm_lookup`, `term_lookup`, `style_rules`, `message_context`, `validate`, `draft`, `assess`. */
            tool: string;
            /** @description The tool's result, as recorded. */
            output: unknown;
            at: components["schemas"]["Timestamp"];
        };
        AIJob: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            message_id: components["schemas"]["Id"];
            message_key: components["schemas"]["MessageKey"];
            namespace: string;
            locale: components["schemas"]["Locale"];
            source_revision: number;
            /** @description Digest of the prompts, style guides and termbase the job was queued against. */
            knowledge_fingerprint: string;
            trigger: components["schemas"]["AITrigger"];
            fill_id?: components["schemas"]["Id"];
            state: components["schemas"]["AIJobState"];
            attempts: number;
            max_attempts: number;
            available_at: components["schemas"]["Timestamp"];
            /** @description Why it failed, was skipped or died (see the listing). */
            failure_code?: string;
            last_error?: string;
            suggestion_id?: components["schemas"]["Id"];
            audit?: components["schemas"]["AIAuditEntry"][];
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            started_at?: components["schemas"]["Timestamp"];
            finished_at?: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
        };
        AIJobList: {
            items: components["schemas"]["AIJob"][];
            next_page_token?: string;
        };
        /** @enum {string} */
        AIAction: "auto_approve" | "approve_recommended" | "review_required";
        /** @enum {string} */
        AISuggestionStatus: "pending" | "accepted" | "rejected" | "auto_applied" | "superseded";
        AIConfidenceFactor: {
            /** @description `origin`, `tm_match`, `term_forbidden`, `term_missing`, `repairs`, `qa_warnings`, `self_assessment`, `formality`, `max_length`, `length_ratio`, `risk_tag`, `markup_density`, `missing_plural_categories`. */
            factor: string;
            /** Format: double */
            value: number;
            /**
             * Format: double
             * @description How much it moved the score (negative lowers it).
             */
            contribution: number;
            reason: string;
        };
        AITermFinding: {
            /** @enum {string} */
            code: "term_missing" | "term_forbidden";
            concept_id: string;
            term_id?: string;
            term: string;
            message: string;
        };
        AIProvenance: {
            /** @enum {string} */
            origin: "ai" | "translation_memory";
            provider?: string;
            model?: string;
            prompt_version?: string;
            tm_unit_ids?: string[];
            term_ids?: string[];
            style_version?: string;
            repairs: number;
        };
        AICall: {
            task: components["schemas"]["AITask"];
            provider: string;
            model: string;
            prompt_version: string;
            usage: components["schemas"]["AIUsage"];
            cost_micro_usd: components["schemas"]["MicroUSD"];
        };
        AIEditDiff: {
            /** @description Character edit distance of the visible text. */
            distance: number;
            /**
             * Format: double
             * @description The distance over the longer text's length.
             */
            ratio: number;
            terms_added?: string[];
            terms_removed?: string[];
            /** @description Style fields whose use changed: `quotes`, `dash`, `ellipsis`, `space_before_punctuation`, `pronoun`. */
            style_fields?: string[];
        };
        AIDecision: {
            edit?: components["schemas"]["AIEditDiff"];
            reason?: string;
        };
        AISuggestion: {
            id: components["schemas"]["Id"];
            job_id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            message_id: components["schemas"]["Id"];
            message_key: components["schemas"]["MessageKey"];
            namespace: string;
            locale: components["schemas"]["Locale"];
            source_revision: number;
            /** @description The translation in canonical MF2 syntax. */
            message: string;
            model: components["schemas"]["MF2Message"];
            /** @description Structural warnings (errors never reach a suggestion). */
            findings: components["schemas"]["QAFinding"][];
            term_findings: components["schemas"]["AITermFinding"][];
            provenance: components["schemas"]["AIProvenance"];
            /** Format: double */
            score: number;
            explanation: components["schemas"]["AIConfidenceFactor"][];
            action: components["schemas"]["AIAction"];
            /** @description Why the routed action differs from what the bands alone say. */
            action_note?: string;
            risk_tags: string[];
            calls: components["schemas"]["AICall"][];
            usage: components["schemas"]["AIUsage"];
            cost_micro_usd: components["schemas"]["MicroUSD"];
            status: components["schemas"]["AISuggestionStatus"];
            /** @description The revision it became, once accepted or auto-applied. */
            translation_revision?: number;
            decided_by?: string;
            decided_at?: components["schemas"]["Timestamp"];
            decision?: components["schemas"]["AIDecision"];
            version: number;
            created_at: components["schemas"]["Timestamp"];
            source?: components["schemas"]["AISuggestionSource"];
        };
        /** @description The suggestion's message as it is now (omitted once the message no longer exists): what a reviewer compares the suggestion with. `message_key` and `namespace` are current (a rename shows here, not in the suggestion's own); the suggestion is outdated when its `source_revision` is older than this one. */
        AISuggestionSource: {
            message_key: components["schemas"]["MessageKey"];
            namespace: string;
            /** @enum {string} */
            state: "active" | "obsolete";
            source_revision: number;
            /** @description The source as authored. */
            text: string;
            syntax: components["schemas"]["Syntax"];
            /** @description The source in canonical MF2 syntax. */
            mf2: string;
            model: components["schemas"]["MF2Message"];
        };
        AISuggestionList: {
            items: components["schemas"]["AISuggestion"][];
            next_page_token?: string;
        };
        AcceptAISuggestion: {
            /** @description An edit to accept instead of the suggestion. */
            text?: string;
            syntax?: components["schemas"]["Syntax"];
            /**
             * @description Where the person was looking when they accepted. Recorded
             *     as `origin_detail.in_context` on the revision, beside what
             *     the suggestion itself contributed, so history can say the
             *     edit was made in the running product and on which route
             *     (RFC 0004 §5.3). Only the in-product editor sends it.
             */
            in_context?: components["schemas"]["InContextProvenance"];
        };
        /**
         * @description An edit made in the running product: the route pattern the page
         *     was on and the viewport it was seen at.
         */
        InContextProvenance: {
            /** @description The route pattern, not the URL: `/orders/:id`, never `/orders/8123`. */
            route: string;
            viewport?: {
                width: number;
                height: number;
            };
        };
        RejectAISuggestion: {
            reason?: string;
        };
        AISentMessage: {
            /** @enum {string} */
            role: "user" | "assistant";
            text: string;
        };
        AIDisclosure: {
            id: components["schemas"]["Id"];
            job_id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            message_id: components["schemas"]["Id"];
            locale: components["schemas"]["Locale"];
            task: components["schemas"]["AITask"];
            provider: string;
            model: string;
            /** @description Identifies the (versioned, data-free) system prompt. */
            system_sha256: string;
            /** @description Exactly what the provider received. */
            sent: components["schemas"]["AISentMessage"][];
            occurred_at: components["schemas"]["Timestamp"];
        };
        AIDisclosureList: {
            items: components["schemas"]["AIDisclosure"][];
            next_page_token?: string;
        };
        AILocaleMetrics: {
            locale: components["schemas"]["Locale"];
            accepted: number;
            /** @description Accepted after an edit. */
            edited: number;
            rejected: number;
            /**
             * Format: double
             * @description accepted / (accepted + rejected).
             */
            acceptance_rate: number;
            /** Format: double */
            mean_edit_distance: number;
            /** Format: double */
            mean_edit_ratio: number;
        };
        AIMetrics: {
            since: components["schemas"]["Timestamp"];
            locales: components["schemas"]["AILocaleMetrics"][];
        };
        AIEvalMetrics: {
            cases: number;
            /** Format: double */
            structural_pass_rate: number;
            /** Format: double */
            terminology_compliance: number;
            /** Format: double */
            formality_compliance: number;
            /** Format: double */
            mean_edit_ratio: number;
            /** Format: double */
            origin_accuracy: number;
        };
        AIEvalBaseline: {
            /** @description A locale pair (`en-de`) or `all` to its tracked metrics. */
            pairs: {
                [key: string]: components["schemas"]["AIEvalMetrics"];
            };
        };
        /**
         * @description `xliff` XLIFF 2.1, `json` flat or nested `{key: message}`, `po`
         *     gettext (import only), `tmx` TMX 1.4b, `tbx` TBX-Basic.
         * @enum {string}
         */
        IntegrationFormat: "xliff" | "json" | "po" | "tmx" | "tbx";
        /**
         * @description What the job moves; follows from the format.
         * @enum {string}
         */
        IntegrationKind: "catalog" | "tm" | "termbase";
        /** @enum {string} */
        ImportMode: "dry_run" | "merge" | "overwrite";
        /** @enum {string} */
        IntegrationJobState: "awaiting_upload" | "queued" | "running" | "succeeded" | "failed" | "cancelled";
        /**
         * @description How the file is read; options a format doesn't take are refused
         *     (`invalid_options`).
         */
        ImportOptions: {
            /**
             * @description JSON: the file's locale (default the project's source
             *     locale, making it a source catalog). PO: the translations'
             *     locale (default the file's `Language` header). XLIFF: the
             *     translations' locale (default the file's `trgLang`) — it
             *     names the locale of a file without `trgLang`, or imports a
             *     file as another locale than it names (`de` into `de-AT`).
             *     It must be one of the project's locales (a target locale;
             *     for JSON also the source locale): `locale_not_found` (404)
             *     otherwise.
             */
            locale?: components["schemas"]["Locale"];
            /** @description JSON and PO: every message's namespace (default `default`). */
            namespace?: string;
            /**
             * @description JSON: the messages' syntax (default mf1). XLIFF (`mf1` only):
             *     read units from other tools that carry plain text as ICU
             *     MessageFormat instead of literally.
             */
            syntax?: components["schemas"]["Syntax"];
            /**
             * @description The review state of translations the file doesn't state:
             *     JSON (default needs_review), PO entries without the fuzzy
             *     flag (default approved). Capped like the file's own states.
             */
            state?: components["schemas"]["ReviewState"];
            /** @description PO: the count variable of plurals (default `count`). */
            plural_variable?: string;
        };
        ImportJobRequest: {
            /** @description Required for catalog formats; without it TMX and TBX import tenant-wide. */
            project_id?: components["schemas"]["Id"];
            format: components["schemas"]["IntegrationFormat"];
            mode?: components["schemas"]["ImportMode"];
            /** @description The file's name, for people. */
            file_name?: string;
            options?: components["schemas"]["ImportOptions"];
        };
        IntegrationFile: {
            /** Format: int64 */
            size: number;
            sha256: string;
            content_type: string;
        };
        ImportCounts: {
            created: number;
            updated: number;
            unchanged: number;
            conflict: number;
            invalid: number;
        };
        ImportSummary: {
            created: number;
            updated: number;
            unchanged: number;
            conflict: number;
            invalid: number;
            /** @description The same counts per kind of result (`message`, `translation`, `tm_unit`, `concept`). */
            by_kind: {
                [key: string]: components["schemas"]["ImportCounts"];
            };
        };
        ImportJob: {
            id: components["schemas"]["Id"];
            project_id?: components["schemas"]["Id"];
            kind: components["schemas"]["IntegrationKind"];
            format: components["schemas"]["IntegrationFormat"];
            mode: components["schemas"]["ImportMode"];
            options: components["schemas"]["ImportOptions"];
            state: components["schemas"]["IntegrationJobState"];
            file_name: string;
            /** @description Where to `PUT` the file; present while the job is `awaiting_upload`. */
            upload_url?: string;
            file?: components["schemas"]["IntegrationFile"];
            /** @description The earlier import of the same file and options whose result this job reuses. */
            reused_job_id?: components["schemas"]["Id"];
            summary: components["schemas"]["ImportSummary"];
            /** @description Entries, units or concepts in the file; 0 until known. */
            total_items: number;
            processed_items: number;
            failure_code?: string;
            failure_message?: string;
            cancel_requested: boolean;
            attempts: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            started_at?: components["schemas"]["Timestamp"];
            finished_at?: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            /** @description When retention deletes the uploaded file (the job and its results stay). */
            expires_at: components["schemas"]["Timestamp"];
            files_deleted_at?: components["schemas"]["Timestamp"];
        };
        ImportJobList: {
            items: components["schemas"]["ImportJob"][];
            next_page_token?: string;
        };
        /** @enum {string} */
        ImportResultKind: "message" | "translation" | "tm_unit" | "concept";
        /** @enum {string} */
        ImportResultStatus: "created" | "updated" | "unchanged" | "conflict" | "invalid";
        ImportResult: {
            /** @description Position in the job's results (file order). */
            seq: number;
            kind: components["schemas"]["ImportResultKind"];
            /** @description The message key, the TMX unit's `tuid` or the TBX concept's id. */
            key: string;
            locale?: components["schemas"]["Locale"];
            status: components["schemas"]["ImportResultStatus"];
            /** @description Why a result is a conflict or invalid. */
            code?: string;
            detail?: string;
            /**
             * @description Where the item is in the file (1-based): the XLIFF `<unit>` or
             *     `<target>`, the JSON member, the PO `msgid`/`msgctxt` or
             *     `msgstr`, the TMX `<tuv>`, the TBX concept entry — for every
             *     item of a file, not only the problem that fails it.
             */
            line?: number;
            /** @description The byte column on that line (1-based). */
            column?: number;
            /**
             * @description The item in the format's own terms: an XLIFF 2 fragment
             *     identifier (`#/f=checkout/u=pay`), a JSON pointer
             *     (`/checkout/pay`), a PO entry's `msgctxt` and `msgid`
             *     (`msgctxt "menu" msgid "Open"`), a TMX `tu[12]` or TBX
             *     `conceptEntry[3]` by its place in the file.
             */
            ref?: string;
        };
        ImportResultList: {
            items: components["schemas"]["ImportResult"][];
            next_page_token?: string;
        };
        /**
         * @description What is written; options a format doesn't take are refused
         *     (`invalid_options`).
         */
        ExportOptions: {
            /**
             * @description Catalogs: one file per locale (XLIFF targets; JSON any locale,
             *     default the source). TMX: keep units with these targets.
             */
            locales?: components["schemas"]["Locale"][];
            /** @description TMX: keep units with this source locale. */
            source_locale?: components["schemas"]["Locale"];
            /** @description Catalogs: only these namespaces (default all). */
            namespaces?: string[];
            /** @description Catalogs: translations in these review states (default approved). */
            states?: components["schemas"]["ReviewState"][];
            /**
             * @description JSON: default flat.
             * @enum {string}
             */
            layout?: "flat" | "nested";
            syntax?: components["schemas"]["Syntax"];
        };
        ExportJobRequest: {
            /** @description Required for catalogs; without it TMX and TBX export the whole tenant's. */
            project_id?: components["schemas"]["Id"];
            format: components["schemas"]["IntegrationFormat"];
            options?: components["schemas"]["ExportOptions"];
        };
        ExportJob: {
            id: components["schemas"]["Id"];
            project_id?: components["schemas"]["Id"];
            kind: components["schemas"]["IntegrationKind"];
            format: components["schemas"]["IntegrationFormat"];
            options: components["schemas"]["ExportOptions"];
            state: components["schemas"]["IntegrationJobState"];
            /** @description The file's name once written (`shop.de.xlf`, `shop.json.zip`). */
            file_name: string;
            file?: components["schemas"]["IntegrationFile"];
            /** @description Where to download the file; present while it is kept. */
            download_url?: string;
            /** @description Messages, units or concepts written. */
            written: number;
            failure_code?: string;
            failure_message?: string;
            cancel_requested: boolean;
            attempts: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            started_at?: components["schemas"]["Timestamp"];
            finished_at?: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            /** @description When retention deletes the file. */
            expires_at: components["schemas"]["Timestamp"];
            files_deleted_at?: components["schemas"]["Timestamp"];
        };
        ExportJobList: {
            items: components["schemas"]["ExportJob"][];
            next_page_token?: string;
        };
        /** @description A tenant-wide TMX or TBX import; the route says which. */
        KnowledgeImportJobRequest: {
            mode?: components["schemas"]["ImportMode"];
            /** @description The file's name, for people. */
            file_name?: string;
        };
        /** @description A tenant-wide TMX or TBX export; the route says which. */
        KnowledgeExportJobRequest: {
            /** @description TMX: `locales` (targets) and `source_locale`. TBX takes none. */
            options?: components["schemas"]["ExportOptions"];
        };
        WebhookAck: {
            /**
             * @description `false` when the delivery id was already in the inbox. The
             *     answer is `202` either way: a duplicate is a no-op.
             */
            accepted: boolean;
        };
        GitHubInstallIntent: {
            /**
             * @description The single-use value to carry through GitHub and hand back
             *     on the callback. Already on `install_url`.
             */
            state: string;
            /** @description Where to send the person to authorize the App. */
            install_url: string;
            expires_at: components["schemas"]["Timestamp"];
        };
        GitHubInstallCallback: {
            /** @description The `state` GitHub handed back. */
            state: string;
            /**
             * Format: int64
             * @description GitHub's installation number.
             */
            installation_id: number;
            /**
             * @description GitHub's one-time authorization code. It is redeemed for the
             *     person's own token, used once to check that they can see the
             *     installation, and never stored.
             */
            code: string;
            /**
             * @description GitHub's `setup_action`. `install` means an installation was
             *     made; `request` means the person could only ask their
             *     organization for one, and there is nothing to claim yet.
             */
            setup_action?: string;
        };
        /**
         * @description `active`; `suspended` when the account suspended the App;
         *     `revoked` when it was uninstalled on GitHub, which leaves the
         *     row so Studio can say what happened.
         * @enum {string}
         */
        GitHubInstallationState: "active" | "suspended" | "revoked";
        GitHubRepository: {
            /**
             * Format: int64
             * @description GitHub's numeric id, which a rename does not change.
             */
            repository_id: number;
            name: string;
            /** @description `owner/name`, a label only. */
            full_name: string;
            private: boolean;
            default_branch: string;
        };
        GitHubInstallation: {
            id: components["schemas"]["Id"];
            /**
             * Format: int64
             * @description GitHub's installation number.
             */
            installation_id: number;
            account_login: string;
            /** @enum {string} */
            account_type: "User" | "Organization";
            state: components["schemas"]["GitHubInstallationState"];
            connected_by?: string;
            connected_at: components["schemas"]["Timestamp"];
            /** @description The repositories the App can see through this installation. */
            repositories?: components["schemas"]["GitHubRepository"][];
            /**
             * @description GitHub could not be reached for this installation, so
             *     `repositories` is empty and says nothing about what it
             *     covers. It is also `true` for a suspended or revoked
             *     installation, whose repositories GitHub would refuse.
             */
            repositories_unavailable: boolean;
        };
        GitHubInstallationList: {
            items: components["schemas"]["GitHubInstallation"][];
            next_page_token?: string;
        };
        GitConnection: {
            id: components["schemas"]["Id"];
            /** @description Glossa's installation `id`, not GitHub's number. */
            installation_id: components["schemas"]["Id"];
            /**
             * Format: int64
             * @description GitHub's numeric repository id.
             */
            repository_id: number;
            /** @description `owner/name` as GitHub last reported it; a label, never a key. */
            repository_name: string;
            project_id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            default_branch: string;
            /**
             * @description The monorepo subdirectory this connection covers, without
             *     leading or trailing slashes. Empty is the whole repository.
             */
            path: string;
            created_by?: string;
            created_at: components["schemas"]["Timestamp"];
            updated_at?: components["schemas"]["Timestamp"];
            /** @description The `If-Match` value for a change. A connection has no history, so it is always `0`. */
            version: number;
        };
        GitConnectionList: {
            items: components["schemas"]["GitConnection"][];
            next_page_token?: string;
        };
        GitConnectionRequest: {
            /** @description Glossa's installation `id`. */
            installation_id: components["schemas"]["Id"];
            /**
             * Format: int64
             * @description GitHub's numeric repository id, from the installation's `repositories`.
             */
            repository_id: number;
            project_id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            /** @description Left out, GitHub's default branch for the repository is used. */
            default_branch?: string;
            /**
             * @description A monorepo subdirectory (`apps/web`). Left out, the
             *     connection covers the whole repository. One repository can
             *     feed several projects, one per path.
             */
            path?: string;
        };
        GitConnectionChange: {
            project_id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            default_branch: string;
            path?: string;
        };
        /** @enum {string} */
        ContextSource: "plugin" | "extract" | "runtime" | "capture";
        /**
         * @description The call shape: `t` (t()/$t(), Go's T), `component` (<GlossaText
         *     id>, <T id>), `element` (<glossa-text key> and its siblings),
         *     `accessor` (typed accessors from `glossa generate`), `template`
         *     ({{t}}, {{td}}, {{th}} in Go templates).
         * @enum {string}
         */
        UsageKind: "t" | "component" | "element" | "accessor" | "template";
        UsagesTool: {
            /** @description A package name: `@klarlabs-studio/glossa/unplugin`, `glossa`. */
            name: string;
            /** @description A semantic version. */
            version: string;
        };
        /**
         * @description A `glossa.usages/v1` document (RFC 0004 §2.2): one build, one
         *     application at one commit. Its JSON Schema,
         *     `runtimes/testdata/schemas/usages.v1.schema.json`, has every rule;
         *     the ones below are the shape.
         */
        UsagesDocument: {
            /** @enum {string} */
            schema: "glossa.usages/v1";
            /** @description The application's slug in the project. */
            application: string;
            /** @description The full commit ID in lowercase hex (SHA-1 or SHA-256). */
            commit: string;
            /** @description The short branch name (`feat/checkout-copy`). */
            branch: string;
            tool: components["schemas"]["UsagesTool"];
            usages: components["schemas"]["UsagesDocumentUsage"][];
        };
        UsagesDocumentUsage: {
            key: components["schemas"]["MessageKey"];
            /** @description A path relative to the project root, with `/` and no `.` or `..` segments. */
            file: string;
            line: number;
            /** @description 1-based, in Unicode code points. */
            column: number;
            component?: string;
            /** @description A route pattern: `/checkout/[step]`. */
            route?: string;
            kind: components["schemas"]["UsageKind"];
        };
        /** @description One upload of usages for one application at one commit. */
        ContextBuild: {
            id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            commit: string;
            branch: string;
            /** @description The branch was the project's default branch at upload. */
            on_default_branch: boolean;
            source: components["schemas"]["ContextSource"];
            tool: components["schemas"]["UsagesTool"];
            /** @description SHA-256 (hex) of the document's RFC 8785 canonical form. */
            digest: string;
            usages: number;
            /** @description Usages whose key the catalog didn't know at upload. */
            unknown_keys: number;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
        };
        ContextBuildList: {
            items: components["schemas"]["ContextBuild"][];
            next_page_token?: string;
        };
        /** @description Where a message's key is used, in a build. */
        ContextUsage: {
            key: components["schemas"]["MessageKey"];
            /** @description The message the key named at upload; absent for an unknown key. */
            message_id?: components["schemas"]["Id"];
            file: string;
            line: number;
            column?: number;
            component?: string;
            route?: string;
            kind: components["schemas"]["UsageKind"];
            build_id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            commit: string;
            branch: string;
            on_default_branch: boolean;
            source: components["schemas"]["ContextSource"];
        };
        ContextUsageList: {
            items: components["schemas"]["ContextUsage"][];
            next_page_token?: string;
        };
        MessageUsages: {
            message_id: components["schemas"]["Id"];
            key: components["schemas"]["MessageKey"];
            usages: components["schemas"]["ContextUsage"][];
            /** @description More usages exist than `limit`. */
            truncated: boolean;
        };
        UnusedMessage: {
            id: components["schemas"]["Id"];
            key: components["schemas"]["MessageKey"];
        };
        UnusedMessageList: {
            items: components["schemas"]["UnusedMessage"][];
            next_page_token?: string;
            /** @description The builds considered; 0 means nothing was uploaded yet. */
            current_builds: number;
            active_messages: number;
            unused_messages: number;
        };
        /**
         * @description A `glossa.captures/v1` manifest (RFC 0004 §3.1–§3.3), the
         *     `manifest` part of a capture upload: one application at one
         *     commit, one capture per route, viewport and locale. Its JSON
         *     Schema, `runtimes/testdata/schemas/captures.v1.schema.json`, has
         *     every rule and is the published contract, so its member names
         *     are kept as they are (`deviceScaleFactor`).
         */
        CapturesManifest: {
            /** @enum {string} */
            schema: "glossa.captures/v1";
            /** @description The application's slug in the project. */
            application: string;
            /** @description The full commit ID in lowercase hex (SHA-1 or SHA-256). */
            commit: string;
            /** @description The short branch name. */
            branch: string;
            tool: components["schemas"]["UsagesTool"];
            captures: components["schemas"]["CapturesManifestCapture"][];
        };
        CapturesManifestCapture: {
            /** @description The route pattern from the capture plan: `/checkout/[step]`. */
            route: string;
            /** @description The concrete URL navigated to; informational, never fetched. */
            url: string;
            viewport: {
                width: number;
                height: number;
                /** @description Defaults to 1. */
                deviceScaleFactor?: number;
            };
            /** @description The locale the page was rendered in (BCP 47). */
            locale: string;
            image: {
                /** @description Lowercase hex SHA-256 of the uploaded PNG; the name of its part. */
                sha256: string;
                width: number;
                height: number;
            };
            /** @description The session's render log; every region `index` names one entry. */
            renders: components["schemas"]["CapturesManifestRender"][];
            regions: components["schemas"]["CapturesManifestRegion"][];
            /**
             * @description What the visual probe pass found on this capture
             *     (RFC 0005 §5), measured live in the page while it was open.
             *     Optional: a capture taken without probes carries none, and
             *     an empty list means the probes ran and found nothing.
             */
            findings?: components["schemas"]["CapturesManifestFinding"][];
        };
        /**
         * @description One `glossa.finding/v1` finding as the page can write it. Two
         *     members of that shape are deliberately absent, because the page
         *     cannot know them and a guess would be worse than a gap: the
         *     `fingerprint`, which hashes the catalog message ID a browser
         *     never has — one minted in the page would not be the one the
         *     server computes, and waivers against it would silently stop
         *     applying — and `locus.capture`, which the server mints. The
         *     ingest completes both.
         */
        CapturesManifestFinding: {
            /** @enum {string} */
            schema: "glossa.finding/v1";
            /**
             * @description A capture's findings are the visual layer's.
             * @enum {string}
             */
            layer: "visual";
            code: string;
            /**
             * @description Always `warning`. A visual finding becomes eligible for
             *     `error` only when the same fingerprint appears in two
             *     consecutive captures (RFC 0005 §5.2), which only the server
             *     can see, and a page may not grade itself.
             * @enum {string}
             */
            severity: "warning";
            /** @description As much of the finding's locus as the page knows. */
            locus: {
                key?: components["schemas"]["MessageKey"];
                locale?: components["schemas"]["Locale"];
                /**
                 * @description `r_<index into this capture's `regions`>`. The ingest
                 *     pairs it with the capture it is on; a region this
                 *     capture doesn't have is refused.
                 */
                region?: string;
            };
            message: string;
            subject?: string;
            /** @description What the probe measured, free-form per code; at most 4096 bytes of JSON. */
            evidence?: Record<string, never>;
        };
        CapturesManifestRender: {
            index: number;
            key: components["schemas"]["MessageKey"];
            /** @description The locale the message resolved from. */
            locale: string;
        };
        /** @description Where a rendered message is, by `key` (a component's host element) or by `index` into `renders` (a marked t() string); exactly one of them. */
        CapturesManifestRegion: {
            key?: components["schemas"]["MessageKey"];
            index?: number;
            kind: components["schemas"]["CaptureRegionKind"];
            /** @description For kind `attribute`: the attribute's name (`placeholder`, `title`, `aria-label`). */
            attribute?: string;
            /** @description CSS pixels from the page's top left, fractional as measured. */
            box: {
                x: number;
                y: number;
                width: number;
                height: number;
            };
            /** @description false when the message rendered zero-size or off-screen. */
            visible: boolean;
        };
        /**
         * @description How the box was found: `element` (a component-rendered message:
         *     its host element), `text` (a marked t() string: its text's
         *     rects), `attribute` (a message in an attribute: its element).
         * @enum {string}
         */
        CaptureRegionKind: "element" | "text" | "attribute";
        /** @description What a capture upload stored. */
        CaptureUpload: {
            build: components["schemas"]["ContextBuild"];
            /** @description The build's captures. */
            captures: number;
            /** @description Images written to storage (0 on a replay). */
            images_stored: number;
            /** @description Images whose pixels the project had stored already (0 on a replay). */
            images_deduplicated: number;
            /** @description The region keys the catalog doesn't know, in order (stored without a message). */
            unknown_keys: string[];
            /**
             * @description The visual findings the upload's captures carried that were
             *     stored (RFC 0005 §5). 0 on a replay, when the manifest
             *     carried none, and when the project's check policy switched
             *     the `visual` layer off — `off` means the project does not
             *     compute a layer, so it does not pay to store it either.
             *     Read them back with `listCaptureFindings`.
             */
            findings: number;
        };
        CaptureViewport: {
            /** @description CSS pixels. */
            width: number;
            /** @description CSS pixels. */
            height: number;
        };
        CaptureImage: {
            /** @description SHA-256 (hex) of the stored (re-encoded) PNG; its `ETag`. */
            digest: string;
            /** @description Pixels. */
            width: number;
            /** @description Pixels. */
            height: number;
            /** @description The image's API path (`getCaptureImage`), relative to the server. */
            url: string;
        };
        /** @description Whole CSS pixels from the page's top left (covering the measured box); off-screen boxes may be negative. */
        CaptureBox: {
            x: number;
            y: number;
            width: number;
            height: number;
        };
        CaptureRegion: {
            kind: components["schemas"]["CaptureRegionKind"];
            box: components["schemas"]["CaptureBox"];
            /** @description false when the message rendered zero-size or off-screen. */
            visible: boolean;
        };
        /** @description A capture that shows a message, with the message's regions on it. */
        MessageCapture: {
            id: components["schemas"]["Id"];
            build_id: components["schemas"]["Id"];
            application_id: components["schemas"]["Id"];
            commit: string;
            branch: string;
            on_default_branch: boolean;
            route: string;
            viewport: components["schemas"]["CaptureViewport"];
            locale: string;
            image: components["schemas"]["CaptureImage"];
            regions: components["schemas"]["CaptureRegion"][];
            created_at: components["schemas"]["Timestamp"];
        };
        MessageCaptures: {
            message_id: components["schemas"]["Id"];
            key: components["schemas"]["MessageKey"];
            captures: components["schemas"]["MessageCapture"][];
            /** @description More captures exist than `limit`. */
            truncated: boolean;
        };
        /**
         * @description Identifies a finding across re-runs and across surfaces: the
         *     SHA-256 of its layer, its code, the message it is about, the
         *     locale and the normalized subject — and of nothing else, so a
         *     reformatted file, a new source revision or a reworded
         *     explanation never re-opens a waiver.
         * @example f_7c1a3e9b40d2f815
         */
        Fingerprint: string;
        /**
         * @description Which layer found it (RFC 0005 §3). Selectable by name in the policy and on the command line.
         * @enum {string}
         */
        FindingLayer: "structure" | "parity" | "completeness" | "terminology" | "style" | "length" | "locale" | "source" | "visual" | "linguistic";
        /**
         * @description `error` or `warning` as the layer emitted it, plus `waived` — a
         *     rendering of a finding and not a third rank a layer may emit. A
         *     waived finding is still computed, still reported and counted on
         *     its own, and can never fail a check.
         * @enum {string}
         */
        FindingSeverity: "error" | "warning" | "waived";
        /** @description The offending words in the source or the target, in bytes, so a surface can underline them. */
        FindingSpan: {
            /** @enum {string} */
            side: "source" | "target";
            start: number;
            end: number;
        };
        /**
         * @description Everything that locates a finding. Every field is optional and
         *     every field means the same thing in every layer. The catalog
         *     fields come from the layer; `file`, `line`, `column`, `route`,
         *     `component`, `capture` and `region` come from Context at report
         *     time and never from a layer.
         */
        FindingLocus: {
            /** @description The catalog message's `id`; absent where the caller had only the key. */
            message?: components["schemas"]["Id"];
            key?: components["schemas"]["MessageKey"];
            locale?: components["schemas"]["Locale"];
            /** @description The translation revision the finding was computed against. */
            revision?: components["schemas"]["Id"];
            namespace?: components["schemas"]["Namespace"];
            file?: string;
            line?: number;
            column?: number;
            route?: string;
            component?: string;
            capture?: components["schemas"]["Id"];
            region?: string;
            span?: components["schemas"]["FindingSpan"];
        };
        /** @description A hint, never an action: nothing applies one without a person or an explicit `--fix`. */
        FindingFix: {
            /** @enum {string} */
            kind: "replace" | "shorten" | "use-term" | "adopt-source-change";
            /** @description The length `shorten` asks for. */
            to?: number;
            term?: components["schemas"]["Id"];
            hint?: string;
        };
        /**
         * @description One problem, from any layer, on any surface — the
         *     `glossa.finding/v1` document
         *     (`runtimes/testdata/schemas/finding.v1.schema.json`), which
         *     `glossa check --json`, the pull-request check and MCP's read
         *     tools all carry. The shape is the contract.
         */
        Finding: {
            /** @constant */
            schema: "glossa.finding/v1";
            fingerprint: components["schemas"]["Fingerprint"];
            layer: components["schemas"]["FindingLayer"];
            /** @description The rule. Stable, and shared with the CLI and the pull-request check. */
            code: string;
            severity: components["schemas"]["FindingSeverity"];
            locus: components["schemas"]["FindingLocus"];
            /** @description The explanation for a person. Its wording is not stable and it is not part of the fingerprint. */
            message: string;
            /** @description What the finding names — the argument, the markup element, the term used. Part of the fingerprint. */
            subject?: string;
            /** @description Qualifies the code. */
            detail?: string;
            /** @description What the layer measured, free-form per code. */
            evidence?: {
                [key: string]: unknown;
            };
            fix?: components["schemas"]["FindingFix"];
            /** @description The source revision the finding was computed against. A waiver dies when it changes. */
            source_revision?: number;
            /** @description The waiver that accepted it. Present exactly when `severity` is `waived`. */
            waiver?: components["schemas"]["Id"];
        };
        /** @description A page of one check run's findings, as they stand now. */
        FindingList: {
            items: components["schemas"]["Finding"][];
            /** @description The run the page came from; absent when nothing has been checked yet. */
            run?: components["schemas"]["CheckRun"];
            /** @description The whole run with today's waivers applied, whatever the filters select. */
            counts: components["schemas"]["CheckRunCounts"];
            next_page_token?: string;
        };
        /** @description A page of the findings on one capture, as they stand now. */
        CaptureFindingList: {
            items: components["schemas"]["Finding"][];
            next_page_token?: string;
        };
        /**
         * @description Where a linguistic-QA job stands. `queued` → `running` →
         *     `succeeded` or `failed`; a job that has not finished can be
         *     cancelled.
         * @enum {string}
         */
        LinguisticJobState: "queued" | "running" | "succeeded" | "failed" | "cancelled";
        /**
         * @description Why a job ended where it did. The first four are spelled exactly
         *     as Intelligence spells the same refusals on a translation job,
         *     because they are the same refusals: the tenant has not consented
         *     to sending text to a provider, its monthly AI budget is spent,
         *     every namespace the job selected is tagged `sensitive`, or no
         *     provider is configured. `invalid_output` is a model answer that
         *     did not parse into (code, span, explanation, optional
         *     suggestion), and `layer_off` a project whose policy switches the
         *     layer off.
         * @enum {string}
         */
        LinguisticFailureCode: "provider_consent" | "budget_exceeded" | "sensitive" | "no_route" | "invalid_output" | "provider_error" | "layer_off" | "internal";
        /**
         * @description What one job reviews. `locales` is required — a review is of a
         *     translation, and there is no translation without a locale — and
         *     `namespace`, `key_prefix` and `keys` narrow the catalog it
         *     covers. Messages under a namespace tagged `sensitive` are never
         *     included, whatever this says.
         */
        LinguisticScope: {
            locales: components["schemas"]["Locale"][];
            namespace?: components["schemas"]["Namespace"];
            /** @description Only the keys under this prefix. */
            key_prefix?: string;
            /** @description Individual messages; absent is the whole selection. */
            keys?: components["schemas"]["MessageKey"][];
        };
        /** @description One linguistic review to run. */
        LinguisticJobCreate: {
            /** @description The branch or environment the review is of. Its findings are recorded in a check run of it. */
            ref: string;
            scope: components["schemas"]["LinguisticScope"];
        };
        /**
         * @description One explicit or batch linguistic review (RFC 0005 §3.8): what it
         *     covers, where it stands, and what became of it. Its findings are
         *     always `warning` and a policy may not raise them to `error`.
         */
        LinguisticJob: {
            id: components["schemas"]["Id"];
            ref: string;
            scope: components["schemas"]["LinguisticScope"];
            state: components["schemas"]["LinguisticJobState"];
            /** @description The check run the findings were recorded in; absent while the job has produced none. Read them with `listFindings?run=…`. */
            check_run?: components["schemas"]["Id"];
            /** @description The findings stored. */
            findings: number;
            /** @description Messages left out because their namespace is tagged `sensitive` and is never sent to a provider. Counted and named, never silently dropped. */
            skipped_sensitive: number;
            /** @description The translations the model saw. */
            reviewed: number;
            failure_code?: components["schemas"]["LinguisticFailureCode"];
            /** @description The sentence that says what to change; present with `failure_code`. */
            failure_detail?: string;
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            started_at?: components["schemas"]["Timestamp"];
            finished_at?: components["schemas"]["Timestamp"];
        };
        /** @description A page of a project's linguistic-QA jobs, newest first. */
        LinguisticJobList: {
            items: components["schemas"]["LinguisticJob"][];
            next_page_token?: string;
        };
        /**
         * @description What asked for the run.
         * @enum {string}
         */
        CheckRunTrigger: "cli" | "pull_request" | "write" | "capture" | "api";
        /**
         * @description The run's verdict, spelled as the pull-request check spells it.
         *     `neutral` is a run that could not grade itself: no policy, or no
         *     catalog to check.
         * @enum {string}
         */
        CheckRunConclusion: "success" | "failure" | "neutral";
        /**
         * @description Findings by how they ended up. `waived` is counted on its own
         *     and is never part of `errors` or `warnings`, so the number a
         *     dashboard shows is true.
         */
        CheckRunCounts: {
            errors: number;
            warnings: number;
            waived: number;
        };
        /** @description One evaluation of a project against its policy. */
        CheckRun: {
            id: components["schemas"]["Id"];
            /** @description What was checked — a branch, or an environment name. */
            ref: string;
            /** @description The commit the run graded, where there is one. */
            commit?: string;
            trigger: components["schemas"]["CheckRunTrigger"];
            /**
             * @description The policy the run graded itself against, so a run can say
             *     which it used when two are live at once. `0` while the
             *     policy document is still the two-field kernel policy.
             */
            policy_version: number;
            /**
             * @description The layers the run actually computed, in report order. A
             *     layer the policy switched off is not in the list, so a
             *     reader can tell "clean" from "not looked at".
             */
            layers: components["schemas"]["FindingLayer"][];
            /** @description What the run concluded, with the waivers live while it graded. Nothing rewrites it. */
            counts: components["schemas"]["CheckRunCounts"];
            /** @description Absent while the run is in flight. */
            conclusion?: components["schemas"]["CheckRunConclusion"];
            created_by: string;
            started_at: components["schemas"]["Timestamp"];
            completed_at?: components["schemas"]["Timestamp"];
        };
        CheckRunList: {
            items: components["schemas"]["CheckRun"][];
            next_page_token?: string;
        };
        /**
         * @description What asked for a recorded run. `capture` and `write` are the
         *     server's own jobs — the capture upload's visual pass and the
         *     write-time catalog check — and are not claimable by a caller.
         * @default api
         * @enum {string}
         */
        ReportedTrigger: "cli" | "pull_request" | "api";
        /**
         * @description As much of the finding's locus as the reporter knows. `message`
         *     is absent: the ingest resolves `key` against the catalog itself,
         *     because the catalog message ID is what the fingerprint hashes
         *     and what makes one finding one identity across surfaces.
         *     `capture` and `region` are absent for the same reason in the
         *     other direction — only the server that minted a capture's ID can
         *     pair them, and a check run is of a catalog, not of a screenshot.
         */
        ReportedFindingLocus: {
            key?: components["schemas"]["MessageKey"];
            locale?: components["schemas"]["Locale"];
            /** @description The translation revision the finding was computed against. */
            revision?: components["schemas"]["Id"];
            namespace?: components["schemas"]["Namespace"];
            file?: string;
            line?: number;
            column?: number;
            route?: string;
            component?: string;
            span?: components["schemas"]["FindingSpan"];
        };
        /**
         * @description One `glossa.finding/v1` finding as a reporter can write it. The
         *     `fingerprint` is deliberately absent — the server computes it
         *     over the catalog message the key resolved to, and one minted by
         *     a client would not be the one every other surface computes for
         *     the same finding — and so are the run's verdict and its counts,
         *     which the policy reaches and nobody asserts.
         */
        ReportedFinding: {
            /** @enum {string} */
            schema: "glossa.finding/v1";
            layer: components["schemas"]["FindingLayer"];
            code: string;
            /**
             * @description The severity the layer emitted. The project's policy decides
             *     what it is worth here and may raise, lower or switch it off.
             *     `waived` is refused: a waiver is the project's to apply, not
             *     a reporter's to assert.
             * @enum {string}
             */
            severity: "error" | "warning";
            locus: components["schemas"]["ReportedFindingLocus"];
            message: string;
            subject?: string;
            detail?: string;
            /** @description What the layer measured, free-form per code. */
            evidence?: {
                [key: string]: unknown;
            };
            fix?: components["schemas"]["FindingFix"];
            /**
             * @description The source revision the finding was computed against — the
             *     server's own number, read back from the translation the
             *     layer graded. A waiver dies when it changes.
             */
            source_revision?: number;
        };
        /**
         * @description One evaluation to record: what was checked, which layers ran,
         *     and what they found.
         */
        CreateCheckRun: {
            /** @description What was checked — a branch, or an environment name. */
            ref: string;
            /**
             * @description The commit graded, where there is one. A branch moves; the
             *     commit a verdict was about does not.
             */
            commit?: string;
            trigger?: components["schemas"]["ReportedTrigger"];
            /**
             * @description The environment the run is about, which selects the policy's
             *     block for it. Absent is a branch check, in no environment at
             *     all — which is every check in CI, so a rule naming an
             *     environment says nothing about a pull request.
             */
            environment?: string;
            /**
             * @description The layers that actually ran, in report order. A layer the
             *     policy switched off, or the run never computed, is not in
             *     the list: "clean" and "not looked at" are different answers.
             */
            layers: components["schemas"]["FindingLayer"][];
            /** @description When the run started; the server's clock where it is absent. */
            started_at?: components["schemas"]["Timestamp"];
            /** @description What the layers found. At most 10 000; more is refused, not truncated. */
            findings?: components["schemas"]["ReportedFinding"][];
        };
        /**
         * @description How far a waiver reaches — everywhere in the project, or on one branch.
         * @enum {string}
         */
        WaiverScope: "project" | "branch";
        /**
         * @description What a waiver accepts, read from the most recent stored finding
         *     carrying its fingerprint. Empty where no stored finding carries
         *     it any more, which is exactly the unexamined waiver a dashboard
         *     should show.
         */
        WaiverFinding: {
            layer?: components["schemas"]["FindingLayer"];
            code?: string;
            locale?: components["schemas"]["Locale"];
            key?: components["schemas"]["MessageKey"];
            namespace?: components["schemas"]["Namespace"];
            message?: string;
        };
        /** @description One accepted finding, and why. */
        Waiver: {
            id: components["schemas"]["Id"];
            fingerprint: components["schemas"]["Fingerprint"];
            reason: string;
            scope: components["schemas"]["WaiverScope"];
            /** @description The branch, for a branch-scoped waiver. */
            ref?: string;
            /**
             * @description The source revision the waiver was made against. The waiver
             *     dies when the source moves past it, and the finding comes
             *     back saying so.
             */
            source_revision: number;
            /** @description It stands now — neither revoked nor expired. */
            active: boolean;
            accepts?: components["schemas"]["WaiverFinding"];
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            /** @description When the daily sweep retires it; absent, never. */
            expires_at?: components["schemas"]["Timestamp"];
            /** @description When a person took it back; absent while it stands. */
            revoked_at?: components["schemas"]["Timestamp"];
        };
        WaiverList: {
            items: components["schemas"]["Waiver"][];
            next_page_token?: string;
        };
        /** @description A finding to accept. The reason is required and non-empty. */
        CreateWaiver: {
            fingerprint: components["schemas"]["Fingerprint"];
            /** @description Why this finding is fine. Blank or whitespace-only is refused. */
            reason: string;
            /** @description Default `project`. */
            scope?: components["schemas"]["WaiverScope"];
            /** @description The branch, required for `scope` `branch` and ignored otherwise. */
            ref?: string;
            /**
             * @description The source revision to waive against. Absent: the one the
             *     most recent stored finding with this fingerprint was
             *     computed against, which is the finding the person is looking
             *     at.
             */
            source_revision?: number;
            /** @description When the daily sweep retires it; absent, never. A time in the past is refused. */
            expires_at?: components["schemas"]["Timestamp"];
        };
        /**
         * @description Which locales must be complete: every one of the project's
         *     (`all`), only those in `locales` (`listed`), or none of them.
         *     `listed` with an empty `locales` is stored as `none`.
         * @enum {string}
         */
        CheckPolicyLocaleRequirement: "all" | "listed" | "none";
        /**
         * @description One line of the policy document (RFC 0005 §4.1): what it
         *     selects, what that is worth, and whether it may fail a run yet.
         *
         *     The selector is any subset of `layer`, `code`, `locale`,
         *     `namespace` and `environment`. Every field it names must match
         *     the finding; every field it leaves out matches anything. A field
         *     the finding doesn't carry never matches a rule that names it, so
         *     a rule for `environment: production` says nothing about a pull
         *     request's branch check, which runs in no environment at all.
         *
         *     **Precedence is by specificity**: the rule naming more fields
         *     wins, and ties go to the later rule — so a rule appended to a
         *     policy always wins against the rule it was written to override.
         *     A policy nobody can predict is worse than no policy.
         */
        CheckPolicyRule: {
            layer?: components["schemas"]["FindingLayer"];
            code?: string;
            locale?: components["schemas"]["Locale"];
            namespace?: components["schemas"]["Namespace"];
            /** @description The environment the run is about. A branch check is in none. */
            environment?: string;
            /**
             * @description What the selected findings are worth. `off` means "don't
             *     compute": the layer isn't run, so a project doesn't pay for
             *     QA it ignores, and nothing is reported or counted. A rule
             *     may not raise the model-decided `linguistic` layer to
             *     `error` — a build never fails on an opinion.
             * @enum {string}
             */
            severity: "error" | "warning" | "off";
            /**
             * @description `enforce` (the default) lets the rule change the run's
             *     conclusion. `warn` computes and reports at the rule's
             *     severity but can never fail a run: it is the on-ramp for a
             *     stricter policy — ship the rule, watch the number, flip it.
             * @enum {string}
             */
            mode?: "enforce" | "warn";
        };
        /**
         * @description What one environment asks for beyond the document's base:
         *     production may require locales a branch does not, and a review
         *     state a branch does not. An environment that names no
         *     `require_complete` inherits the document's, so naming one only
         *     to ask for a review state doesn't quietly change what must be
         *     translated.
         */
        CheckPolicyEnvironment: {
            /** @description Absent, the document's. */
            require_complete?: components["schemas"]["CheckPolicyLocaleRequirement"];
            /** @description The locales this block's `listed` names; empty otherwise. */
            locales?: components["schemas"]["Locale"][];
            /**
             * @description The review state a release must have reached to publish
             *     here. Release enforces it at publish (`policy_not_met`); the
             *     policy only states it.
             * @enum {string}
             */
            require_review?: "approved";
        };
        /**
         * @description The check-policy document in its canonical form (RFC 0005 §4.1):
         *     what the policy says, and none of the bookkeeping about when it
         *     said it. It is what `exportCheckPolicy` writes and
         *     `importCheckPolicy` reads, and what `saveCheckPolicy` takes as
         *     its `policy`.
         *
         *     A save replaces the whole document. The three base fields —
         *     `require_complete`, `fail_on` and `missing_translations` — are
         *     the ones every project starts from and mean exactly what they
         *     meant before the document existed, so a policy that names no
         *     rules and no environments decides every question the way the
         *     pre-document policy did.
         */
        CheckPolicyDocument: {
            /**
             * @description Always present in responses; absent in a write, the server writes this one.
             * @enum {string}
             */
            schema?: "glossa.check-policy/v1";
            require_complete: components["schemas"]["CheckPolicyLocaleRequirement"];
            /**
             * @description The locales `require_complete: listed` names; empty
             *     otherwise. Every one must be a locale the project has
             *     (`invalid_check_policy`).
             */
            locales?: components["schemas"]["Locale"][];
            /**
             * @description The lowest severity that fails the check: `error` (the
             *     default), `warning` — a warning fails it too — or `never`,
             *     where the check only ever reports.
             * @enum {string}
             */
            fail_on: "error" | "warning" | "never";
            /**
             * @description What an untranslated key in a locale that must be complete
             *     is. Teams that translate after merging set this to `warning`
             *     and keep every locale required: the check still lists what
             *     is untranslated without blocking the pull request.
             * @enum {string}
             */
            missing_translations: "error" | "warning";
            /** @description What each environment asks for, by environment name. */
            environments?: {
                [key: string]: components["schemas"]["CheckPolicyEnvironment"];
            };
            /**
             * @description The selectors, in document order. Order is part of the
             *     meaning: ties in specificity go to the later rule.
             */
            rules?: components["schemas"]["CheckPolicyRule"][];
        };
        /** @description The policy that grades, and the record of who put it there. */
        CheckPolicyState: {
            /**
             * @description Monotonic. Every check run records the version it graded
             *     itself against, so a run can say which it used when two are
             *     live at once. `0` is a project that has never saved one.
             */
            version: number;
            document: components["schemas"]["CheckPolicyDocument"];
            /** @description When this version was saved. */
            effective_from?: components["schemas"]["Timestamp"];
            /**
             * @description Until when pull requests opened before `effective_from` keep
             *     grading against `pinned_version`. Absent, the version grades
             *     everything at once.
             */
            grace_until?: components["schemas"]["Timestamp"];
            /** @description The version a pull request still inside the grace grades against. */
            pinned_version?: number;
            /** @description Absent for a policy nobody saved through this endpoint. */
            created_by?: string;
            created_at?: components["schemas"]["Timestamp"];
        };
        /** @description A new version of the check policy. */
        SaveCheckPolicy: {
            policy: components["schemas"]["CheckPolicyDocument"];
            /**
             * @description How long the save pins the pull requests that predate it to
             *     the version they were opened under. Absent, 14 days — the
             *     default protects the people who did not cause the change;
             *     `0` pins nothing, which is what a policy that only loosens
             *     wants.
             */
            grace_days?: number;
            /** @description Answer the impact preview and store nothing — neither the document nor a version. */
            dry_run?: boolean;
        };
        /**
         * @description What one rule of the candidate did. A rule that changed nothing
         *     is listed too: it is exactly what a reader wants to know before
         *     saving.
         */
        CheckPolicyRuleImpact: {
            /** @description The rule's index in the candidate document's `rules`. */
            rule: number;
            selector?: components["schemas"]["CheckPolicyRule"];
            /** @description Findings this rule decided. */
            matched: number;
            /** @description Findings it decided differently from the policy in force. */
            changed: number;
            /** @description Findings it turned into a reason a run fails. */
            newly_failing: number;
        };
        /**
         * @description What a candidate policy would change about the findings and the
         *     runs the project already has (RFC 0005 §4.3), measured against
         *     the newest run of each of the refs checked most recently.
         */
        CheckPolicyImpact: {
            /** @description The stored findings the preview was measured against. */
            findings: number;
            /** @description The runs those findings came from — one per ref, the newest. */
            runs: number;
            /** @description Findings the candidate makes count for more. */
            raised: number;
            lowered: number;
            /** @description Findings the candidate stops computing. */
            silenced: number;
            /** @description Findings that fail under the candidate and did not before. */
            newly_failing: number;
            no_longer_failing: number;
            /**
             * @description Open pull requests among `newly_failing_refs` — the number
             *     of people who would wake up to a red pull request they did
             *     not cause, and the one that decides whether this policy
             *     ships with a grace.
             */
            open_pull_requests: number;
            /** @description The refs whose verdict turns from passing to failing. */
            newly_failing_refs?: string[];
            /**
             * @description The open pull requests `open_pull_requests` counts, named: each
             *     one among `newly_failing_refs` that has an open pull request,
             *     with its number and, where the project's repository is known,
             *     where it is. The count says how many people would wake up to a
             *     red pull request; this is what lets whoever saves the policy go
             *     and tell them. Absent when there are none.
             */
            newly_failing_pull_requests?: components["schemas"]["CheckPolicyPullRequest"][];
            no_longer_failing_refs?: string[];
            rules: components["schemas"]["CheckPolicyRuleImpact"][];
        };
        /** @description An open pull request a candidate check policy would newly fail (RFC 0005 §4.3). */
        CheckPolicyPullRequest: {
            /** @description The pull request's branch. */
            ref: string;
            /** @description The pull request's number on its repository. */
            number: number;
            /**
             * Format: uri
             * @description Where the pull request is on the web: the repository connected
             *     to the project, at the GitHub App's web host. Absent when no
             *     repository is connected, when the project is connected to more
             *     than one, or when the caller may not read the integration.
             */
            url?: string;
        };
        CheckPolicySaved: {
            /** @description Nothing was stored. */
            dry_run: boolean;
            policy: components["schemas"]["CheckPolicyState"];
            impact: components["schemas"]["CheckPolicyImpact"];
        };
        /** @description One saved version of the policy, and who saved it when. */
        CheckPolicyVersion: {
            version: number;
            document: components["schemas"]["CheckPolicyDocument"];
            effective_from?: components["schemas"]["Timestamp"];
            grace_until?: components["schemas"]["Timestamp"];
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
        };
        CheckPolicyVersionList: {
            items: components["schemas"]["CheckPolicyVersion"][];
            next_page_token?: string;
        };
        /**
         * @description The seven numbers of a project's localization health, for the
         *     project as a whole and per locale, plus the findings-by-day
         *     trend.
         *
         *     Every number is optional, and an absent one was **not
         *     measured** — never zero. `unmeasured` names each one that is
         *     missing with the reason. `findings` absent means nothing was
         *     ever checked; `findings.errors: 0` means a run looked and found
         *     none, and a client that rendered the first as `0` would be
         *     reporting the absence of a check as the absence of problems.
         */
        QualitySummary: {
            /** @enum {string} */
            schema: "glossa.quality-summary/v1";
            project_id: components["schemas"]["Id"];
            /** @description The locale the rows were narrowed to; absent for all of them. */
            locale?: components["schemas"]["Locale"];
            /** @description Where "published" meant, for the lead time. */
            environment: components["schemas"]["EnvironmentName"];
            /** @description The start of the window the windowed numbers were measured over. */
            since: components["schemas"]["Timestamp"];
            computed_at: components["schemas"]["Timestamp"];
            /** @description When this summary stops being served from the cache. */
            expires_at: components["schemas"]["Timestamp"];
            /**
             * @description True where the summary is a previous computation served
             *     again. `computed_at` is when it was computed either way.
             */
            cached: boolean;
            project: components["schemas"]["QualityProjectHealth"];
            /** @description One row per locale any number mentioned, by code. */
            locales: components["schemas"]["QualityLocaleHealth"][];
            findings_by_day?: components["schemas"]["QualityTrend"];
            /**
             * @description Every number absent above, with why. Empty when all eight
             *     were computed.
             */
            unmeasured: components["schemas"]["QualityUnmeasured"][];
        };
        /**
         * @description The seven numbers for the project as a whole. `coverage` counts
         *     the target locales only — the source locale is complete by
         *     definition, and counting it would flatter every project by one
         *     locale's worth. `checks` has no locale, because a pull request
         *     is about a commit and not a language.
         */
        QualityProjectHealth: {
            coverage?: components["schemas"]["QualityCoverage"];
            findings?: components["schemas"]["CheckRunCounts"];
            /** @description The findings broken down by layer, in report order. A layer that found nothing is not a row. */
            by_layer?: components["schemas"]["QualityLayerCounts"][];
            run?: components["schemas"]["QualitySummaryRun"];
            ai?: components["schemas"]["QualityAcceptance"];
            queue?: components["schemas"]["QualityQueue"];
            context?: components["schemas"]["QualityContextCoverage"];
            lead_time?: components["schemas"]["QualityPercentiles"];
            checks?: components["schemas"]["QualityCheckHealth"];
        };
        /**
         * @description The same numbers for one locale, and which layers are available
         *     for it. There is no `context` here: a usage is a place in the
         *     product's code and a region a box on a screenshot, and neither
         *     belongs to a locale.
         */
        QualityLocaleHealth: {
            code: components["schemas"]["Locale"];
            direction: components["schemas"]["Direction"];
            is_source: boolean;
            coverage?: components["schemas"]["QualityCoverage"];
            findings?: components["schemas"]["CheckRunCounts"];
            ai?: components["schemas"]["QualityAcceptance"];
            queue?: components["schemas"]["QualityQueue"];
            lead_time?: components["schemas"]["QualityPercentiles"];
            /** @description Every layer, available or not, in report order. */
            layers: components["schemas"]["QualitySummaryLayer"][];
        };
        /** @description The check run the findings came from — the project's newest. */
        QualitySummaryRun: {
            id: components["schemas"]["Id"];
            ref: string;
            commit?: string;
            policy_version: number;
            conclusion?: components["schemas"]["CheckRunConclusion"];
            started_at: components["schemas"]["Timestamp"];
            /** @description The layers that run actually computed, which is what tells "clean" from "not looked at". */
            layers: components["schemas"]["FindingLayer"][];
        };
        /**
         * @description One layer for one locale (intent §41). `available` is asked
         *     before grading and answered from the project's check policy, so
         *     a layer that cannot run here is never drawn as one that ran and
         *     passed. `checked` says the newest run actually computed it, and
         *     only a checked layer carries `findings` — a layer nobody ran
         *     found nothing in the sense that says nothing.
         */
        QualitySummaryLayer: {
            layer: components["schemas"]["FindingLayer"];
            available: boolean;
            /**
             * @description Why it cannot run here. `unsupported_locale`: the policy
             *     switches it off for this locale while leaving it on for
             *     others. `not_configured`: the policy switches it off
             *     throughout the project. `no_evidence`: the policy asks for
             *     it and the newest run did not compute it — it had no
             *     capture, no termbase or no provider to compute it from, or
             *     the run that produced these numbers was narrowed to other
             *     layers.
             * @enum {string}
             */
            unavailable?: "unsupported_locale" | "not_configured" | "no_evidence";
            checked: boolean;
            findings?: components["schemas"]["CheckRunCounts"];
        };
        /** @description One number the summary could not compute, and why. */
        QualityUnmeasured: {
            /** @enum {string} */
            number: "coverage" | "findings" | "ai" | "queue" | "context" | "lead_time" | "checks" | "findings_by_day";
            reason: string;
        };
        /**
         * @description The p50 and p90 of a sample of durations, in seconds, with the
         *     size of the sample. It is present only where something was
         *     measured: an empty sample has no median, and a zero would say
         *     "no wait" where the truth is "nothing waited".
         */
        QualityPercentiles: {
            samples: number;
            /** Format: double */
            p50_seconds: number;
            /** Format: double */
            p90_seconds: number;
        };
        /** @description Number 1 — how much of the catalog is translated (Localization). */
        QualityCoverage: {
            /** @description The active messages this is a share of. */
            messages: number;
            translated: number;
            /** @description Usable translations made against an older source revision. */
            outdated: number;
            missing: number;
        };
        QualityLayerCounts: {
            layer: components["schemas"]["FindingLayer"];
            counts: components["schemas"]["CheckRunCounts"];
        };
        /**
         * @description Number 3 — what people did with the machine's suggestions
         *     (Intelligence). `decisions` is accepted plus rejected, the
         *     denominator of the rate, so a rate with nothing behind it cannot
         *     be mistaken for a bad one. `mean_edit_distance` counts a
         *     suggestion accepted as it is as 0, and the project's is pooled
         *     over accepted suggestions rather than averaged over locales.
         */
        QualityAcceptance: {
            decisions: number;
            accepted: number;
            /** @description Of the accepted: how many were changed first. */
            edited: number;
            rejected: number;
            /** Format: double */
            acceptance_rate: number;
            /** Format: double */
            mean_edit_distance: number;
        };
        /**
         * @description Number 4 — the review queue (Intelligence). `depth` is always
         *     measured; `age` is how long what is in it has been waiting, and
         *     an empty queue has none. The project's `age` is present only
         *     where one locale is in scope: the percentiles of several queues
         *     cannot be pooled from their percentiles.
         */
        QualityQueue: {
            depth: number;
            age?: components["schemas"]["QualityPercentiles"];
        };
        /**
         * @description Number 5 — how much of the catalog the product's own code and
         *     screenshots account for (Context), on the default branch.
         *     `active_messages` is the denominator; a project with none has no
         *     coverage to report, which is neither 0 % nor 100 %.
         */
        QualityContextCoverage: {
            active_messages: number;
            /** @description Active messages with a usage in a current build. */
            with_usage: number;
            /** @description Active messages with a visible region on a capture of a current build. */
            with_region: number;
        };
        /**
         * @description Number 7 — the pull-request check's record over the window
         *     (Integration). `pass_rate` is successes over the checks that
         *     passed or failed; `neutral` is in neither half, because it is
         *     what a check concludes when it had nothing to grade. The rate
         *     and `latency` are absent where nothing concluded.
         */
        QualityCheckHealth: {
            /** @description Checks that reached a verdict in the window. */
            runs: number;
            succeeded: number;
            failed: number;
            neutral: number;
            /** Format: double */
            pass_rate?: number;
            /**
             * Format: double
             * @description The median from the pull-request event to the conclusion.
             */
            median_seconds?: number;
            /** @description The same measurement with its p90 and sample size. */
            latency?: components["schemas"]["QualityPercentiles"];
        };
        /**
         * @description Findings by layer per day — the one trend M4 keeps, rolled up in
         *     Quality's own table because Quality owns the data. A day counts
         *     each finding once by fingerprint, however many runs saw it. A
         *     layer appears on a day only where a run that day ran it, so a
         *     missing row means "not looked at" and a row with `0` means
         *     "looked at and clean"; a day with no run has no rows at all.
         */
        QualityTrend: {
            /** Format: date */
            from: string;
            /** Format: date */
            to: string;
            days: components["schemas"]["QualityDayFindings"][];
        };
        QualityDayFindings: {
            /** Format: date */
            day: string;
            layer: components["schemas"]["FindingLayer"];
            findings: number;
        };
        /**
         * @description What a definition's instances are about.
         * @default translation
         * @enum {string}
         */
        WorkflowSubject: "translation" | "release_request";
        /**
         * @description A `glossa.workflow/v1` document (RFC 0006 §2.3): `schema`,
         *     `name` (`^[a-z0-9][a-z0-9-]{0,63}$`), `subject`, a statekit
         *     Native JSON `chart`, and the `guards` and `actions` that bind the
         *     chart's names to the platform's vocabulary of primitives. It is
         *     data, never code: a name the vocabulary does not have is refused
         *     at save. At most 256 KiB.
         */
        WorkflowDocument: {
            [key: string]: unknown;
        };
        /** @description One thing wrong with, or worth knowing about, a definition. */
        WorkflowFinding: {
            /** @description The check that found it (`unknown-primitive`, `delayed-transition`, `statechart`, …). */
            rule: string;
            /**
             * @description `error` and `warning` refuse a save; `info` is a note.
             * @enum {string}
             */
            severity: "error" | "warning" | "info";
            /** @description Where in the document (`guards.two_approvals`, `chart.states.reviewing`); absent for the whole document. */
            path?: string;
            state?: string;
            event?: string;
            message: string;
        };
        WorkflowProblem: components["schemas"]["Problem"] & {
            findings?: components["schemas"]["WorkflowFinding"][];
        };
        WorkflowLintResult: {
            /** @description Whether a save of this document would be accepted. */
            valid: boolean;
            findings: components["schemas"]["WorkflowFinding"][];
        };
        WorkflowDefinition: {
            id: components["schemas"]["Id"];
            /** @description Set when only this project may bind it. */
            project_id?: components["schemas"]["Id"];
            name: string;
            subject: components["schemas"]["WorkflowSubject"];
            /** @description The latest version's number. */
            version: number;
            created_by: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            deleted_at?: string;
        };
        WorkflowDefinitionList: {
            items: components["schemas"]["WorkflowDefinition"][];
            next_page_token?: string;
        };
        /** @description A definition at the version a save just stored. */
        WorkflowDefinitionSaved: {
            id: components["schemas"]["Id"];
            project_id?: components["schemas"]["Id"];
            name: string;
            subject: components["schemas"]["WorkflowSubject"];
            /** @description The version this save stored. */
            version: number;
            /** @description Who saved this version. */
            created_by: string;
            /**
             * Format: date-time
             * @description When this version was saved.
             */
            created_at: string;
            /** @description Lint's informational notes; anything worse refused the save. */
            findings: components["schemas"]["WorkflowFinding"][];
        };
        WorkflowDefinitionVersion: {
            definition_id: components["schemas"]["Id"];
            version: number;
            /** @example glossa.workflow/v1 */
            schema: string;
            document: components["schemas"]["WorkflowDocument"];
            created_by: string;
            /** Format: date-time */
            created_at: string;
        };
        WorkflowDefinitionVersionList: {
            items: components["schemas"]["WorkflowDefinitionVersion"][];
            next_page_token?: string;
        };
        CreateWorkflowBinding: {
            definition_id: components["schemas"]["Id"];
            /** @description Narrows the binding to these locales; absent or empty is every locale. */
            locales?: components["schemas"]["Locale"][];
            /** @description Narrows the binding to one namespace. */
            namespace?: string;
        };
        WorkflowBinding: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            definition_id: components["schemas"]["Id"];
            subject: components["schemas"]["WorkflowSubject"];
            /** @description Canonical and sorted; empty is every locale. */
            locales: components["schemas"]["Locale"][];
            namespace?: string;
            /** @description Creation order; the later of two equally specific bindings wins. */
            position: number;
            created_by: string;
            /** Format: date-time */
            created_at: string;
        };
        WorkflowBindingList: {
            items: components["schemas"]["WorkflowBinding"][];
            next_page_token?: string;
        };
        WorkflowResolution: {
            /** @description Whether any binding applies. */
            bound: boolean;
            binding?: components["schemas"]["WorkflowBinding"];
            definition_id?: components["schemas"]["Id"];
            definition_name?: string;
            /** @description The version a new instance would start on. */
            version?: number;
        };
        /**
         * @description Exactly one of `member`, `role`, `group` or `vendor` — who work
         *     is given to, or who may approve. A group or a vendor is named by
         *     its `id` or its name (case does not matter).
         */
        Party: {
            member?: components["schemas"]["Id"];
            role?: components["schemas"]["Role"];
            /** @description A group `id` or name. */
            group?: string;
            /** @description A vendor `id` or name. */
            vendor?: string;
        };
        /** @description A party as stored, resolved to Identity's ids. */
        Assignee: {
            /** @enum {string} */
            kind: "member" | "role" | "group" | "vendor";
            /** @description The member, group or vendor; absent for a role. */
            id?: components["schemas"]["Id"];
            role?: components["schemas"]["Role"];
        };
        AssignmentReport: {
            since?: components["schemas"]["Timestamp"];
            generated_at: components["schemas"]["Timestamp"];
            /** @description A bound stopped the report; its numbers cover only what was read before it. */
            truncated: boolean;
            rows: components["schemas"]["AssignmentReportRow"][];
        };
        /** @description One assignee's completed work in one locale (RFC 0006 §3.4). */
        AssignmentReportRow: {
            /** @description As stored: `vendor:<id>`, `member:<id>`, `group:<id>` or `role:<name>`. */
            assignee: string;
            locale: components["schemas"]["Locale"];
            assignments: number;
            on_time: number;
            late: number;
            no_due: number;
            units: number;
            /** @description Units whose facts could not be read. */
            unavailable: number;
            source_words: number;
            /** @description Source words delivered from translation memory, by match band (`exact`). */
            tm_words: {
                [key: string]: number;
            };
            approved: number;
            rejected: number;
            needs_review: number;
            draft: number;
            unreviewed: number;
            changed_after_delivery: number;
            /** Format: double */
            mean_edit_distance: number;
            /** Format: double */
            mean_edit_ratio: number;
            /** @description Open findings by layer. */
            findings: {
                [key: string]: number;
            };
            /** Format: double */
            findings_per_unit: number;
            reworked: number;
            /** Format: double */
            rework_rate: number;
            /**
             * Format: double
             * @description On time over the assignments that had a due date.
             */
            on_time_rate: number;
        };
        /**
         * @description `open` and `accepted` are live; the others are final.
         * @enum {string}
         */
        AssignmentState: "open" | "accepted" | "done" | "declined" | "expired";
        AssignmentUnit: {
            message_id: components["schemas"]["Id"];
            locale: components["schemas"]["Locale"];
        };
        Assignment: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            /** @description The workflow instance whose `assign` action made it; absent for one made by hand. */
            instance_id?: components["schemas"]["Id"];
            /** @description The translation units, sorted by message and locale. */
            units: components["schemas"]["AssignmentUnit"][];
            assignee: components["schemas"]["Assignee"];
            /** @description What doing the work takes (`translations.write` for translating), for every unit's locale. */
            permission: string;
            due_at?: components["schemas"]["Timestamp"];
            state: components["schemas"]["AssignmentState"];
            /** @description Who made it, as the outbox names actors (`person:…`, `token:…`). */
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            updated_at: components["schemas"]["Timestamp"];
            /** @description Who completed, declined or expired it. */
            closed_by?: string;
            closed_at?: components["schemas"]["Timestamp"];
            /** @description A decline's reason. */
            reason?: string;
        };
        AssignmentList: {
            items: components["schemas"]["Assignment"][];
            next_page_token?: string;
        };
        CreateAssignment: {
            project_id: components["schemas"]["Id"];
            units: {
                message: components["schemas"]["MessageKey"];
                locale: components["schemas"]["Locale"];
            }[];
            assignee: components["schemas"]["Party"];
            /** @description What doing the work takes; `translations.write` when omitted. */
            permission?: string;
            due_at?: components["schemas"]["Timestamp"];
        };
        AssignmentDecline: {
            reason?: string;
        };
        /**
         * @description `pending` collects decisions; one denial ends it `denied`, n distinct grants `granted`.
         * @enum {string}
         */
        ApprovalState: "pending" | "granted" | "denied";
        ApprovalDecision: {
            /** @description Who decided (`person:…`). */
            principal: string;
            /** @enum {string} */
            decision: "granted" | "denied";
            reason?: string;
            at: components["schemas"]["Timestamp"];
        };
        Approval: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            /** @description The workflow instance whose `request_approval` action asked for it; absent for one asked by hand. */
            instance_id?: components["schemas"]["Id"];
            subject: components["schemas"]["WorkflowSubject"];
            /** @description The message, for a translation unit; the release request otherwise. */
            subject_id: components["schemas"]["Id"];
            /** @description The translation unit's locale; absent for a release request. */
            locale?: components["schemas"]["Locale"];
            /** @description How many distinct eligible people must grant it. */
            required: number;
            eligible: components["schemas"]["Assignee"];
            /** @description Four-eyes: the author of the text under approval neither decides nor counts. */
            distinct_from_author: boolean;
            due_at?: components["schemas"]["Timestamp"];
            state: components["schemas"]["ApprovalState"];
            /** @description Every decision, in the order they were made. Append-only. */
            decisions: components["schemas"]["ApprovalDecision"][];
            created_by: string;
            created_at: components["schemas"]["Timestamp"];
            closed_at?: components["schemas"]["Timestamp"];
        };
        ApprovalList: {
            items: components["schemas"]["Approval"][];
            next_page_token?: string;
        };
        /** @description An approval of one translation unit. */
        CreateApproval: {
            project_id: components["schemas"]["Id"];
            message: components["schemas"]["MessageKey"];
            locale: components["schemas"]["Locale"];
            /** @description How many distinct eligible people must grant it. */
            n: number;
            from: components["schemas"]["Party"];
            due_at?: components["schemas"]["Timestamp"];
        };
        CreateApprovalDecision: {
            /** @enum {string} */
            decision: "granted" | "denied";
            reason?: string;
        };
        /** @enum {string} */
        WorkflowInstanceStatus: "active" | "finished";
        WorkflowInstance: {
            id: components["schemas"]["Id"];
            project_id: components["schemas"]["Id"];
            definition_id: components["schemas"]["Id"];
            /** @description The version the instance runs on; a new save does not move it. */
            definition_version: number;
            subject: components["schemas"]["WorkflowSubject"];
            /** @description The message, for a translation unit; the release request otherwise. */
            subject_id: components["schemas"]["Id"];
            /** @description The translation unit's locale; absent for a release request. */
            locale?: components["schemas"]["Locale"];
            /** @description The chart state the instance is in. */
            state: string;
            status: components["schemas"]["WorkflowInstanceStatus"];
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        RebaseWorkflowInstance: {
            /** @description The version to move to; the definition's latest when absent. */
            version?: number;
        };
        WorkflowInstanceList: {
            items: components["schemas"]["WorkflowInstance"][];
            next_page_token?: string;
        };
        WorkflowTransition: {
            /** Format: int64 */
            seq: number;
            from: string;
            event: string;
            /** @description The state after; the same as `from` unless `applied`. */
            to: string;
            /** @enum {string} */
            outcome: "applied" | "ignored" | "refused";
            guards: components["schemas"]["WorkflowGuardOutcome"][];
            actions: components["schemas"]["WorkflowActionOutcome"][];
            /** @description Who caused the event, as the outbox names them (`person:…`, `token:…`, `system:…`). The transition's actions ran as this actor. */
            actor: string;
            outbox_event_id?: components["schemas"]["Id"];
            /** Format: date-time */
            at: string;
        };
        WorkflowGuardOutcome: {
            /** @description The guard's name in the definition. */
            guard: string;
            passed: boolean;
        };
        WorkflowActionOutcome: {
            /** @description The action's name in the definition. */
            name: string;
            /** @enum {string} */
            outcome: "done" | "refused" | "failed";
            detail?: string;
        };
        WorkflowTransitionList: {
            items: components["schemas"]["WorkflowTransition"][];
            next_page_token?: string;
        };
    };
    responses: {
        /** @description Signed in. The session cookie is set. */
        SessionStarted: {
            headers: {
                "Set-Cookie": components["headers"]["SessionCookie"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Session"];
            };
        };
        /** @description The request is malformed or invalid. */
        BadRequest: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description No valid credentials. */
        Unauthenticated: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Authenticated, but not allowed (or missing the CSRF token). */
        Forbidden: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description No such resource in this tenant. */
        NotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description The request conflicts with the resource's state. */
        Conflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description `If-Match` doesn't match the current `ETag`. */
        PreconditionFailed: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description `If-Match` is required. */
        PreconditionRequired: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description The `Idempotency-Key` was used for a different request. */
        UnprocessableEntity: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Slow down. */
        TooManyRequests: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description A dependency (object storage) is unavailable; retry. */
        Unavailable: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description The request body exceeds the limit. */
        PayloadTooLarge: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description The resource existed and was deleted (retention). */
        Gone: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /**
         * @description `structural_qa_failed`: the translation is structurally
         *     incompatible with its source (`findings`, errors first).
         */
        StructuralQAFailed: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["QAProblem"];
            };
        };
        /**
         * @description `invalid_workflow`: the document does not compile or lint, and
         *     nothing was saved (`findings`, every one).
         */
        InvalidWorkflow: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["WorkflowProblem"];
            };
        };
    };
    parameters: {
        /** @description A tenant `id`. */
        TenantPath: components["schemas"]["Id"];
        /** @description A member `id`. */
        MemberPath: components["schemas"]["Id"];
        /** @description An API token `id` (never its secret). */
        TokenPath: components["schemas"]["Id"];
        /** @description A group `id`. */
        GroupPath: components["schemas"]["Id"];
        /** @description A vendor `id`. */
        VendorPath: components["schemas"]["Id"];
        /** @description A project `id`. */
        ProjectPath: components["schemas"]["Id"];
        /** @description An application `id`. */
        ApplicationPath: components["schemas"]["Id"];
        /** @description A GitHub installation `id` as Glossa issued it, not GitHub's number. */
        InstallationPath: components["schemas"]["Id"];
        /** @description A Git connection `id`. */
        ConnectionPath: components["schemas"]["Id"];
        /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
        MessagePath: components["schemas"]["MessageKey"];
        /** @description A locale code; canonicalized before use. */
        LocalePath: components["schemas"]["Locale"];
        /**
         * @description A branch `id`. Branch names may hold `/`, and an encoded slash
         *     doesn't survive every proxy, so URLs never carry the name; find
         *     the `id` with `listBranches?name=…`.
         */
        BranchPath: components["schemas"]["Id"];
        /** @description An environment `name`. */
        EnvironmentPath: components["schemas"]["EnvironmentName"];
        /** @description A release `id`. */
        ReleasePath: components["schemas"]["Id"];
        /** @description A release request `id`. */
        ReleaseRequestPath: components["schemas"]["Id"];
        /** @description A rollout `id`. */
        RolloutPath: components["schemas"]["Id"];
        /** @description A passkey `id` (its credential ID, base64url). */
        PasskeyPath: string;
        /** @description A delivery key `id` (not the key itself). */
        DeliveryKeyPath: components["schemas"]["Id"];
        /** @description A registered preview origin's `id`. */
        PreviewOriginPath: components["schemas"]["Id"];
        /** @description A translation-memory unit `id`. */
        TMUnitPath: components["schemas"]["Id"];
        /** @description A termbase concept `id`. */
        ConceptPath: components["schemas"]["Id"];
        /** @description A style guide `id`. */
        StyleGuidePath: components["schemas"]["Id"];
        /** @description An AI provider `id`. */
        AIProviderPath: components["schemas"]["Id"];
        /** @description A fill `id`. */
        AIFillPath: components["schemas"]["Id"];
        /** @description A job `id`. */
        AIJobPath: components["schemas"]["Id"];
        /** @description A suggestion `id`. */
        AISuggestionPath: components["schemas"]["Id"];
        /** @description An import job `id`. */
        ImportJobPath: components["schemas"]["Id"];
        /** @description An audit export job `id`. */
        AuditExportJobPath: components["schemas"]["Id"];
        /** @description An export job `id`. */
        ExportJobPath: components["schemas"]["Id"];
        /** @description A capture `id`. */
        CapturePath: components["schemas"]["Id"];
        /** @description A check run `id`. */
        CheckRunPath: components["schemas"]["Id"];
        /** @description A waiver `id`. */
        WaiverPath: components["schemas"]["Id"];
        /** @description A linguistic-QA job `id`. */
        LinguisticJobPath: components["schemas"]["Id"];
        /** @description A workflow definition `id`. */
        WorkflowDefinitionPath: components["schemas"]["Id"];
        /** @description A workflow definition's version number. */
        WorkflowVersionPath: number;
        /** @description A workflow binding `id`. */
        WorkflowBindingPath: components["schemas"]["Id"];
        /** @description An assignment `id`. */
        AssignmentPath: components["schemas"]["Id"];
        /** @description An approval `id`. */
        ApprovalPath: components["schemas"]["Id"];
        /** @description A workflow instance `id`. */
        WorkflowInstancePath: components["schemas"]["Id"];
        /** @description A check-policy version number, as `listCheckPolicyVersions` gives it. */
        CheckPolicyVersionPath: number;
        /** @description A branch view: that branch's latest builds, and the default branch's where it didn't rebuild. Absent: the default branch's. */
        ContextBranch: string;
        PageSize: number;
        /** @description The `next_page_token` of the previous page. */
        PageToken: string;
        /** @description The `ETag` the change is based on. */
        IfMatch: string;
        /** @description When sent, the `ETag` the change is based on. */
        IfMatchOptional: string;
        /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
        IfMatchSingleton: string;
        IdempotencyKey: string;
        /** @description WebAuthn ceremony state set by the matching challenge operation. */
        CeremonyCookie: string;
    };
    requestBodies: never;
    headers: {
        /** @description Opaque version of the resource, for `If-Match`. */
        ETag: string;
        /** @description URL of the created resource. */
        Location: string;
        /** @description `private, max-age=31536000, immutable`: content-addressed, never changes. */
        ImmutableCacheControl: string;
        /** @description `true` when this response replays an earlier request with the same `Idempotency-Key`. */
        IdempotentReplayed: "true";
        /** @description `__Host-glossa_session=…; Path=/; HttpOnly; Secure; SameSite=Lax` */
        SessionCookie: string;
        /** @description Expires the session cookie. */
        ClearedSessionCookie: string;
        /** @description `__Host-glossa_webauthn=…; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=300` */
        CeremonyCookie: string;
    };
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    getMeta: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The deployment's facts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Meta"];
                };
            };
        };
    };
    requestMagicLink: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["EmailRequest"];
            };
        };
        responses: {
            /** @description Accepted; a link is on its way if the address can receive one. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            404: components["responses"]["NotFound"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    redeemMagicLink: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TokenRedemption"];
            };
        };
        responses: {
            200: components["responses"]["SessionStarted"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            404: components["responses"]["NotFound"];
        };
    };
    register: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Registration"];
            };
        };
        responses: {
            /** @description Accepted; a verification link is on its way. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    signInWithPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PasswordSignIn"];
            };
        };
        responses: {
            200: components["responses"]["SessionStarted"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    requestPasswordReset: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["EmailRequest"];
            };
        };
        responses: {
            /** @description Accepted. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            404: components["responses"]["NotFound"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    resetPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PasswordReset"];
            };
        };
        responses: {
            /** @description Password changed; every session was revoked. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            404: components["responses"]["NotFound"];
        };
    };
    beginPasskeySignIn: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["EmailRequest"];
            };
        };
        responses: {
            /** @description Request options. */
            200: {
                headers: {
                    "Set-Cookie": components["headers"]["CeremonyCookie"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasskeyOptions"];
                };
            };
            400: components["responses"]["BadRequest"];
            404: components["responses"]["NotFound"];
        };
    };
    finishPasskeySignIn: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: {
                /** @description WebAuthn ceremony state set by the matching challenge operation. */
                "__Host-glossa_webauthn"?: components["parameters"]["CeremonyCookie"];
            };
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WebAuthnResponse"];
            };
        };
        responses: {
            200: components["responses"]["SessionStarted"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
        };
    };
    signOut: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Signed out. */
            204: {
                headers: {
                    "Set-Cookie": components["headers"]["ClearedSessionCookie"];
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    signOutEverywhere: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Signed out everywhere. */
            204: {
                headers: {
                    "Set-Cookie": components["headers"]["ClearedSessionCookie"];
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    startDeviceAuthorization: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeviceAuthorizationRequest"];
            };
        };
        responses: {
            /** @description The codes to show and poll with. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviceAuthorization"];
                };
            };
            400: components["responses"]["BadRequest"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    getDeviceAuthorization: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description The code the device shows, with or without its hyphen, in any case. */
                user_code: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The pending request. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviceAuthorizationView"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            404: components["responses"]["NotFound"];
        };
    };
    decideDeviceAuthorization: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeviceApproval"];
            };
        };
        responses: {
            /** @description Decided. The device's next poll learns the outcome. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    redeemDeviceAuthorization: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeviceSessionRequest"];
            };
        };
        responses: {
            /** @description Signed in. Send `access_token` as `Authorization: Bearer`. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeviceSession"];
                };
            };
            400: components["responses"]["BadRequest"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    exchangeGitHubOIDCToken: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GitHubOIDCExchange"];
            };
        };
        responses: {
            /** @description The CI token, with its secret shown once. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CIToken"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            503: components["responses"]["Unavailable"];
        };
    };
    getMe: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The person. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Me"];
                };
            };
            401: components["responses"]["Unauthenticated"];
        };
    };
    beginPasskeyRegistration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Creation options. */
            200: {
                headers: {
                    "Set-Cookie": components["headers"]["CeremonyCookie"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasskeyOptions"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listPasskeys: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of passkeys. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasskeyList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
        };
    };
    finishPasskeyRegistration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: {
                /** @description WebAuthn ceremony state set by the matching challenge operation. */
                "__Host-glossa_webauthn"?: components["parameters"]["CeremonyCookie"];
            };
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PasskeyRegistration"];
            };
        };
        responses: {
            /** @description The passkey was added. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Passkey"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    deletePasskey: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A passkey `id` (its credential ID, base64url). */
                passkey: components["parameters"]["PasskeyPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    beginTotpEnrollment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The secret to add to an authenticator app. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TotpEnrollment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
        };
    };
    confirmTotpEnrollment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TotpCode"];
            };
        };
        responses: {
            /** @description TOTP is now required for password sign-in. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
        };
    };
    disableTotp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TotpCode"];
            };
        };
        responses: {
            /** @description TOTP is off. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
        };
    };
    previewMessage: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["MessagePreviewRequest"];
            };
        };
        responses: {
            /** @description What the kernel made of the source. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessagePreview"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    listTenants: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of tenants. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TenantList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
        };
    };
    createTenant: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateTenant"];
            };
        };
        responses: {
            /** @description The organization. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getTenant: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The tenant. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listMembers: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of members. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MemberList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    addMember: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AddMember"];
            };
        };
        responses: {
            /** @description The invitation. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Member"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getMember: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The member. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Member"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    removeMember: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateMember: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateMember"];
            };
        };
        responses: {
            /** @description The updated member. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Member"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listTokens: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of tokens. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TokenList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createToken: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateToken"];
            };
        };
        responses: {
            /**
             * @description The token and its secret. A replayed request returns the token
             *     without `secret`.
             */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CreatedToken"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An API token `id` (never its secret). */
                token: components["parameters"]["TokenPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The token. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Token"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    revokeToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An API token `id` (never its secret). */
                token: components["parameters"]["TokenPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Revoked. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listGroups: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of groups. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GroupList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createGroup: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GroupName"];
            };
        };
        responses: {
            /** @description The group. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Group"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getGroup: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The group. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Group"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteGroup: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    renameGroup: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GroupName"];
            };
        };
        responses: {
            /** @description The renamed group. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Group"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    putGroupMember: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The group. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Group"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    removeGroupMember: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A group `id`. */
                group: components["parameters"]["GroupPath"];
                /** @description A member `id`. */
                member: components["parameters"]["MemberPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listVendors: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of vendors. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["VendorList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createVendor: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateVendor"];
            };
        };
        responses: {
            /** @description The vendor. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Vendor"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getVendor: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A vendor `id`. */
                vendor: components["parameters"]["VendorPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The vendor. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Vendor"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteVendor: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A vendor `id`. */
                vendor: components["parameters"]["VendorPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    updateVendor: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A vendor `id`. */
                vendor: components["parameters"]["VendorPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateVendor"];
            };
        };
        responses: {
            /** @description The vendor. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Vendor"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listProjects: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of projects. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createProject: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateProject"];
            };
        };
        responses: {
            /** @description The project. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Project"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getProject: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The project. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Project"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteProject: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateProject: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateProject"];
            };
        };
        responses: {
            /** @description The updated project. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Project"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listApplications: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of applications. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ApplicationList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createApplication: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateApplication"];
            };
        };
        responses: {
            /** @description The application. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Application"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getApplication: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An application `id`. */
                application: components["parameters"]["ApplicationPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The application. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Application"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteApplication: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An application `id`. */
                application: components["parameters"]["ApplicationPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateApplication: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An application `id`. */
                application: components["parameters"]["ApplicationPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateApplication"];
            };
        };
        responses: {
            /** @description The updated application. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Application"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listMessages: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                namespace?: components["schemas"]["Namespace"];
                state?: components["schemas"]["MessageState"];
                /** @description Keys starting with this, e.g. `checkout.`. */
                key_prefix?: string;
                /** @description Only messages without a translation in this locale. */
                missing_in?: components["schemas"]["Locale"];
                /** @description Only messages whose translation in this locale is outdated. */
                outdated_in?: components["schemas"]["Locale"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of messages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessageList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createMessage: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateMessage"];
            };
        };
        responses: {
            /** @description The message. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listNamespaces: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of namespaces. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NamespaceList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    upsertMessages: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["MessageUpsert"];
            };
        };
        responses: {
            /** @description One result per item, in request order. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessageUpsertResult"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getMessage: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The message. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    updateMessage: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateMessage"];
            };
        };
        responses: {
            /** @description The updated message. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    reviseMessageSource: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReviseSource"];
            };
        };
        responses: {
            /** @description The message. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    obsoleteMessage: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The obsolete message. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    renameMessage: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RenameMessage"];
            };
        };
        responses: {
            /** @description The renamed message. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Message"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listSourceRevisions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of source revisions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SourceRevisionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listLocales: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of locales, by code. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LocaleList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    addLocale: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AddLocale"];
            };
        };
        responses: {
            /** @description The locale already existed. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectLocale"];
                };
            };
            /** @description The locale. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectLocale"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getLocale: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The locale. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectLocale"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    removeLocale: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    getFallbackGraph: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The fallback graph. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["FallbackGraph"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    putFallbackGraph: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PutFallbackGraph"];
            };
        };
        responses: {
            /** @description The fallback graph. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["FallbackGraph"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listMessageTranslations: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of translations. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TranslationList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getTranslation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The translation. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Translation"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    putTranslation: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PutTranslation"];
            };
        };
        responses: {
            /** @description The revised (or unchanged) translation. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Translation"];
                };
            };
            /** @description The new translation. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Translation"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["StructuralQAFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listUnitTMMatches: {
        parameters: {
            query?: {
                limit?: number;
                min_score?: number;
                /** @description The syntax of each `target_text`; by default the one the message's source is written in. */
                target_syntax?: components["schemas"]["Syntax"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The matches, best first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UnitTMMatches"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listUnitAISuggestions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The unit's suggestions, newest first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UnitAISuggestions"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listTranslationRevisions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of revisions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TranslationRevisionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    reviewTranslation: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
                /** @description A locale code; canonicalized before use. */
                locale: components["parameters"]["LocalePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReviewTranslation"];
            };
        };
        responses: {
            /** @description The reviewed translation. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Translation"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listProjectTranslations: {
        parameters: {
            query: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A locale to list; repeat for several (`locale=de&locale=fr`). */
                locale: components["schemas"]["Locale"][];
                /** @description Only translations in these review states; repeatable. */
                state?: components["schemas"]["ReviewState"][];
                /**
                 * @description Only translations with these provenances; repeatable. `ai`
                 *     is the platform translating on a person's behalf and `agent`
                 *     an autonomous agent writing through MCP on a long-lived
                 *     token — different origins since they became different
                 *     values, and this is what makes the difference answerable
                 *     without SQL. Translations written through MCP before `agent`
                 *     existed keep `ai` and are not rewritten; their
                 *     `origin_detail` still says `{"via": "mcp"}`.
                 */
                origin?: components["schemas"]["Origin"][];
                /** @description `true`: only outdated translations; `false`: only current ones. */
                outdated?: boolean;
                namespace?: components["schemas"]["Namespace"];
                /** @description Keys starting with this, e.g. `checkout.`. */
                key_prefix?: string;
                /** @description Only translations of `active` (or `obsolete`) messages. */
                message_state?: components["schemas"]["MessageState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of translations. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectTranslationList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getTranslationStats: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The summary. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TranslationStats"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    importTranslations: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TranslationImport"];
            };
        };
        responses: {
            /** @description One result per item, in request order. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TranslationImportResult"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listEnvironments: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of environments. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["EnvironmentList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createEnvironment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateEnvironment"];
            };
        };
        responses: {
            /** @description The environment. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Environment"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    getEnvironment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The environment. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Environment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    updateEnvironment: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateEnvironment"];
            };
        };
        responses: {
            /** @description The updated environment. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Environment"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    promoteRelease: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Promotion"];
            };
        };
        responses: {
            /** @description The environment, now serving the release. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Environment"];
                };
            };
            /**
             * @description Held for approval: a release request was made and no pointer
             *     moved. `Location` is the request.
             */
            202: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseHeld"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    previewRelease: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The preview. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleasePreview"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    rollbackEnvironment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Rollback"];
            };
        };
        responses: {
            /** @description The environment, now serving the earlier release. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Environment"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listDeployments: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of deployments. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeploymentList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listRollouts: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of rollouts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RolloutList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    startRollout: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["StartRollout"];
            };
        };
        responses: {
            /** @description The rollout, active. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Rollout"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getRollout: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The rollout. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Rollout"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    advanceRollout: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AdvanceRollout"];
            };
        };
        responses: {
            /** @description The rollout. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Rollout"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    completeRollout: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The rollout, completed. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Rollout"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            503: components["responses"]["Unavailable"];
        };
    };
    abortRollout: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description An environment `name`. */
                environment: components["parameters"]["EnvironmentPath"];
                /** @description A rollout `id`. */
                rollout: components["parameters"]["RolloutPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The rollout, aborted. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Rollout"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listReleaseRequests: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                environment?: components["schemas"]["EnvironmentName"];
                state?: components["schemas"]["ReleaseRequestState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of release requests. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseRequestList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getReleaseRequest: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The release request. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseRequest"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    decideReleaseRequest: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateApprovalDecision"];
            };
        };
        responses: {
            /** @description The approval with the decision recorded. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    withdrawReleaseRequest: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release request `id`. */
                release_request: components["parameters"]["ReleaseRequestPath"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["ReleaseRequestWithdrawal"];
            };
        };
        responses: {
            /** @description The release request, withdrawn. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseRequest"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listReleases: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of releases. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    publishRelease: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PublishRelease"];
            };
        };
        responses: {
            /** @description The release. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Release"];
                };
            };
            /**
             * @description Held for approval: the release was recorded and a release
             *     request made; no pointer moved. `Location` is the request.
             */
            202: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseHeld"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getRelease: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The release. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Release"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getReleaseDiff: {
        parameters: {
            query?: {
                /** @description The release `id` to compare with. */
                base?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The diff. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseDiff"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    getReleaseManifest: {
        parameters: {
            query: {
                environment: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The manifest (`glossa.manifest/v1`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseManifest"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getReleaseArtifact: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A release `id`. */
                release: components["parameters"]["ReleasePath"];
                /** @description The artifact's SHA-256, as the manifest names it. */
                digest: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The artifact (`glossa.artifact/v1`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReleaseArtifact"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    listReleaseSigningKeys: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The keys, active first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SigningKeys"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listDeliveryKeys: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of keys. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeliveryKeyList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createDeliveryKey: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateDeliveryKey"];
            };
        };
        responses: {
            /** @description The key. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeliveryKey"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    setDeliveryKeyScope: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A delivery key `id` (not the key itself). */
                delivery_key: components["parameters"]["DeliveryKeyPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeliveryKeyScope"];
            };
        };
        responses: {
            /** @description The key with its new scope. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeliveryKey"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    revokeDeliveryKey: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A delivery key `id` (not the key itself). */
                delivery_key: components["parameters"]["DeliveryKeyPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Revoked. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listPreviewOrigins: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The registered origins, in origin order. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PreviewOriginList"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    registerPreviewOrigin: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreatePreviewOrigin"];
            };
        };
        responses: {
            /** @description The registration. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PreviewOrigin"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    unregisterPreviewOrigin: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A registered preview origin's `id`. */
                preview_origin: components["parameters"]["PreviewOriginPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Unregistered. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createInContextGrant: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateInContextGrant"];
            };
        };
        responses: {
            /** @description The grant, with its secret shown once. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InContextGrant"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    lookupTranslationMemory: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TMLookup"];
            };
        };
        responses: {
            /** @description The matches, best first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TMLookupResult"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    searchTranslationMemory: {
        parameters: {
            query: {
                q: string;
                side?: "source" | "target";
                source_locale?: components["schemas"]["Locale"];
                target_locale?: components["schemas"]["Locale"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                all_projects?: boolean;
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Units containing the phrase. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TMConcordance"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listTranslationMemoryUnits: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                source_locale?: components["schemas"]["Locale"];
                target_locale?: components["schemas"]["Locale"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                /** @description A translation `id`. */
                translation?: components["schemas"]["Id"];
                state?: "active" | "retired" | "all";
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of units. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TMUnitList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getTranslationMemoryUnit: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A translation-memory unit `id`. */
                unit: components["parameters"]["TMUnitPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The unit. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TMUnit"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    retireTranslationMemoryUnit: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A translation-memory unit `id`. */
                unit: components["parameters"]["TMUnitPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Retired. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listTermConcepts: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                q?: string;
                locale?: components["schemas"]["Locale"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                domain?: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of concepts with their terms. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermConceptList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createTermConcept: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateTermConcept"];
            };
        };
        responses: {
            /** @description The concept. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermConcept"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getTermConcept: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The concept. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermConcept"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    replaceTermConcept: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReplaceTermConcept"];
            };
        };
        responses: {
            /** @description The concept. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermConcept"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    deleteTermConcept: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listTermConceptRevisions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A termbase concept `id`. */
                concept: components["parameters"]["ConceptPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of revisions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermConceptRevisionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    recognizeTerms: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TermRecognitionRequest"];
            };
        };
        responses: {
            /** @description The terms found, in text order. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TermRecognition"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    checkTerminology: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TerminologyCheckRequest"];
            };
        };
        responses: {
            /** @description The findings, source first, by position. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TerminologyCheck"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listProjectTerminologyFindings: {
        parameters: {
            query: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A target locale to check; repeat for several. */
                locale: components["schemas"]["Locale"][];
                /** @description Only translations in these review states; repeatable. Default every state but `rejected`. */
                state?: components["schemas"]["ReviewState"][];
                namespace?: components["schemas"]["Namespace"];
                /** @description Keys starting with this, e.g. `checkout.`. */
                key_prefix?: string;
                /**
                 * @description Exactly these message keys; repeatable, at most 50. For
                 *     asking about the messages on one screen — the in-product
                 *     editor asks about one — where `key_prefix` would also match
                 *     everything below the key. Combined with `key_prefix` and
                 *     `namespace` it narrows further.
                 */
                key?: components["schemas"]["MessageKey"][];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of findings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProjectTerminologyFindings"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listStyleGuides: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                locale?: components["schemas"]["Locale"];
                tenant_only?: boolean;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of guides. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StyleGuideList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createStyleGuide: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateStyleGuide"];
            };
        };
        responses: {
            /** @description The guide. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StyleGuide"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getStyleGuide: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The guide. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StyleGuide"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    replaceStyleGuide: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReplaceStyleGuide"];
            };
        };
        responses: {
            /** @description The guide. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StyleGuide"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    deleteStyleGuide: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listStyleGuideVersions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A style guide `id`. */
                style_guide: components["parameters"]["StyleGuidePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of versions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StyleGuideVersionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getEffectiveStyleGuide: {
        parameters: {
            query?: {
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                locale?: components["schemas"]["Locale"];
                namespace?: components["schemas"]["Namespace"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The effective style. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["EffectiveStyleGuide"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listAIProviders: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of providers. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProviderList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createAIProvider: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateAIProvider"];
            };
        };
        responses: {
            /** @description The provider. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProvider"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getAIProvider: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An AI provider `id`. */
                ai_provider: components["parameters"]["AIProviderPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The provider. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProvider"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteAIProvider: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An AI provider `id`. */
                ai_provider: components["parameters"]["AIProviderPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    updateAIProvider: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An AI provider `id`. */
                ai_provider: components["parameters"]["AIProviderPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateAIProvider"];
            };
        };
        responses: {
            /** @description The provider. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProvider"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    getAISettings: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The settings. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISettings"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    putAISettings: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
                "If-Match"?: components["parameters"]["IfMatchSingleton"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateAISettings"];
            };
        };
        responses: {
            /** @description The settings. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISettings"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    getAIPrices: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The prices. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIPrices"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    putAIPrices: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
                "If-Match"?: components["parameters"]["IfMatchSingleton"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PutAIPrices"];
            };
        };
        responses: {
            /** @description The prices. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIPrices"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    getAIBudget: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The budget. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIBudget"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listAISpend: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                since?: components["schemas"]["Timestamp"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of calls. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISpendList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAIRoutingPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The policy in effect. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIRoutingPolicyView"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    putAIRoutingPolicy: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
                "If-Match"?: components["parameters"]["IfMatchSingleton"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AIRoutingPolicy"];
            };
        };
        responses: {
            /** @description The policy. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIRoutingPolicyView"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    getProjectAIRoutingPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The policy in effect. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIRoutingPolicyView"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    putProjectAIRoutingPolicy: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
                "If-Match"?: components["parameters"]["IfMatchSingleton"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AIRoutingPolicy"];
            };
        };
        responses: {
            /** @description The policy. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIRoutingPolicyView"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    deleteProjectAIRoutingPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getProjectAISettings: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The settings. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProjectSettings"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    putProjectAISettings: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on: `"0"` (the ETag of unsaved defaults) writes only while nobody has saved the settings yet, and fails with 412 once someone has. */
                "If-Match"?: components["parameters"]["IfMatchSingleton"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateAIProjectSettings"];
            };
        };
        responses: {
            /** @description The settings. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIProjectSettings"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    createAIFill: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateAIFill"];
            };
        };
        responses: {
            /** @description The fill and its jobs' states. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIFill"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    previewAIFill: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateAIFill"];
            };
        };
        responses: {
            /** @description What the fill would do. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIFillPreview"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getAIFill: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A fill `id`. */
                ai_fill: components["parameters"]["AIFillPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The fill. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIFill"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    cancelAIFill: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A fill `id`. */
                ai_fill: components["parameters"]["AIFillPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The fill. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIFill"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listAIJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                state?: components["schemas"]["AIJobState"];
                locale?: components["schemas"]["Locale"];
                /** @description A fill `id`. */
                fill?: components["schemas"]["Id"];
                /** @description A message `id`. */
                message?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAIJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A job `id`. */
                ai_job: components["parameters"]["AIJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    cancelAIJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A job `id`. */
                ai_job: components["parameters"]["AIJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listAISuggestions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                status?: components["schemas"]["AISuggestionStatus"];
                locale?: components["schemas"]["Locale"];
                /** @description A message `id`. */
                message?: components["schemas"]["Id"];
                /** @description A job `id`. */
                job?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of suggestions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISuggestionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAISuggestion: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The suggestion. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISuggestion"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    acceptAISuggestion: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AcceptAISuggestion"];
            };
        };
        responses: {
            /** @description The accepted suggestion. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISuggestion"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    rejectAISuggestion: {
        parameters: {
            query?: never;
            header?: {
                /** @description When sent, the `ETag` the change is based on. */
                "If-Match"?: components["parameters"]["IfMatchOptional"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A suggestion `id`. */
                ai_suggestion: components["parameters"]["AISuggestionPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RejectAISuggestion"];
            };
        };
        responses: {
            /** @description The rejected suggestion. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISuggestion"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    getAIReviewQueue: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                locale?: components["schemas"]["Locale"][];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of the queue. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AISuggestionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listAIDisclosures: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A job `id`. */
                job?: components["schemas"]["Id"];
                /** @description A message `id`. */
                message?: components["schemas"]["Id"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                provider?: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of disclosures. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIDisclosureList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAIMetrics: {
        parameters: {
            query?: {
                since?: components["schemas"]["Timestamp"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The metrics. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIMetrics"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getAIEvalBaseline: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The baseline. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AIEvalBaseline"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listImportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of import jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createImportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ImportJobRequest"];
            };
        };
        responses: {
            /** @description The job, waiting for its file. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getImportJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    uploadImportFile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/octet-stream": string;
            };
        };
        responses: {
            /** @description The job, queued. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["PayloadTooLarge"];
            503: components["responses"]["Unavailable"];
        };
    };
    cancelImportJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listImportResults: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                status?: components["schemas"]["ImportResultStatus"];
                kind?: components["schemas"]["ImportResultKind"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An import job `id`. */
                import_job: components["parameters"]["ImportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of results. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportResultList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listExportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of export jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createExportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ExportJobRequest"];
            };
        };
        responses: {
            /** @description The job, queued. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getExportJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    cancelExportJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    downloadExportFile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An export job `id`. */
                export_job: components["parameters"]["ExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The file. */
            200: {
                headers: {
                    /** @description `attachment; filename="…"` */
                    "Content-Disposition"?: string;
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/octet-stream": string;
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            410: components["responses"]["Gone"];
            503: components["responses"]["Unavailable"];
        };
    };
    listTMImportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of import jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createTMImportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KnowledgeImportJobRequest"];
            };
        };
        responses: {
            /** @description The job, waiting for its file. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listTMExportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of export jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createTMExportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KnowledgeExportJobRequest"];
            };
        };
        responses: {
            /** @description The job, queued. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listTermbaseImportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of import jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createTermbaseImportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KnowledgeImportJobRequest"];
            };
        };
        responses: {
            /** @description The job, waiting for its file. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ImportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listTermbaseExportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["IntegrationJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of export jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createTermbaseExportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KnowledgeExportJobRequest"];
            };
        };
        responses: {
            /** @description The job, queued. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ExportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    receiveGitHubWebhook: {
        parameters: {
            query?: never;
            header: {
                /** @description The event name, e.g. `pull_request`. */
                "X-GitHub-Event": string;
                /** @description GitHub's delivery id; the inbox key. */
                "X-GitHub-Delivery": string;
                /** @description `sha256=` and the HMAC-SHA256 of the raw body under the App's webhook secret. */
                "X-Hub-Signature-256": string;
            };
            path?: never;
            cookie?: never;
        };
        /**
         * @description The raw delivery. GitHub sends `application/json`; Glossa reads
         *     the bytes exactly as they were signed and parses them only
         *     after the signature verifies, so the body is declared as an
         *     opaque stream.
         */
        requestBody: {
            content: {
                "application/octet-stream": string;
            };
        };
        responses: {
            /**
             * @description The delivery was verified and stored, or was a duplicate the
             *     inbox already holds. Nothing has been processed yet.
             */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WebhookAck"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            413: components["responses"]["PayloadTooLarge"];
            503: components["responses"]["Unavailable"];
        };
    };
    startGitHubInstall: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The state and where to send the person. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitHubInstallIntent"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["Unavailable"];
        };
    };
    listGitHubInstallations: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of installations. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitHubInstallationList"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["Unavailable"];
        };
    };
    completeGitHubInstall: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GitHubInstallCallback"];
            };
        };
        responses: {
            /** @description The installation, now this workspace's. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitHubInstallation"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            503: components["responses"]["Unavailable"];
        };
    };
    forgetGitHubInstallation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A GitHub installation `id` as Glossa issued it, not GitHub's number. */
                installation: components["parameters"]["InstallationPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The installation is gone from Glossa. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    listGitConnections: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description Only this installation's connections. */
                installation?: components["schemas"]["Id"];
                /** @description Only this project's connections. */
                project?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of connections. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitConnectionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["Unavailable"];
        };
    };
    createGitConnection: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GitConnectionRequest"];
            };
        };
        responses: {
            /** @description The connection. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitConnection"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            503: components["responses"]["Unavailable"];
        };
    };
    getGitConnection: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A Git connection `id`. */
                connection: components["parameters"]["ConnectionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The connection. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitConnection"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteGitConnection: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A Git connection `id`. */
                connection: components["parameters"]["ConnectionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The connection is gone. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateGitConnection: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A Git connection `id`. */
                connection: components["parameters"]["ConnectionPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["GitConnectionChange"];
            };
        };
        responses: {
            /** @description The connection. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GitConnection"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listContextBuilds: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description Only this application's builds (its slug). */
                application?: components["schemas"]["Slug"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of builds. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContextBuildList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createContextBuild: {
        parameters: {
            query: {
                /** @description The collector that wrote the document: `plugin` (@klarlabs-studio/glossa/unplugin), `extract` (`glossa extract`), `runtime` (capture and editor sessions) or `capture` (`glossa capture`). */
                source: components["schemas"]["ContextSource"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UsagesDocument"];
            };
        };
        responses: {
            /** @description The same document was uploaded before; its build. */
            200: {
                headers: {
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContextBuild"];
                };
            };
            /** @description The build, stored. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContextBuild"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            413: components["responses"]["PayloadTooLarge"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    listMessageUsages: {
        parameters: {
            query?: {
                /** @description A branch view: that branch's latest builds, and the default branch's where it didn't rebuild. Absent: the default branch's. */
                branch?: components["parameters"]["ContextBranch"];
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The message's current usages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessageUsages"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listUsages: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A branch view: that branch's latest builds, and the default branch's where it didn't rebuild. Absent: the default branch's. */
                branch?: components["parameters"]["ContextBranch"];
                /** @description A route pattern, exactly (`/checkout/[step]`). */
                route?: string;
                /** @description A component, exactly (`PaymentFooter`, `mail.(*Mailer).Send`). */
                component?: string;
                /** @description A file path relative to the project root, exactly. */
                file?: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of usages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContextUsageList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listUnusedMessages: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A branch view: that branch's latest builds, and the default branch's where it didn't rebuild. Absent: the default branch's. */
                branch?: components["parameters"]["ContextBranch"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of unused messages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UnusedMessageList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createCaptures: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "multipart/form-data": {
                    manifest: components["schemas"]["CapturesManifest"];
                } & {
                    [key: string]: string;
                };
            };
        };
        responses: {
            /** @description The same manifest was uploaded before; its build. */
            200: {
                headers: {
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CaptureUpload"];
                };
            };
            /** @description The build with its captures, stored. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CaptureUpload"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            413: components["responses"]["PayloadTooLarge"];
            429: components["responses"]["TooManyRequests"];
            503: components["responses"]["Unavailable"];
        };
    };
    getCaptureImage: {
        parameters: {
            query?: never;
            header?: {
                /** @description The `ETag` of a cached copy. */
                "If-None-Match"?: string;
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A capture `id`. */
                capture: components["parameters"]["CapturePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The image. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    "Cache-Control": components["headers"]["ImmutableCacheControl"];
                    [name: string]: unknown;
                };
                content: {
                    "image/png": string;
                };
            };
            /** @description The cached copy is the image. */
            304: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    "Cache-Control": components["headers"]["ImmutableCacheControl"];
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    listCaptureFindings: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description One region of the capture (`r_0`), as `locus.region` names it. */
                region?: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A capture `id`. */
                capture: components["parameters"]["CapturePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of the capture's findings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CaptureFindingList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listMessageCaptures: {
        parameters: {
            query?: {
                /** @description A branch view: that branch's latest builds, and the default branch's where it didn't rebuild. Absent: the default branch's. */
                branch?: components["parameters"]["ContextBranch"];
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A message `key` (`checkout.pay`). Keys are URL-safe as they are. */
                message: components["parameters"]["MessagePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The message's current captures. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MessageCaptures"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listCheckRuns: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description The ref the run was of — a branch, or an environment name. */
                branch?: string;
                /** @description The commit the run graded, as a full lowercase SHA. */
                commit?: string;
                conclusion?: components["schemas"]["CheckRunConclusion"];
                trigger?: components["schemas"]["CheckRunTrigger"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of check runs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckRunList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createCheckRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateCheckRun"];
            };
        };
        responses: {
            /** @description The run as it was stored, with the counts and the conclusion the policy reached. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckRun"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getCheckRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A check run `id`. */
                check_run: components["parameters"]["CheckRunPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The check run. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckRun"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listFindings: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A check run `id`. Without it, the newest run matching `branch` and `commit`. */
                run?: components["schemas"]["Id"];
                /** @description Read the newest run of this ref — a branch, or an environment name. */
                branch?: string;
                /** @description Read the newest run of this commit. */
                commit?: string;
                layer?: components["schemas"]["FindingLayer"];
                /** @description The severity as it stands now, so `waived` selects the accepted findings. */
                severity?: components["schemas"]["FindingSeverity"];
                /** @description The rule, exactly (`missing-argument`, `term_forbidden`). */
                code?: string;
                locale?: components["schemas"]["Locale"];
                namespace?: components["schemas"]["Namespace"];
                /** @description The message key the finding is about, exactly. */
                message?: components["schemas"]["MessageKey"];
                /** @description Only the waived findings, or only those no waiver accepts. Absent, both. */
                waived?: boolean;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of findings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["FindingList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listWaivers: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                fingerprint?: components["schemas"]["Fingerprint"];
                layer?: components["schemas"]["FindingLayer"];
                code?: string;
                /** @description The message key of the finding the waiver accepts, exactly. */
                message?: components["schemas"]["MessageKey"];
                /** @description Only the waivers that stand now, or only the revoked and expired ones. Absent, both. */
                active?: boolean;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of waivers. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WaiverList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createWaiver: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateWaiver"];
            };
        };
        responses: {
            /** @description The project already had a live waiver for this finding and reach; it now says this. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Waiver"];
                };
            };
            /** @description The waiver. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Waiver"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    revokeWaiver: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A waiver `id`. */
                waiver: components["parameters"]["WaiverPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Revoked. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getCheckPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The policy. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicyState"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    saveCheckPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SaveCheckPolicy"];
            };
        };
        responses: {
            /** @description The policy as it now stands, or — for a `dry_run` — as it would stand. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicySaved"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listCheckPolicyVersions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of policy versions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicyVersionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getCheckPolicyVersion: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A check-policy version number, as `listCheckPolicyVersions` gives it. */
                version: components["parameters"]["CheckPolicyVersionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The version. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicyVersion"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    exportCheckPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The canonical policy document. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicyDocument"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    importCheckPolicy: {
        parameters: {
            query?: {
                /**
                 * @description How long to pin the pull requests that predate the save.
                 *     Absent, 14 days; `0` pins nothing.
                 */
                grace_days?: number;
                /** @description Answer the impact preview and store nothing. */
                dry_run?: boolean;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CheckPolicyDocument"];
            };
        };
        responses: {
            /** @description The policy as it now stands, or — for a `dry_run` — as it would stand. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CheckPolicySaved"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    getQualitySummary: {
        parameters: {
            query?: {
                /** @description Narrow every per-locale number to this locale. */
                locale?: components["schemas"]["Locale"];
                /**
                 * @description Where "published" means, for the lead time. Absent,
                 *     `production`.
                 */
                environment?: components["schemas"]["EnvironmentName"];
                /**
                 * @description The start of the window for the AI decisions, the lead-time
                 *     samples, the check health and the trend. Absent, 30 days
                 *     ago — the same default `getAIMetrics` uses, so a caller who
                 *     reads both sees one month in both.
                 */
                since?: components["schemas"]["Timestamp"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The summary, as of `computed_at`. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["QualitySummary"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listLinguisticJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["LinguisticJobState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of linguistic-QA jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LinguisticJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createLinguisticJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LinguisticJobCreate"];
            };
        };
        responses: {
            /** @description The job this `Idempotency-Key` already made. A review costs money, so a retry finds it instead of starting a second one. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LinguisticJob"];
                };
            };
            /** @description The job, as it stands after the preflight. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LinguisticJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getLinguisticJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A linguistic-QA job `id`. */
                linguistic_job: components["parameters"]["LinguisticJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LinguisticJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    cancelLinguisticJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A linguistic-QA job `id`. */
                linguistic_job: components["parameters"]["LinguisticJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job, cancelled. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LinguisticJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listBranches: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                state?: components["schemas"]["BranchState"];
                /** @description Only the branch with this exact name. */
                name?: components["schemas"]["BranchName"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of branches. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BranchList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    upsertBranch: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpsertBranch"];
            };
        };
        responses: {
            /** @description The branch as it now stands. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Branch"];
                };
            };
            /** @description The branch was created. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Branch"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    pushBranchMessages: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["BranchPush"];
            };
        };
        responses: {
            /** @description The branch's status report, with a result per item. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BranchPushResult"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    getBranch: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The branch and its status report. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BranchStatus"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listBranchProposals: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of proposals. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ProposalList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    closeBranch: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The closed branch. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Branch"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    mergeBranch: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The merged branch. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Branch"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    setBranchPreview: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /**
                 * @description A branch `id`. Branch names may hold `/`, and an encoded slash
                 *     doesn't survive every proxy, so URLs never carry the name; find
                 *     the `id` with `listBranches?name=…`.
                 */
                branch: components["parameters"]["BranchPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["BranchPreview"];
            };
        };
        responses: {
            /** @description The branch, with its preview URL. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Branch"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listWorkflowDefinitions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of definitions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinitionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createWorkflowDefinition: {
        parameters: {
            query?: {
                /** @description The project `id` the definition belongs to. */
                project?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WorkflowDocument"];
            };
        };
        responses: {
            /** @description The definition at version 1. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinitionSaved"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["InvalidWorkflow"];
        };
    };
    getWorkflowDefinition: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The definition. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinition"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteWorkflowDefinition: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Deleted. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listWorkflowDefinitionVersions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of versions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinitionVersionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    saveWorkflowDefinitionVersion: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WorkflowDocument"];
            };
        };
        responses: {
            /** @description The definition at its new version. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinitionSaved"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["InvalidWorkflow"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    getWorkflowDefinitionVersion: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A workflow definition `id`. */
                workflow_definition: components["parameters"]["WorkflowDefinitionPath"];
                /** @description A workflow definition's version number. */
                version: components["parameters"]["WorkflowVersionPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The version. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowDefinitionVersion"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    lintWorkflowDefinition: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WorkflowDocument"];
            };
        };
        responses: {
            /** @description The findings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowLintResult"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    listWorkflowBindings: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of bindings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowBindingList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    createWorkflowBinding: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateWorkflowBinding"];
            };
        };
        responses: {
            /** @description The binding. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowBinding"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            /** @description The binding cannot be made (`invalid_workflow_binding`, `workflow_definition_out_of_scope`). */
            422: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["Problem"];
                };
            };
        };
    };
    deleteWorkflowBinding: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow binding `id`. */
                workflow_binding: components["parameters"]["WorkflowBindingPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Removed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    resolveWorkflow: {
        parameters: {
            query?: {
                subject?: components["schemas"]["WorkflowSubject"];
                /** @description The translation unit's locale. */
                locale?: components["schemas"]["Locale"];
                /** @description The message's namespace. */
                namespace?: string;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The resolution. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowResolution"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listWorkflowInstances: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A definition `id`. */
                definition?: components["schemas"]["Id"];
                status?: components["schemas"]["WorkflowInstanceStatus"];
                locale?: components["schemas"]["Locale"];
                /** @description A message `key`. */
                message?: components["schemas"]["MessageKey"];
                /** @description A subject `id` (a message's or a release request's). */
                subject_id?: components["schemas"]["Id"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of instances. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowInstanceList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    getWorkflowInstance: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The instance. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowInstance"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    rebaseWorkflowInstance: {
        parameters: {
            query?: never;
            header: {
                /** @description The `ETag` the change is based on. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["RebaseWorkflowInstance"];
            };
        };
        responses: {
            /** @description The instance on its new version. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowInstance"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            /** @description The rebase is refused (`invalid_workflow_rebase`, `workflow_rebase_state_missing`, `workflow_rebase_state_final`); the instance is unchanged. */
            422: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["Problem"];
                };
            };
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listWorkflowTransitions: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
                /** @description A workflow instance `id`. */
                workflow_instance: components["parameters"]["WorkflowInstancePath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of transitions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WorkflowTransitionList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    listAssignments: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                /** @description A message `key`; needs `project`. */
                message?: components["schemas"]["MessageKey"];
                locale?: components["schemas"]["Locale"];
                state?: components["schemas"]["AssignmentState"];
                /** @description Only the caller's own work. */
                mine?: boolean;
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of assignments, oldest first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssignmentList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createAssignment: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateAssignment"];
            };
        };
        responses: {
            /** @description The assignment, `open`. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Assignment"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getAssignmentReport: {
        parameters: {
            query?: {
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                /** @description A vendor `id`. */
                vendor?: components["schemas"]["Id"];
                /** @description Only assignments completed at or after this time. */
                since?: components["schemas"]["Timestamp"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The rows, sorted by assignee and locale. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssignmentReport"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    getAssignment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The assignment; the `ETag` is its version. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Assignment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    acceptAssignment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The assignment, `accepted`. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Assignment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    completeAssignment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The assignment, `done`. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Assignment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    declineAssignment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An assignment `id`. */
                assignment: components["parameters"]["AssignmentPath"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["AssignmentDecline"];
            };
        };
        responses: {
            /** @description The assignment, `declined`. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Assignment"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listApprovals: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                /** @description A message `key`; needs `project`. */
                message?: components["schemas"]["MessageKey"];
                locale?: components["schemas"]["Locale"];
                subject?: components["schemas"]["WorkflowSubject"];
                state?: components["schemas"]["ApprovalState"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of approvals, oldest first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ApprovalList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createApproval: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateApproval"];
            };
        };
        responses: {
            /** @description The approval, `pending`. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getApproval: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An approval `id`. */
                approval: components["parameters"]["ApprovalPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The approval. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    decideApproval: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An approval `id`. */
                approval: components["parameters"]["ApprovalPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateApprovalDecision"];
            };
        };
        responses: {
            /** @description The approval with the decision recorded. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    importV0History: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description A project `id`. */
                project: components["parameters"]["ProjectPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["V0HistoryImport"];
            };
        };
        responses: {
            /** @description What the import recorded. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["V0HistoryReport"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    listAuditEntries: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
                /** @description Entries that happened at or after this instant. */
                from?: components["schemas"]["Timestamp"];
                /** @description Entries that happened before this instant. */
                to?: components["schemas"]["Timestamp"];
                first_sequence?: number;
                last_sequence?: number;
                actor?: string;
                action?: string;
                /** @description A project `id`. */
                project?: components["schemas"]["Id"];
                source?: components["schemas"]["AuditSource"];
                aggregate_type?: string;
                aggregate_id?: string;
                order?: "desc" | "asc";
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of entries. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditEntryList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAuditEntry: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description The entry's place in the tenant's chain. */
                sequence: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The entry. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditEntry"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listAuditExportJobs: {
        parameters: {
            query?: {
                page_size?: components["parameters"]["PageSize"];
                /** @description The `next_page_token` of the previous page. */
                page_token?: components["parameters"]["PageToken"];
            };
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of audit export jobs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditExportJobList"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
        };
    };
    createAuditExportJob: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AuditExportJobCreate"];
            };
        };
        responses: {
            /** @description The job, queued. */
            201: {
                headers: {
                    Location: components["headers"]["Location"];
                    "Idempotent-Replayed": components["headers"]["IdempotentReplayed"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditExportJob"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getAuditExportJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The job. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditExportJob"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    downloadAuditExportEntries: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description entries.jsonl. */
            200: {
                headers: {
                    /** @description `attachment; filename="entries.jsonl"` */
                    "Content-Disposition"?: string;
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/jsonl": string;
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            410: components["responses"]["Gone"];
            503: components["responses"]["Unavailable"];
        };
    };
    downloadAuditExportManifest: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A tenant `id`. */
                tenant: components["parameters"]["TenantPath"];
                /** @description An audit export job `id`. */
                audit_export_job: components["parameters"]["AuditExportJobPath"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description manifest.json (`glossa.audit/v1`). */
            200: {
                headers: {
                    /** @description `attachment; filename="manifest.json"` */
                    "Content-Disposition"?: string;
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AuditExportManifest"];
                };
            };
            401: components["responses"]["Unauthenticated"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            410: components["responses"]["Gone"];
            503: components["responses"]["Unavailable"];
        };
    };
}
