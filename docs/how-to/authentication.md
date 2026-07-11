# Authentication

`hadron` authenticates to a Hadron server with a long-lived `hdr_user_*` token.
**None of the ways to obtain one require the web portal** — `hadron-server`
hosts its own OAuth server (spec `025-oauth-for-mcp`), so a self-hosted server is
all you need.

Point the CLI at your server first (the default is `https://mcp.hadronmemory.com`):

```sh
hadron config set server https://hadron.example.com     # persistent
# or per-invocation:  hadron --server https://hadron.example.com <cmd>
```

## 1. Browser sign-in (interactive)

```sh
hadron auth login
```

Runs OAuth 2.0 authorization-code + PKCE against the **server**: the CLI reads
the server's `/.well-known/oauth-authorization-server`, dynamically registers a
one-shot client bound to a `127.0.0.1` loopback redirect, opens the server's
consent screen in your browser, and exchanges the code for a token (stored in
your OS keychain). Same shape as `gh auth login`; the portal is never involved.

## 2. Host bootstrap (no browser / air-gapped)

When there's no browser — CI images, air-gapped boxes, first-run setup — mint the
first token on the server host itself:

```sh
# on the hadron-server host
pnpm admin:mint-token --email you@example.com     # prints hdr_user_… once
```

Then hand it to the CLI:

```sh
echo "$TOKEN" | hadron auth login --with-token     # store in the keychain
# or, ephemerally, for a single invocation:
HADRON_TOKEN=hdr_user_… hadron memory ls
```

(See [hadron-server#303](https://github.com/hadron-memory/hadron-server/pull/303).)

## 3. Mint more tokens from the CLI

Once signed in via (1) or (2), mint additional personal access tokens for CI and
automation — no browser needed:

```sh
hadron auth token create --label ci-deploy     # prints the raw key ONCE
hadron auth token ls
echo "$TOKEN" | hadron auth token validate     # check a token: exit 0 valid / 3 rejected
hadron auth token revoke <id>
```

The raw key is shown once (the server stores only its hash). Tokens are
user-scoped — an app or agent key can't manage user tokens.

`token validate` reads a token from standard input (so it never lands in your
shell history) and reports whether it still authenticates — useful in CI to
fail fast on an expired or revoked credential. For a valid user key it names the
exact key that authenticated (preview, label, last-used); a valid **App** key is
reported as valid too (`principalType: APP`), not a false "invalid".

## Inspecting and clearing credentials

```sh
hadron auth status      # am I signed in?  exit 0 yes / 3 no
hadron auth whoami
hadron auth logout
```
