package accountusage

// GLMSnippet is the status line script text a glm session installs (cli#3312);
// `team usage --help` prints it and a test runs it. It is the ONLY place the
// Z.ai endpoint is called, from the session that already holds the key: the
// token goes to curl on stdin (never argv), the host is the origin of
// ANTHROPIC_BASE_URL, only type/unit/number/percentage/nextResetTime of
// data.limits[] are kept (no token, no account identifier), a call is made at
// most once per 5 minutes (by the file's mtime and an attempt marker, so a
// failing endpoint is not retried on every refresh), only over https, with a
// unique temp file per run (concurrent runs never share one), and any failure (no key, curl
// error, a response without data.limits) leaves the previous file untouched.
const GLMSnippet = `d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hadron-usage"; f="$d/glm-quota.json"; mkdir -p "$d"
if [ -n "$ANTHROPIC_AUTH_TOKEN" ] && [ "${ANTHROPIC_BASE_URL#https://}" != "$ANTHROPIC_BASE_URL" ] &&
   [ -z "$(find "$f" "$f.try" -mmin -5 2>/dev/null)" ]; then
  touch "$f.try"
  t=$(mktemp "$f.XXXXXX") &&
  h=$(printf '%s' "$ANTHROPIC_BASE_URL" | sed -E 's#^(https://[^/]+).*#\1#') &&
  r=$(printf 'Authorization: %s\n' "$ANTHROPIC_AUTH_TOKEN" | curl -fsS --proto =https --max-time 5 -H @- "$h/api/monitor/usage/quota/limit" 2>/dev/null) &&
  printf '%s' "$r" | jq -ce '.data.limits | select(type == "array") | {observedAt: now|floor, limits: map({type, unit, number, percentage, nextResetTime})}' > "$t" 2>/dev/null &&
  [ -s "$t" ] && mv "$t" "$f"; rm -f "$t"
fi`
