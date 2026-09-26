# GoDesk SMS architecture

GoDesk SMS is a self-hosted SMS gateway. It keeps the httpSMS send, receive, and webhook ideas, and it uses [SMS Gateway for Android](https://github.com/capcom6/android-sms-gateway) as the phone app. The hard requirement is no Firebase and no other Google service.

## Option A — official private server

Run [android-sms-gateway/server](https://github.com/android-sms-gateway/server) in private mode and point the Android app at it.

What it already does:

- Device registration protected by a private token
- The same 3rdparty REST API as the public SMSGate cloud
- Multi-device routing, message state, and webhooks
- A separate web dashboard image

What it does not do:

- The private server still wakes phones by relaying a push through `api.sms-gate.app` and Firebase Cloud Messaging. The project documents an SSE path, but the supported install still depends on that upstream for push. That fails the "no Firebase or Google services" rule.
- Storage is MariaDB or MySQL, not Postgres.
- The API is the SMSGate API (`/api/3rdparty/v1/...`), not the httpsms API (`/v1/messages/send`, `x-api-key`, `message.phone.received` webhooks).
- The httpSMS Nuxt dashboard signs in with Firebase. It cannot talk to this server without a new auth layer and a new client.

## Option B — GoDesk API talks to the phones

Keep a Go API, Postgres, and an httpsms-compatible edge. Replace Firebase Auth with local accounts, and replace FCM by calling SMS Gateway for Android directly.

Two phone modes exist:

- **Local server mode** runs an HTTP server on the phone. The API sends SMS with HTTP basic auth and receives `sms:received` webhooks from the phone. No Firebase, no Google, no public upstream.
- **Private server mode** keeps message text on a server you run, but the official server still uses the public push relay. A later polling or SSE channel could remove that relay. It is not the path that is free of Google services today.

## Decision

Option B, with **local server mode** as the device integration.

Local mode is the only supported SMS Gateway mode that is fully off Firebase. The GoDesk service then keeps the pieces this deployment needs: Postgres, Traefik on the LAN, more than one phone, the httpsms send/receive/webhook shape, and email/password auth with optional OIDC (Authentik).

The tradeoff is reachability. The API must be able to open a connection to each phone, and each phone must be able to POST webhooks back. Phones that are not reachable inbound cannot send until a later polling design exists. That limitation is accepted for this slice because it is what makes the system independent of Google.

The existing httpSMS API and Nuxt app stay in the tree for the current Firebase deployment. They are not started by `docker-compose.selfhosted.yml`.

## What this slice runs

`docker-compose.selfhosted.yml` starts:

- Traefik, publishing only ports 80 and 443
- Postgres 16, with no published port
- `selfhosted/`, a Go service

On startup the service applies `selfhosted/internal/store/pg/migrations`. Accounts are email and password (bcrypt). Sessions are HS256 JWTs. Each user also has an `x-api-key` for httpsms-style clients. When `OIDC_ISSUER` is set, `/v1/auth/oidc/start` performs an authorization-code login with PKCE against that issuer.

A device row stores the phone's E.164 number, the gateway origin, basic-auth username, and the password sealed with AES-GCM. The key comes from `CREDENTIALS_KEY`, or from `JWT_SECRET` when that is unset. Registering a device asks the phone to webhook `sms:received`, `sms:sent`, `sms:delivered`, and `sms:failed` to `PUBLIC_URL/v1/gateway/events/{token}`.

`POST /v1/messages/send` with `from`, `to`, and `content` selects the device whose phone number matches `from` and POSTs to the phone's `/message` endpoint (local) or `/api/3rdparty/v1/messages` (private URL shape). `POST /v1/gateway/events/{token}` stores an inbound SMS and POSTs a CloudEvents JSON body of type `message.phone.received` to the user's webhooks, with `X-Event-Type` set. A signing key produces an `Authorization: Bearer` JWT whose audience is the webhook URL.

The same process serves a small dashboard at `/` for sign-up, login, device registration, send, and webhooks. `http://godesk.localhost` is the generic host used in the example env file. Replace it before exposing the stack.

## Remaining work

- Port the Nuxt app in `web/` off Firebase and onto this login, and retire the small dashboard.
- Wake phones that cannot accept inbound connections, without FCM. That is device polling or SSE, not the official private-server push relay.
- Finish private-server mode as a first-class deploy (the client already knows the URL shape).
- httpsms features not in this slice: bulk send, MMS, end-to-end encryption, search, heartbeats, schedules, and the full webhook event set.
- Retry and signing parity for every outbound webhook.
- Store API keys as hashes and show them once.
- Bring your own TLS material for Traefik. Port 443 currently uses Traefik's default certificate.
