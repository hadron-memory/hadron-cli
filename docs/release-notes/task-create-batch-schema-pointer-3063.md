# `task create --batch-file`: the help names the schema types

Unreleased (cli#3063). `hadron team task create --help` now says where the field
names of a `--batch-file` item are listed: an item is the GraphQL input
`CreateAgentTasksItemInput`, its `task` is `CreateAgentTaskInput`, and both are in
the published schema
(<https://github.com/hadron-memory/hadron-cli/blob/main/schema/schema.graphql>; a
server newer than that file can have fields it does not list yet). Help text only:
no flag, no behaviour and no server change.
