---
title: Operate
weight: 3
icon: support
description: What to alert on, what gets cleaned up for you, and what upgrades need.
---

Running Meridian day to day comes down to three things: alerting on the right metrics, knowing which cleanup happens by itself, and knowing what an upgrade does to existing copies.

When something has already gone wrong, [Troubleshoot]({{% relref "guides/troubleshoot" %}}) is organised by symptom.

## What to alert on

Meridian has no database and no UI. Alerts on its [metrics]({{% relref "reference/metrics" %}}) are how you learn that something broke. Four are worth wiring.

**`meridian_last_successful_cycle_timestamp_seconds` going stale**, per rule, beyond a few multiples of your `interval`. This is the primary signal. Whatever stops a rule converging, this catches it, and it catches the failure modes nobody predicted too.

**Any increase in `meridian_guard_triggers_total`.** These are rare-by-design safety nets. Any firing is worth a look.

**`meridian_shadow_drift_unrepairable` above zero.** Something is persistently overwriting what Meridian writes.

**`AuthFailed` entries in `meridian_op_errors_total` or `meridian_fetch_errors_total`.** Credentials need a human, and the damage compounds quietly: nothing crashes, your calendars just stop being true.

## What gets cleaned up automatically

Every cycle, Meridian looks at each account's writable calendars and deletes its own copies from any calendar no rule currently targets. Removing a rule, renaming a rule's `id`, or dropping a calendar from a rule's `to` list all trigger this on their own.

The sweep is windowed, though. It only reaches copies inside `[now - 1 day, now + 90 days]`.

So the line is: **current config plus sync window gets reconciled for you. Everything else needs `meridian wipe`.** You'll want it for:

- Copies that have aged out of the window and now sit there as history.
- Cleanup after changing `instance`, since the old instance's copies become invisible to the renamed one. Ownership is scoped by instance ID, and Meridian genuinely cannot see what it no longer claims to own.
- Tearing down a rule's copies immediately instead of waiting for the sweep.

See the [CLI reference]({{% relref "reference/cli#meridian-wipe" %}}) for the exact commands.

## Upgrading

Ownership markers are versioned, and Meridian can always read markers written by older versions of itself, filling in any field that didn't exist yet with the value it would have had.

Any copy touched by normal reconciliation gets rewritten in the current format as a side effect. There's no migration pass to run for an ordinary version bump; the upgrade happens quietly as events change.

The exception is a marker change where the old shape can't be decoded into the new one at all, such as a renamed field or a different identity encoding. That needs a targeted `meridian wipe` and a rebuild, and the release notes will say so. It hasn't happened yet.

> [!NOTE]
> Redeploying restarts the pod, which resets `meridian_last_successful_cycle_timestamp_seconds` and any soak clock you were watching. Harmless, but worth knowing before you conclude that an upgrade broke something.
