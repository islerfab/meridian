---
title: FAQ
weight: 5
icon: question-mark-circle
description: Why it's built this way, and what to expect before you point it at a calendar.
---

Questions that come up before people trust meridian with a calendar they care about. [Limitations]({{% relref "limitations" %}}) covers what it deliberately doesn't do; [Design]({{% relref "design" %}}) has the invariants underneath these answers.

## Why is there no database?

Because the calendars already hold the state. Every copy meridian writes carries a marker naming the rule that created it, so the full picture of what should exist is rebuilt from the calendars themselves each cycle. A database would be a second copy of something already stored, and a second thing to get out of step.

The alternative is also harder than it looks on both protocols.

{{% details title="What made delta sync unattractive" closed="true" %}}
CalDAV client libraries commonly lack sync-collection ([RFC 6578](https://www.rfc-editor.org/rfc/rfc6578)) support for calendars, so delta sync there means hand-rolling REPORT and PROPFIND against each provider's interpretation of the spec.

Google's `syncToken` can't be combined with `timeMin` and `timeMax`. Incremental sync means tracking a calendar's entire history rather than a bounded window.

Prior art points the same way. The closest comparable tool in shape is stateless-snapshot by explicit design, with markers embedded on the events themselves.

A delta-plus-database design in the same space tells the other half of the story. Its issue tracker is dominated by stale-state bugs and requests for a "force resync" button, and it still needed ownership markers and orphan collection as a fallback, because the database alone couldn't be trusted. Even sync-token-based mobile clients fall back to a full resync when a token goes stale.
{{% /details %}}

The payoff is that there's nothing to reconcile against reality. A crashed pod, a restored backup and a rewritten config all converge the same way, by looking at what's there and fixing the difference.

## Doesn't polling every few minutes burn through API quota?

Not close to it. Fetching a 90-day window every few minutes sits orders of magnitude below both providers' rate limits at single-user scale. Meridian also shares one fetch per calendar across every rule reading from it, so adding rules doesn't multiply requests.

If you do hit a limit, the adapter classifies it as `RateLimited` and ends that rule's cycle. The next scheduled cycle is the retry. Nothing queues and nothing backs off.

## Is it safe to point at a calendar I actually use?

That's the case it was built for, and three things make it hold.

**Meridian only ever touches events it created.** Every write carries an ownership marker naming the instance and rule behind it. Reconciliation, garbage collection and `wipe` all filter on that marker, so your own events are invisible to it. So are another tool's.

**It won't start until it can read.** A read-only probe runs against every configured calendar before the first cycle, and the reconciliation loop doesn't begin until all of them pass. An instance that never became ready has provably written nothing.

**Undo is one command.** `meridian wipe` removes everything carrying this instance's marker and leaves everything else alone.

The five hardening guards in the engine were extracted from the issue trackers of other tools in this space. Each one is there because it bit somebody else first.

## What happens if I get a rule wrong?

You get wrong copies, and then you delete them. Copies are derived state, so there's no accumulated history to untangle: fix the rule and the next cycle converges, or run `meridian wipe rule <id>` to clear that rule's output outright.

`meridian validate` catches the structural mistakes before anything runs. Unknown fields, calendar references that don't resolve, CEL that doesn't compile, templates that don't parse. It touches no credential and no provider, so it's safe to run in CI on every change.

What validation can't catch is a filter that's perfectly valid and selects the wrong events. Pointing rules at scratch destination calendars while reading from your real sources is how you find that out cheaply, as described in [Deploy]({{% relref "guides/deploy" %}}).

## Can I run it next to another sync tool?

Not on the same calendars. Two engines each treating the other's output as a source event to mirror produce results that are very hard to tell apart from a bug in either one.

Meridian skips any source event carrying a meridian marker, whoever wrote it, so it never feeds on its own output or another instance's. It has no way to recognize a different tool's copies, and no tool in this space has a shared convention for saying "this is a copy, leave it alone".

Stage on calendars the old tool never touches, then cut over.

## Why does it need a long-running pod instead of a CronJob?

The tick loop is the same idiom as any other Kubernetes controller, and the observability surface assumes a stable, scrapeable target: per-rule gauges, a last-successful-cycle timestamp that alerting watches, readiness that means something specific. A pod that exits between runs takes all of that with it, and a CronJob's characteristic failure mode is silence.

It can't scale to zero between cycles, and that isn't on the roadmap. The idle cost is one small pod.

## Can two instances write to the same calendar?

Yes. Ownership is scoped by instance ID plus rule ID, so two instances sharing a destination never see each other's copies and never contend over them.

That's also why the instance ID has to be unique and stable. Change it and the previous instance's copies become invisible to the new one: stranded rather than cleaned up, since nothing recognizes them as its own any more. `meridian wipe --instance <old>` is the way back.

## Why CEL and Go templates instead of letting me write code?

Filters get an expression language because a bad filter selects the wrong events, which the next cycle can undo. Transforms get typed fields and string templating only, because a bad transform corrupts calendar content, which is meaningfully worse.

CEL specifically because it's already the Kubernetes ecosystem's embedded expression language, used in CRD validation and `ValidatingAdmissionPolicy`: non-Turing-complete, sandboxed, guaranteed to terminate, incapable of I/O. It compiles and type-checks when the config loads, so a broken expression stops startup instead of surfacing halfway through a sync.

Conditional transforms fall out of writing two rules with disjoint `when` predicates, and rule-scoped markers guarantee those two can never fight over the same copy.
