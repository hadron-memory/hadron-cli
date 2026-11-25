# Code-entry login for hosts with no browser (hadron-cli#3180)

Design as built. Governing spec: `cor:aut:030:01`, "A code-entry completion for a host with no browser". Server half: hadron-server#5125.

## Behavior

- **Flag:** `hadron auth login --no-browser` enters code-entry mode. Mutually exclusive with `--with-token`.
- **Client-side choice:** without the flag, when the browser cannot be opened the CLI stops waiting for the redirect (it can never arrive) and takes the code by hand. The server never infers the mode; the CLI asks for the code page with `code_delivery=page` on the authorize request.
- **Default unchanged:** loopback redirect. A code pasted while the redirect is awaited also completes the login (whichever comes first wins; the paste reader stops when login returns).
- **No listener in code-entry mode:** the client still registers a loopback-shaped `redirect_uri` (the server validates it at the exchange) but only reserves the port to name it; nothing listens and the server sends nothing there.
- **Exchange:** the pasted code, trimmed of surrounding whitespace and otherwise opaque (no parsing of its shape), goes to `POST /oauth/token` with the CLI's PKCE verifier and registered `redirect_uri`, exactly as today.

## Refusals

- **Server refuses the code** (`400 invalid_grant`, whatever the reason): one fixed line, exit 1. The CLI adds no text that tells wrong, expired, used, exhausted or mismatched apart, and counts nothing: the five-entry limit is the server's.
- **No terminal to paste into** (stdin not a terminal): exit 2 at once, pointing to `--with-token` / `HADRON_TOKEN`; before any request when the flag is given.
- **Timeout:** the wait for a code is bounded by the login timeout with the existing Cancelled answer.

## Secrets

The code is read without echo (`term.ReadPassword`; the terminal is restored if the wait is abandoned), never printed, logged or put in an error. `--json` output is unchanged.

## Not done

A server that predates the code page ignores `code_delivery` and redirects to an address nothing listens on, so no code is shown; the help says the mode needs a server that offers the page. A date floor is to be added once the server PR has merged.
