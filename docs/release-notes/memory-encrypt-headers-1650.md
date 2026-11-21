Memory encryption sends its secret only in a per-request header, never a GraphQL
argument. Server header support must be deployed before releasing this client.

Passphrase files now require private permissions (chmod 600 on Unix). On Windows,
use --passphrase-file - to read stdin: file permission checks are unsupported.

Confirmed encryption remains successful if the response repeats the secret in
metadata. Only those metadata fields are blanked; JSON adds detailsWithheld:true
and retains isEncrypted:true. Text prints a fixed success receipt.
