# icalmerge

Merges multiple calendar sources - iCal feeds and Google Calendar accounts - into a single iCal feed served over HTTP. Built for [TRMNL](https://trmnl.com) but works with any iCal client.

Typical use: your work calendar is an iCal URL, your personal calendar is Google, your partner's calendar is also Google. icalmerge fetches all three, deduplicates, and exposes one clean feed. Optionally prefixes overlapping events with `CONFLICT:` so you can see clashes at a glance on your e-ink display.

---

## Quick start (Docker)

```sh
# Generate a hashed secret
echo "yourpassword" | docker run -i ghcr.io/freman/icalmerge password

# Create config
cp config.example.yaml config.yaml
# Edit config.yaml: paste the hash, add your calendars

# Run
docker run -d \
  -v $(pwd)/data:/data \
  -v $(pwd)/config.yaml:/config.yaml \
  -e ICALMERGE_DATA_DIR=/data \
  -p 8080:8080 \
  ghcr.io/freman/icalmerge serve --config /config.yaml
```

Your merged calendar is at `http://localhost:8080/calendar` - authenticate with `Authorization: Bearer yourpassword`.

---

## Building from source

Requires [Go 1.26+](https://go.dev/dl/).

```sh
git clone https://github.com/freman/icalmerge
cd icalmerge
go build -o icalmerge .
```

---

## Configuration

Copy `config.example.yaml` and edit it:

```yaml
server:
  port: 8080
  secret: "$2a$12$..."       # bcrypt hash from: echo "pw" | icalmerge password
  auth_header: Authorization # header to check; "Authorization" requires "Bearer " prefix
                             # use e.g. "X-API-Key" for a raw token (no Bearer)
  cache_ttl: 15m             # on-demand mode: how long to cache between requests
  fetch_timeout: 30s         # per-refresh deadline
  # poll_interval: 10m       # uncomment to enable background polling mode
  days_ahead: 60             # only include events in the next N days
  parallelism: 0             # max concurrent source fetches (0 = unlimited)
  mark_conflicts: false      # prefix overlapping events with "CONFLICT: "
  expand_recurrences: false  # expand recurring events into individual instances server-side
                             # use when your client doesn't handle RRULE (e.g. TRMNL)

google:
  client_id: ""              # or use GOOGLE_CLIENT_ID env var
  client_secret: ""          # or use GOOGLE_CLIENT_SECRET env var

data_dir: /data              # where Google OAuth tokens are stored

calendars:
  - name: Work
    type: ical
    url: "https://calendar.example.com/feed.ics"

  - name: My Calendar
    type: google
    account: me              # name for the stored token
    calendar_id: primary

  - name: Partner
    type: google
    account: partner
    calendar_id: primary
```

### Environment variables

All sensitive values can be injected without a config file:

| Variable | Overrides |
|---|---|
| `ICALMERGE_CONFIG` | path to config file |
| `ICALMERGE_SECRET` | `server.secret` |
| `ICALMERGE_DATA_DIR` | `data_dir` |
| `GOOGLE_CLIENT_ID` | `google.client_id` |
| `GOOGLE_CLIENT_SECRET` | `google.client_secret` |

### Hashing your secret

Store a bcrypt hash rather than plaintext to avoid leaking the token in config files or environment variable dumps:

```sh
echo "yourpassword" | icalmerge password
# $2a$12$...paste this into config or ICALMERGE_SECRET
```

A plaintext secret still works but logs a warning on startup.

---

## Google Calendar setup

You need a Google Cloud project with the Calendar API enabled and an OAuth 2.0 client configured for desktop/installed-app use.

### 1. Create a project and enable the API

1. Open the [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project (or select an existing one)
3. Go to [APIs & Services > Library](https://console.cloud.google.com/apis/library) and enable the **Google Calendar API**

### 2. Create OAuth credentials

1. Go to [APIs & Services > Credentials](https://console.cloud.google.com/apis/credentials)
2. Click **Create Credentials > OAuth client ID**
3. Application type: **Desktop app**
4. Download or copy the **Client ID** and **Client Secret**
5. Set them in config or as `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`

For reference: [OAuth 2.0 for Installed Applications](https://developers.google.com/identity/protocols/oauth2/native-app)

### 3. Authorize each account

Run this locally (where you have a browser). The token is saved to `<data_dir>/tokens/<name>.json` and can then be copied to your server.

```sh
icalmerge auth add me --config config.yaml
icalmerge auth add partner --config config.yaml
```

A browser window opens for each account. After authorizing, you'll see a confirmation in the terminal.

```sh
# Check stored tokens
icalmerge auth list --config config.yaml

# Remove a token
icalmerge auth revoke partner --config config.yaml
```

### 4. Copy tokens to your server

For Docker or Kubernetes, copy the token directory to the data volume before starting the service:

```sh
scp -r ~/.config/icalmerge/tokens/ user@server:/path/to/data/tokens/
```

Tokens include a refresh token so they stay valid indefinitely unless access is revoked in your [Google Account security settings](https://myaccount.google.com/permissions).

---

## TRMNL setup

[TRMNL](https://trmnl.com) is an e-ink display that fetches content from plugins on a schedule.

1. In the TRMNL dashboard, add the **CalDAV** plugin (or a custom plugin that accepts an iCal URL)
2. Configure the auth header TRMNL sends - either use the default `Authorization: Bearer <secret>` or set a custom header in your config:
   ```yaml
   server:
     auth_header: X-API-Key   # TRMNL sends this header with your secret as the value
   ```
3. Set the feed URL to your icalmerge endpoint:
   ```
   http://your-server:8080/calendar
   ```
4. Set the refresh interval to match your `cache_ttl` or `poll_interval` - no point polling faster than icalmerge refreshes

The `days_ahead` setting controls how far forward events are included. 30-60 days works well for a typical TRMNL calendar view.

Enable `mark_conflicts: true` if you want scheduling conflicts to be visually obvious on the display.

Enable `expand_recurrences: true` if the TRMNL plugin doesn't render recurring events - this materialises each occurrence as a standalone event within the `days_ahead` window.

---

## Fetch modes

### On-demand (default)

Fetches all sources when the first request arrives after the cache expires. The requesting client waits for the fetch. Simplest to operate.

```yaml
server:
  cache_ttl: 15m   # re-fetch at most every 15 minutes
```

### Background polling

A goroutine fetches on a fixed interval regardless of incoming requests. HTTP requests are served immediately from the last good buffer - zero wait time. If a poll fails, the previous calendar is retained and an error is logged.

```yaml
server:
  poll_interval: 10m   # fetch every 10 minutes in the background
```

Use polling when you want predictable latency on requests, or when your upstream sources are slow.

---

## Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: icalmerge
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: icalmerge
          image: ghcr.io/freman/icalmerge:latest
          args: ["serve", "--config", "/config/config.yaml"]
          env:
            - name: ICALMERGE_SECRET
              valueFrom:
                secretKeyRef:
                  name: icalmerge-secrets
                  key: secret
            - name: GOOGLE_CLIENT_ID
              valueFrom:
                secretKeyRef:
                  name: icalmerge-secrets
                  key: google-client-id
            - name: GOOGLE_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: icalmerge-secrets
                  key: google-client-secret
            - name: ICALMERGE_DATA_DIR
              value: /data
          volumeMounts:
            - name: config
              mountPath: /config
            - name: data
              mountPath: /data
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
      volumes:
        - name: config
          configMap:
            name: icalmerge-config
        - name: data
          persistentVolumeClaim:
            claimName: icalmerge-data
```

---

## Commands

```
icalmerge serve [--config <path>]          start the HTTP server
icalmerge auth add <name> [--config]       authorize a Google account
icalmerge auth list [--config]             list authorized accounts
icalmerge auth revoke <name> [--config]    remove an account token
icalmerge password                         hash a password (stdin -> stdout)
```
