# Node type union filter (cli#1528)

`node list --types` is a repeatable/comma-separated StringSlice. It sends
NodeFilter.nodeTypes unchanged: no trim, deduplication, case fold, client cap,
comment refusal or mutual-exclusion policy. The server owns BAD_USER_INPUT
for empty/both/over20/comment, mapped to exit2. Search and listing use the
same filter and existing page loop; no output DTO or retry behavior changes.

Generated []string plus omitempty would lose explicit empty. A field binding
NodeTypeList = *[]string preserves nil omission and pointer-to-empty [].
All five NodeFilter operation annotations use the binding consistently;
no schema snapshot or selected field change. Other callers leave it nil.
The app nodes door remains outside this gate, retaining its own gap.

Red commit44b5adbd: missing flag fails wire cases and server-refusal reach
assertion on main ebed377b. Tests pin unset alongside other filters, exact
comma/repeated values, explicit empty, both flags, search, and each refusal
input including21 duplicates. Compiled-fake QA is independent at freeze.
Governing cor:api:090:02 is Proposed/built server3114 per Vera6653942512;
flag shape is CLI-owned. No access change or local authorization rule.
