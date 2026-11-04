# cli#916 C7: App install memory

Refs #916, C7 only, under dispatch6463114409. Provider inspected at server
main59ec40579; the committed SDL already contains installMemory and both
install inputs. No new platform rule or spec citation: this implements the
merged install-memory contract. Other umbrella cuts stay separate.

app agent customize is a read, despite its name. It emits a three-field memory
record or null; null is concealed absence/unavailability, never proof that the
App has not customised the Agent. No memory or node is written by this command.

node add/update/get/list gain an explicit --install App/Agent pair, excluding
--memory by flag presence. Bare or relative entity refs are refused; the
existing full-URN and entity-id forms are accepted. Get/update take only locs
inside the selected memory; a node id or qualified ref cannot override it.

Add passes native input.install to the existing kind-specific authoring door.
CreateNodeInput.memoryId becomes omitempty while retaining its string Go type:
all nonempty legacy memory selectors retain their exact bytes; install writes
omit it. No extra read is needed for the lazy first creation. Update resolves
installMemory first for existing node guards/door selection, then sends the
native install+loc selector (not an id) for a field update, including each
kind-specific door. --data-merge/--data-merge-file with --install is refused
with usage2 before any request, including mixed field+merge commands: the
provider updateNodeData accepts nodeRef only and cannot preserve the install
fence. Use --data/--data-file replacement through the native selector. Ordinary
non-install merges keep their existing path. Role clearing with --install
remains an explicit unsupported-selector refusal, never an id fallback.
No retry or client create is added. Provider merge authority is tracked in
hadron-server#2701; this client does not work around it.

Get/list resolve the memory's returned URN and reuse the existing read paths.
Null lookup exits4 before any node read/write, because an empty filter would
widen listing to every readable memory. Batch/prefix, raw/body-only, pagination,
JSON shapes, expected revisions and input-presence semantics are unchanged.
Memory metadata admission does not grant content admission or manager rights.

Author checks pin noncreating/null reads, all native selectors and omission,
invalid/empty/combined selectors with zero requests, successful body/list/update
paths, and the no-unscoped-null boundary. Independent seats remain required.


Exact new refusal mapping: INSTALL_TARGET_CHANGED is state conflict5. Existing
AGENT_NOT_INSTALLED remains usage2, INSTALL_MEMORY_NOT_FOUND maps to4 and
FORBIDDEN to8; command pins preserve their complete server messages. Source
names and hidden state are never enriched through a compensating lookup.

The earlier full-suite baseline defect cli#1140 was fixed by #1151 and is
included through the main merge. The full suite now runs without a skip.
The fix cycle pins all four create/update kind doors on the captured wire,
ordinary memoryId retention after omitempty, missing/empty selectors and the
pure InstallRef/ParseInstallTarget grammar. There is no node-add --link flag
in this tree; data/properties are carried in the same native create input.
