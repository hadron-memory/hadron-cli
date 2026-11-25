# Code-entry login for hosts with no browser (hadron-cli#3180)

Design as built. Governing spec: `cor:aut:030:01`, "A code-entry completion for a host with no browser". Server half: hadron-server#5125.

## Behavior

- **Flag:** `hadron auth login --no-browser` enters code-entry mode. Mutually exclusive with `--with-token`.
- **Client-side choice:** without the flag, when the browser cannot be opened the CLI prints only the code-entry address (the default one could not work: its code would go to a loopback address nobody can reach) and takes the code by hand. The server never infers the mode; the CLI asks for the code page with `code_delivery=page` on the authorize request. The loopback listener stays up in this fallback, so a redirect arriving over a forwarded port still completes the login, and a refused paste ends it.
- **Default unchanged:** loopback redirect. A code pasted while the redirect is awaited also completes the login (whichever comes first wins). A refused paste there is reported and the wait for the redirect goes on.
- **No listener under `--no-browser`:** the client still registers a loopback-shaped `redirect_uri` (the server validates it at the exchange) but only reserves the port to name it; nothing listens and the server sends nothing there.
- **Exchange:** the pasted code, trimmed of surrounding whitespace and otherwise opaque (no parsing of its shape), goes to `POST /oauth/token` with the CLI's PKCE verifier and registered `redirect_uri`, exactly as today.

## Refusals

- **Server refuses the code** (`400 invalid_grant`, whatever the reason): one fixed line, exit 1. The CLI adds no text that tells wrong, expired, used, exhausted or mismatched apart, and counts nothing: the five-entry limit is the server's.
- **No terminal to paste into** (stdin not a terminal): exit 2 at once, pointing to `--with-token` / `HADRON_TOKEN`; before any request when the flag is given.
- **Timeout:** the wait for a code is bounded by the login timeout with the existing Cancelled answer.

## Secrets

The code is read without echo, never printed, logged or put in an error. `--json` output is unchanged.

The terminal read (`readHiddenLine`) polls the descriptor in 100 ms steps and reads only what is already there, so it never blocks in `read(2)`. When the wait ends (the redirect won, the timeout, Ctrl+C) it returns at once, restores echo and has consumed nothing, so the next prompt (install-skills) sees its own answer. `auth login` turns SIGINT/SIGTERM into context cancellation (`signal.NotifyContext`) so Ctrl+C takes that path and ends Cancelled. Windows has no pollable console descriptor: there the read is `term.ReadPassword` in a goroutine, the console mode is restored on abandon, and a line typed afterwards may still be taken by it.

## Not done

A server that predates the code page ignores `code_delivery` and redirects to an address nothing listens on, so no code is shown; the help says the mode needs a server that offers the page. A date floor is to be added once the server PR has merged.
