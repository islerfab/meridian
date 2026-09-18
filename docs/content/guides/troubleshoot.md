---
title: Troubleshoot
weight: 4
icon: search
description: Symptoms, the log lines that go with them, and what to do.
---

Organised by what you saw, not by what it turned out to be. Every quoted string below appears verbatim in Meridian's own output, so searching this page for the text in front of you should land somewhere useful.

## The pod never becomes ready

`/readyz` returns `503` indefinitely and the log carries `auth probe failed`.

Meridian runs a read-only listing against every configured calendar before it will start reconciling, and it stays unready until all of them pass. This is the intended behaviour, not a startup race: an instance that never went ready has provably written nothing anywhere.

Look at the error class on the `auth probe failed` record:

- `authentication failed` — credentials are wrong or revoked. See below.
- `not found` — the calendar ID or path is wrong. A CalDAV `path` must be the collection path, not the full URL.
- `transient error` — network or provider trouble. It retries every interval on its own.

The pod stays scrapeable while unready, deliberately, because this is exactly when its metrics are worth reading.

## `authentication failed`

The one error class Meridian treats as fatal for a rule rather than retrying.

**Google.** Most often a revoked or expired refresh token. Check `myaccount.google.com/permissions` for a revocation. If your OAuth consent screen is still in **Testing** mode, tokens expire after seven days and no amount of retrying will help; publish to Production. Mint a new token with `meridian oauth` and update your Secret.

**CalDAV.** Usually an app-specific password that was rotated or never worked. If your provider enforces 2FA, an account password will authenticate to the web UI and fail here, which makes it look like Meridian's fault.

No config change is needed for either unless the env var name itself changed.

## The config won't load at all

Meridian parses strictly: an unknown key is a startup error rather than something silently ignored. Run `meridian validate --config rules.yaml` to get the same result without touching a provider.

| Message | Meaning |
|---|---|
| `field surprise not found in type ...` | A typo, or a key removed in a newer version. `expansion` was removed once client-side expansion was dropped. |
| `from "x/y" is not a configured calendar` | The rule references `account/calendar` that no account declares. Check both halves. |
| `duplicate rule id (marker collision)` | Two rules share an `id`. Since ownership markers are keyed on it, they'd fight over the same copies forever. |
| `timezone is required when window or weekdays are set` | A time filter without a timezone is ambiguous, so Meridian refuses to guess. |
| `window "..." (want HH:MM-HH:MM, not crossing midnight)` | Split a window spanning midnight into two rules. |
| `caldav-only fields set on google account` | Usually a copy-paste between two account blocks. |

## Nothing is appearing at the destination

Work down this list in order.

1. **Did the cycle run at all?** Check `meridian_last_successful_cycle_timestamp_seconds` for the rule. If it's stale or absent, this is a failure, not a filtering question.
2. **Did the fetch fail?** `source fetch failed, rule cycle aborted` means the rule made no writes on purpose. A failed fetch is an error, never an empty calendar.
3. **Is your filter excluding everything?** `weekdays` and `window` AND together, so an event has to match both. A weekday-only event outside your hours matches nothing.
4. **Is the event inside the sync window?** Only events overlapping `[now - 1 day, now + 90 days]` are considered. Something nine months out is invisible until it drifts closer.
5. **Are they birthdays?** Google injects contacts' birthdays into `primary`, and Meridian skips them by default.

## Copies keep coming back after I delete them

Working as intended. Shadows are derived state, so a missing one reads as damage and the next cycle repairs it.

To remove copies permanently, remove the rule that creates them, or run `meridian wipe rule <id>`. Deleting by hand is a conversation with a process that has more patience than you do.

## My edit to a copy keeps getting reverted

Also intended, and visible in `meridian_shadow_drift`.

Meridian hashes the content it means each shadow to hold and stores that hash in the shadow's own marker. When the observed content stops matching, it rewrites the intended version, a bounded number of times, then gives up and reports `meridian_shadow_drift_unrepairable` instead of rewriting forever.

If you want different content at the destination, change the rule's `transform`. The destination is not a place to store edits.

## `shadow drift: unrepairable after retries`

Meridian rewrote a shadow, read it back, and found it changed again. Repeatedly.

Two causes worth separating. Either somebody is editing those events by hand on a schedule, or the provider is normalising something Meridian writes, so the content never round-trips. The second is a bug worth reporting, and the op log's recorded hashes are what identify it.

`meridian wipe rule <id>` followed by a cycle rebuilds the affected shadows from scratch and rules out a stale marker.

## The mass-delete alert fired

`guard: mass-delete detection` means a rule deleted an unusual share of its own shadows in one cycle. The deletes went through; the guard reports, it doesn't block.

Ask one question: was the source calendar supposed to lose that many events? If yes, nothing is wrong. If no, look for a fetch that returned success with no events. The next healthy cycle rebuilds everything from the source either way, since Meridian stores nothing that a rebuild could lose.

## Duplicate copies at the destination

Almost always two rules writing the same events into the same calendar, each with its own `id` and so its own markers. Neither can see the other's copies, and both are behaving correctly.

Check whether a rule was renamed without wiping the old one's shadows first. A renamed `id` orphans everything the old name wrote, and those orphans are only swept while they remain inside the sync window.

## Old events were never cleaned up

Automatic cleanup is windowed. A shadow that drifted past `now + 90 days` or fell behind `now - 1 day` stops being reconciled and stays as calendar history.

This is what `meridian wipe` exists for, including after an `instance` rename:

```bash
meridian wipe calendar <account/calendar> --config rules.yaml --instance <old-id> --yes
```

## Rate limiting

A `rate_limited` fetch error means the provider turned Meridian away. That rule's cycle ends there, and the next scheduled cycle retries.

There is no backoff timer and nothing sleeps. Your `interval` is the retry delay, which is the whole retry strategy: cycles are stateless, so a skipped one costs nothing but freshness.

If it happens constantly, raise `interval`. Meridian polls a bounded window rather than tracking calendar history, so a longer interval costs you latency and nothing else.
