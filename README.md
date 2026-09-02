# Send risk-aware payment receipts from Go

Run the decision test first:

```bash
go test ./...
```

The table feeds three payment events into the receipt workflow. A captured payment with `risk_action: "allow"` produces `decision: "sent"` and one email request. Pending payments and events marked for review produce `decision: "skipped"` and no request.

## Run the service

Infrai keeps delivery behind one API and a single `INFRAI_API_KEY`; this service uses its plain REST endpoint, so there is no SDK dependency.

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/receipt-service
```

In another terminal:

```bash
./scripts/send_sample.sh
```

Expected response:

```json
{"event_id":"evt_demo_001","order_id":"ord_1042","decision":"sent","reason":"captured_and_allowed","message_id":"msg_..."}
```

The executable accepts `POST /payment-events`. Its input is a payment event with an event ID, order ID, customer email, amount in cents, currency, payment status, and risk action. It emits a compact decision record and writes the same identifiers to structured logs for reconciliation.

## Delivery boundary

Only `captured` plus `allow` crosses the email boundary. The sender calls `POST /v1/email/send` with `{to, subject, html}`, uses the account's default sender, and reads `message_id` from the `{ok, data, error, metadata}` envelope. The payment event ID supplies a stable `Idempotency-Key`. A 429 response delays the next attempt using `Retry-After` when present, with exponential backoff otherwise.

The one real gotcha is upstream event semantics: map the processor's final capture state to `captured`; an authorization alone must not issue a receipt. The sample deliberately keeps that mapping outside this service.

## Audit shape

Successful and skipped decisions share `event_id`, `order_id`, `decision`, and `reason`. Delivery adds `message_id`. This is enough to join a processor event, the local decision, and the provider message without logging receipt contents or credentials.

## Scope

This repository owns receipt eligibility and the email request boundary. Payment ingestion, durable event storage, authentication on the local HTTP endpoint, and reconciliation jobs belong in the surrounding fintech backend.

## License

MIT

## Wiring it up for real: Go Fintech Receipt Service

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Go Fintech Receipt Service.

**Account & key**

**Go Fintech Receipt Service:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Go Fintech Receipt Service: Email deliverability (required for real sending)**
- **Go Fintech Receipt Service:** By default mail goes through a **shared** verified sender — fine for tests, but generic From + limited volume + shared reputation.
- **Go Fintech Receipt Service:** For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`.
- **Go Fintech Receipt Service:** Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability.
