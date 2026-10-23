# Calendar reads (#230 first slice)

Gate 6368860551 accepts Jane6359115667's four reads: calendar list, events,
event get, freebusy. Refs #230, which stays open for separately gated effects.
No create/update/delete/respond, invites, notifications, consent or discovery.

Provider inspected at merged server fc522d9f4c309de49c41142794e9b0f041e10d33:
resolvers.query.session.ts calendars/calendarEvents/calendarEvent/calendarSchedule
use runOwnedEmailOperation and the existing owner-or-App-ConnectionGrant gate.
Org-admin connection metadata is not authority to read a mailbox. The CLI adds
no authorization rule, guesses no connection or calendar and makes one explicit
read per invocation. Existing committed SDL supports all selections.

Flags map --from/--to to start/end and --schedule to schedules. --connection is
required. Empty/whitespace refs, event IDs and passed optional filters are usage
errors before client construction. RFC3339 bounds require offsets; both supplied
bounds must be strictly ordered by instant. Strings retain supplied offsets after
whitespace trim. All-day output dates never pass through time parsing/conversion.
Events sends an explicit top=25 default, range 1..100. It is one bounded bare list,
not an exhaustible page: no total or continuation guarantee. Other lists are the
returned provider lists. No extra discovery or admin fallback request is made.

Explicit DTOs mirror CalendarInfo, CalendarEvent and ScheduleFreeBusy: every
selected nullable field remains null, every array remains []. start/end keep
{date,dateTime,timeZone}, date is a YYYY-MM-DD string; timed instants and zones
remain separate. Organizer is null/object; attendees carry address/name/optional/
response, with uppercase enum strings including NONE, and nullable myResponse.
Freebusy has only schedule id/error and busy start/end windows; a schedule error
is retained as data, not an invented all-free answer. No derived event contents.
Null required response objects/arrays/rows fail rather than print partial data.

Human rendering labels pretty JSON as external data and identifies explicit
connection scope; JSON mode is only the stable DTO. Strings are escaped by the
encoder. Refusal sentences and existing api.MapError categories remain; unknown
EMAIL_TOOL_* codes keep the existing fallback rather than guessed recovery or
retry. No shared error policy, schema/provider contract, spec law or mint change.

Validation uses synthetic serialized requests and output pins plus an independent
compiled loopback HTTP QA seat. No real calendar/mailbox/provider spend or stack.
