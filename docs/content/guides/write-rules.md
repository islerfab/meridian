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

There's a subtler trap here that meridian closes for you. Left alone, `work-to-private` would read the work calendar, find the Busy blocks that `private-to-work` just wrote there, and dutifully mirror them back to the private calendar as new events. Then the first rule mirrors *those* across, and so on. Delete the original and the loop hands it back to you.

So meridian skips any source event carrying a meridian marker, whoever wrote it. A copy is never treated as something worth copying. This is the zombie-resurrection guard, and it earned the name.

This is still two mirrors, not a merge. See [Limitations]({{% relref "limitations" %}}).

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
