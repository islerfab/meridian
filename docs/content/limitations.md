---
title: Limitations
weight: 4
icon: exclamation-circle
description: What Meridian doesn't do, on purpose and otherwise.
---

Every tool has a shape. Here is Meridian's, stated plainly, so you can find out now rather than three weeks into a migration.

This page is the list. The [FAQ]({{% relref "faq" %}}) covers why any of it is the way it is.

## Won't change

Consequences of the architecture. Changing them would mean a different tool.

**Sync is one way.** A rule reads from one calendar and writes to others, and edits at the destination never flow back. Two rules pointing opposite ways give you two independent mirrors rather than a merge: when the same event is edited on both sides, nobody reconciles the disagreement.

**Meridian only touches what it created.** Every event it writes carries an ownership marker, and reconciliation, garbage collection and `wipe` all filter on it. Your own events are invisible to it, as are another tool's. That's what makes it safe to point at a calendar you rely on, and also why it can't clean up a mess it didn't make.

**Hand-edited copies get overwritten.** Meridian recomputes what each copy should contain and rewrites anything that drifted, so an edit you make by hand survives until the next cycle and then it doesn't. Delete a copy and the next cycle puts it back. This is either self-healing or haunting, depending on whether you meant it.

**The sync window is bounded.** Only events overlapping `[now - 1 day, now + 90 days]` are created, updated or collected. Older copies accumulate as ordinary calendar history; [`meridian wipe`]({{% relref "reference/cli#meridian-wipe" %}}) is how you clear them out.

**One instance, one person.** Accounts, rules and credentials belong to a single deployment. A second person means a second instance with its own config and its own instance ID. There is no user model and no plan for one.

**It doesn't schedule anything.** Meridian copies events. It never looks for a free slot, moves an appointment, or negotiates availability between people.

**Recurrence depends on your provider.** Recurring events are expanded server-side and Meridian consumes the result. A provider that expands incorrectly is one Meridian fails loudly against rather than works around.

## Not yet

Genuine gaps rather than design positions. These may close.

| Gap | Where it bites |
|---|---|
| Google Calendar and CalDAV only | No Outlook, Exchange, or Microsoft 365 support |
| Colors aren't normalized across providers | You write Google's numeric `colorId` or CalDAV's CSS color name, depending on the destination |
| Attendees and RSVP status aren't copied | A mirrored meeting shows the event, not who accepted it |
| Conferencing links aren't copied | A mirrored meeting has no Zoom or Meet link attached |

## Worth knowing

**Birthdays are skipped.** Google injects contacts' birthdays into the `primary` calendar's event feed, and Meridian ignores them, because almost nobody means to mirror them. To sync them anyway, name Google's dedicated birthday calendar explicitly, as shown in [Write sync rules]({{% relref "guides/write-rules" %}}).

**The mass-delete guard reports rather than blocks.** An unusual share of deletions in one cycle fires a loud metric, and the deletions still go through. That's the intended behaviour, for reasons the [Design]({{% relref "design" %}}) page covers under the guards.
