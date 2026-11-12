package auth

import authpkg "github.com/hadron-memory/hadron-cli/internal/auth"

// PasskeyOpenBrowser is the launcher `revoke-page --open` calls. Tests swap it.
var PasskeyOpenBrowser = authpkg.OpenInBrowser
