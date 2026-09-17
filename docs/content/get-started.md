---
title: Get started
weight: 1
icon: play
description: One rule syncing end to end, on your laptop, in a few minutes.
---

One rule, syncing end to end, on your laptop. No Kubernetes and no calendar you care about: you'll sync a real calendar into a scratch one you create for the occasion and delete afterwards.

> [!NOTE]
> Only Google Calendar and CalDAV are supported — no Exchange, Outlook, or Microsoft 365. See [Limitations]({{% relref "limitations" %}}) for the full list before you invest more time.

{{% steps %}}

### Install

```bash
go install github.com/islerfab/meridian/cmd/meridian@latest
```

Or grab a binary from the [releases page](https://github.com/islerfab/meridian/releases). Both work; the binary skips needing Go 1.27+.

> [!NOTE]
> Or skip the install. Substitute `docker run --rm -v "$PWD:/work" -w /work ghcr.io/islerfab/meridian:latest <args>` for `meridian <args>` below. Unsupported as a deployment target.

### Create a scratch destination calendar

Make a new, empty calendar on your provider. This is where the copies land, so it wants to be somewhere you won't mind seeing filled with duplicates of your own events.

{{< tabs >}}
{{< tab name="Google" >}}
Settings → Add calendar → Create new calendar. Then open Settings → your new calendar → Integrate calendar and copy the **Calendar ID**. It looks like `xxxxxxxx@group.calendar.google.com`.
{{< /tab >}}
{{< tab name="CalDAV" >}}
Create the calendar in your provider's web UI, then find its **collection path**. Most providers show this in the calendar's sharing or CalDAV settings; it looks like `/calendars/you@example.com/scratch/`.

If your provider only shows a full URL, the path is everything after the host.
{{< /tab >}}
{{< /tabs >}}

### Set up credentials

Meridian never stores secrets in its config. It stores the *names* of environment variables that hold them, so the config stays safe to commit.

{{< tabs >}}
{{< tab name="Google" >}}
You need a Google Cloud project with the Calendar API enabled and an OAuth client of type **Desktop app** (Cloud Console → APIs & Services → Credentials).

Publish the consent screen to **Production**. Apps left in Testing mode hand out refresh tokens that expire after seven days, which is a memorable way to discover your sync stopped a week ago.

```bash
meridian oauth --client-id "<id>.apps.googleusercontent.com" \
               --client-secret "<secret>" \
               --write-env .env
```

This runs a browser consent flow and writes the refresh token straight into `.env`, never to your terminal. Add the other two by hand:

```bash {filename=".env"}
MERIDIAN_GOOGLE_CLIENT_ID="<id>.apps.googleusercontent.com"
MERIDIAN_GOOGLE_CLIENT_SECRET="<secret>"
MERIDIAN_GOOGLE_REFRESH_TOKEN="..."   # written by the command above
```
{{< /tab >}}
{{< tab name="CalDAV" >}}
You need your CalDAV endpoint, your username, and a password. If your provider requires 2FA, generate an app-specific password rather than using your account password.

```bash {filename=".env"}
MERIDIAN_CALDAV_USERNAME="you@example.com"
MERIDIAN_CALDAV_PASSWORD="app-specific-password"
```
{{< /tab >}}
{{< /tabs >}}

### Write `rules.yaml`

{{< tabs >}}
{{< tab name="Google" >}}
```yaml {filename="rules.yaml"}
instance: quickstart # unique per instance, and never change it once shadows exist

accounts:
  - name: google
    type: google
    clientIDEnv: MERIDIAN_GOOGLE_CLIENT_ID
    clientSecretEnv: MERIDIAN_GOOGLE_CLIENT_SECRET
    refreshTokenEnv: MERIDIAN_GOOGLE_REFRESH_TOKEN
    calendars:
      - name: main
        id: primary
      - name: scratch
        id: "xxxxxxxx@group.calendar.google.com" # from the previous step

rules:
  - id: main-to-scratch
    from: google/main
    to: [google/scratch]
    # No filter, no transform: a faithful copy of everything.
```
{{< /tab >}}
{{< tab name="CalDAV" >}}
```yaml {filename="rules.yaml"}
instance: quickstart # unique per instance, and never change it once shadows exist

accounts:
  - name: caldav
    type: caldav
    endpoint: https://caldav.example.com/dav/
    usernameEnv: MERIDIAN_CALDAV_USERNAME
    passwordEnv: MERIDIAN_CALDAV_PASSWORD
    calendars:
      - name: main
        path: /calendars/you@example.com/personal/
      - name: scratch
        path: /calendars/you@example.com/scratch/ # from the previous step

rules:
  - id: main-to-scratch
    from: caldav/main
    to: [caldav/scratch]
    # No filter, no transform: a faithful copy of everything.
```
{{< /tab >}}
{{< /tabs >}}

### Validate, then run once

```bash
meridian validate --config rules.yaml   # parse, check references, compile rules
meridian run --once --config rules.yaml # one cycle, then exit
```

`validate` touches no provider and no credential, so it's safe to run anywhere, including CI.

### Run it again

Open the scratch calendar. Your events are there, each carrying a `meridian.*` ownership marker that's visible through the API but not in any calendar UI.

Now run `meridian run --once` a second time.

Nothing happens, and that's the entire point. Meridian compares a content hash against the one stored in each shadow's marker, so an unchanged event produces no write. You can run it a thousand times and the calendar won't notice.

{{% /steps %}}

## Cleaning up

When you're done playing, remove everything meridian created:

```bash
meridian wipe rule main-to-scratch --config rules.yaml --yes
```

Then delete the scratch calendar. Meridian only ever deletes events carrying its own marker, so your source calendar is never at risk from this command.

## Next steps

{{< cards >}}
  {{< refcard path="/guides/write-rules" title="Write sync rules" subtitle="Filters, transforms, the Busy pattern, bidirectional pairs." icon="adjustments" >}}
  {{< refcard path="/guides/deploy" title="Deploy on Kubernetes" subtitle="Run this continuously instead of by hand." icon="server" >}}
  {{< refcard path="/limitations" title="Limitations" subtitle="What meridian deliberately doesn't do, before you rely on it." icon="exclamation-circle" >}}
{{< /cards >}}
