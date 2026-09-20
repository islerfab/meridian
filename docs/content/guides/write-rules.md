---
title: Write sync rules
weight: 2
icon: adjustments
description: Worked examples for filters, transforms, the Busy pattern, and bidirectional pairs.
---

Worked examples for the patterns that come up most. The [configuration reference]({{% relref "reference/configuration" %}}) has the full schema underneath them.

## A faithful mirror

The simplest rule there is. Every field of every event, copied as-is:

```yaml
rules:
  - id: personal-to-family
    from: personal/main
    to: [family/shared]
```

Any `transform` field you leave unset copies the source, so a plain mirror needs no configuration at all.

## Show private time as Busy at work

The pattern most people come for: filter to working hours, then strip everything except a generic title.

```yaml
rules:
  - id: private-to-work
    from: private/main
    to: [work/main]
    filter:
      weekdays: [mon, tue, wed, thu, fri]
      window: "08:00-19:15"
      timezone: "Europe/Zurich" # required whenever weekdays or window is set
    transform:
      title: "Busy"
      description: "Synced from personal calendar via meridian."
      color: "8"
```

Filter fields AND together. An event has to fall on a listed weekday *and* overlap the window to match. All-day events are judged by `weekdays` alone, since time-windowing them would mean nothing.

## Force opacity on transparent events

Some calendars mark their own events as transparent, meaning free. A "focus time" calendar often does. If you want those to read as genuinely busy at the destination:

```yaml
transform:
  title: "Busy"
  transparent: false
```

`transparent` is the only boolean transform. Unset copies the source; `true` or `false` force it.

## Set who can see a copy

`transform` decides what a copy says. `visibility` is the other half of that question: who may read it at all. Both are independent of whether the copy blocks time, which is `transparent`'s job.

```yaml
rules:
  - id: work-to-private
    from: work/main
    to: [private/main]
    # A fuller mirror: titles and details survive, so readership matters.
    transform:
      visibility: private
```

Three values: `public`, `private` and `confidential`. They map onto Google's visibility field and iCalendar's `CLASS` property, so one rule means the same thing whichever provider owns the destination. Google accepts `confidential` and treats it as `private`, where it exists for compatibility.

Unset mirrors the source. The field earns its keep on mirrors that preserve real content, or when you'd rather colleagues didn't see an entry at all. A rule that already rewrites the title to `Busy` and drops the description has little left to hide, and can ignore it.

> [!WARNING]
> **`private` hides a copy from colleagues, not from whoever runs the calendar.** A Google Workspace administrator can read any event in the domain, and the operator of a CalDAV server can read its database. Where an employer seeing an event would matter, don't mirror the content: strip it to `Busy`, drop the description, or leave it out of the rule entirely.

## Recolour and nothing else

```yaml
rules:
  - id: shared-to-personal
    from: shared/family
    to: [personal/main]
    transform:
      color: "3" # Grape
```

`color` is the one transform field with no source value to fall back on. Omitting it means no colour is set at all, rather than copying whatever the source had.

## Bidirectional pairs

Two independent rules, each with its own `id`:

```yaml
rules:
  - id: private-to-work
    from: private/main
    to: [work/main]
    filter: { weekdays: [mon, tue, wed, thu, fri], window: "08:00-19:15", timezone: "Europe/Zurich" }
    transform: { title: "Busy" }

  - id: work-to-private
    from: work/main
    to: [private/main]
    # No filter: mirror the full variety of work events, meetings included.
    transform:
      description: "synced from work calendar\n---\n{{ .Description }}"
```

Ownership markers are scoped by instance and rule ID, so these two never fight over each other's copies.

There's a subtler trap here that Meridian closes for you. Left alone, `work-to-private` would read the work calendar, find the Busy blocks that `private-to-work` just wrote there, and dutifully mirror them back to the private calendar as new events. Then the first rule mirrors *those* across, and so on. Delete the original and the loop hands it back to you.

So Meridian skips any source event carrying a Meridian marker, whoever wrote it. A copy is never treated as something worth copying. This is the zombie-resurrection guard, and it earned the name.

This is still two mirrors, not a merge. See [Limitations]({{% relref "limitations" %}}).

## Skip meetings you declined

An invitation you turned down still sits on your calendar, still marked busy. Google keeps it and only hides it in its own web UI; the API hands it over like any other event. Mirror that calendar and the copy blocks a slot you told everyone you weren't using.

```yaml
filter:
  skipDeclined: true
```

Only invitations you personally declined are dropped. Events you were never invited to have no response to read, so they pass through untouched, and that covers most of an ordinary calendar.

On Google this works as written, because the API marks your own entry in the attendee list. CalDAV has no equivalent, so the account has to say which addresses are yours:

```yaml
accounts:
  - name: private
    type: caldav
    identities: [you@example.com]
```

Ask the server for it with `meridian identities private` rather than guessing. A rule that reads your response from a CalDAV source with no `identities` set is a startup error, because the alternative is a filter that silently matches nothing for as long as it runs.

> [!NOTE]
> Read responses from the calendar that owns them. Where a calendar is itself a mirror of another system, its copy of your answer is only as fresh as whatever sync populates it, and can lag the original by a good while.

## Show unanswered invitations without blocking time

Dropping declined meetings is one half. The other is that an invitation you haven't answered shouldn't hold a slot as firmly as one you committed to:

```yaml
filter:
  skipDeclined: true
transform:
  transparentForRSVP: [needsAction, tentative]
```

Copies of anything you left unanswered or marked as a maybe stop blocking time, while still appearing on the destination so you can see what's pending. Accepted meetings keep blocking, declined ones never arrive.

Name whichever responses you want freed. `[needsAction]` alone is stricter, treating a tentative yes as a real commitment. Four values are accepted: `needsAction`, `accepted`, `declined` and `tentative`.

The field only ever frees a slot. A response you didn't list keeps whatever transparency the source set, so an event the organiser already marked free stays free. Events you were never invited to have no response at all and are left alone entirely, which covers the bulk of an ordinary calendar.

It can't be combined with `transparent`, since that forces one answer for every event and this derives the answer per event; setting both is a startup error.

## Filters beyond the typed fields

Typed `filter` fields cover the common cases. For anything else, `filter.when` takes a [CEL](https://cel.dev/) expression over a small documented event schema, ANDed with the typed fields:

```yaml
filter:
  weekdays: [mon, tue, wed, thu, fri]
  window: "08:00-19:15"
  timezone: "Europe/Zurich"
  when: '!event.title.startsWith("[private]")'
```

Expressions are compiled and type-checked when the config loads, so a bad one stops the pod at startup rather than halfway through a sync. The [event schema reference]({{% relref "reference/configuration#cel-event-schema" %}}) lists every field an expression can see.

## Templating titles and descriptions

`title`, `description` and `location` accept a literal or a [Go template](https://pkg.go.dev/text/template) over the source event:

```yaml
transform:
  title: "[{{ .SourceCalendar }}] {{ .Title }}"
```

A plain literal like `"Busy"` is just a template with nothing to substitute. The special value `drop` empties a field instead of copying or templating it.

## Colours

Colour values aren't normalized across providers yet, so you write whatever the destination speaks.

{{% details title="Google destinations: numeric `colorId`" closed="true" %}}
The 11 event colours, as returned by the [`colors.get`](https://developers.google.com/workspace/calendar/api/v3/reference/colors/get) API, which is the live source of truth:

| `colorId` | Name | Swatch |
|---|---|---|
| `"1"` | Lavender | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#a4bdfc;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#a4bdfc` |
| `"2"` | Sage | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#7ae7bf;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#7ae7bf` |
| `"3"` | Grape | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#dbadff;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#dbadff` |
| `"4"` | Flamingo | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#ff887c;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#ff887c` |
| `"5"` | Banana | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#fbd75b;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#fbd75b` |
| `"6"` | Tangerine | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#ffb878;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#ffb878` |
| `"7"` | Peacock | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#46d6db;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#46d6db` |
| `"8"` | Graphite | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#e1e1e1;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#e1e1e1` |
| `"9"` | Blueberry | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#5484ed;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#5484ed` |
| `"10"` | Basil | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#51b749;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#51b749` |
| `"11"` | Tomato | <span style="display:inline-block;width:0.9em;height:0.9em;border-radius:50%;background:#dc2127;vertical-align:middle;border:1px solid rgba(128,128,128,.4)"></span> `#dc2127` |
{{% /details %}}

{{% details title="CalDAV destinations: RFC 7986 `COLOR`" closed="true" %}}
One of the [CSS3 extended colour keywords](https://www.w3.org/TR/css-color-3/#svg-color), such as `"green"`, `"darkslateblue"` or `"tomato"`, per [RFC 7986 §5.9](https://www.rfc-editor.org/rfc/rfc7986#section-5.9).

Client support is inconsistent across the CalDAV ecosystem. DAVx⁵ renders it; some desktop clients have historically ignored it entirely. Confirm your destination client does something with `COLOR` before relying on it.
{{% /details %}}

## Google's birthday events

Google quietly injects your contacts' birthdays into the `primary` calendar's event feed as `eventType: birthday`. Meridian skips them, on the grounds that nobody has ever wanted their work calendar to announce that it is Dave's birthday.

To sync them on purpose, name Google's dedicated birthday calendar explicitly:

```yaml
calendars:
  - name: birthdays
    id: "addressbook#contacts@group.v.calendar.google.com"
```

That calendar never shows up in an account's calendar list, not even with hidden calendars revealed, so it can't be discovered automatically. A rule has to ask for it by exactly this ID.
