# #1112 baseline templates

Captured by the new tests against main 7a262e4d8277d7f0d240297be9a786579bf960d3 BEFORE the production conversion (`UPDATE_METADATA_1112=1`). JSON files include clean and attacked original values; clean text has separate readable .txt files. Never recapture from the changed renderer to hide a regression.

Only fixture parameters are substituted: __FIXTURE_DIR__ is expanded to the current private temporary config/state directory, and __SERVER_URL__ to the ephemeral loopback server URL in clean preview text. The test then compares the complete actual output bytes to the instantiated baseline template. API values are not normalized. No live server, host operation or generated shell command is executed.

Host labels already have a 128-rune load-time limit. Their over-length test pins that existing rejection instead of claiming a new red display test. Malformed families are rejected by the existing family grammar. Other accepted config/state fields and synthetic server fields exercise the control/1,000-rune cases without weakening validation.
