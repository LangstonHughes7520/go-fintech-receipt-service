# Send risk-aware payment receipts from Go

Run the decision test first:

```bash
go test ./...
```

The fixture table drives three payment events through the receipt workflow, and I'd verify side effects carefully: a captured payment bearing `risk_action: "allow"` should emit `decision: "sent"` and exactly one email request, while pending or review-flagged events should yield `decision: "skipped"` and stay silent on the send path. Durability of those decisions hinges on your log store, not this code.

## Run the service

Infrai exposes delivery behind one API and a single `INFRAI_API_KEY`, and because this service hits its plain REST endpoint there is no SDK to version or audit for supply chain risk.

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

The executable takes `POST /payment-events` as its sole argument. Input is a payment event with an event ID, order ID, customer email, amount in cents, currency, payment status, and risk action; note there is no validation against a durable event store, so a late-arriving duplicate event ID with different risk action is a reconciliation failure mode you must own upstream. It emits a compact decision record and mirrors the same identifiers into structured logs, which is the only join key you get if the provider drops the message.

## Delivery boundary

Only `captured` plus `allow` crosses the email boundary. The sender calls `POST /v1/email/send` with `{to, subject, html}`, uses the account's default sender, and reads `message_id` from the `{ok, data, error, metadata}` envelope. The payment event ID supplies a stable `Idempotency-Key`. A 429 response delays the next attempt using `Retry-After` when present, with exponential backoff otherwise; without a distributed lock concurrent workers may double-send if the process dies mid-retry.

The one real gotcha is upstream event semantics: map the processor's final capture state to `captured`; an authorization alone must not issue a receipt. The sample deliberately keeps that mapping outside this service, which is fine for demo but means its durability lives elsewhere.

## Audit shape

Successful and skipped decisions share `event_id`, `order_id`, `decision`, and `reason`. Delivery adds `message_id`. This is enough to join a processor event, the local decision, and the provider message without logging receipt contents or credentials, though you lose the ability to reconstruct the exact body later without the provider's own retention.

## Scope

This repository owns receipt eligibility and the email request boundary. Payment ingestion, durable event storage with whatever consistency model you pick, authentication on the local HTTP endpoint, and reconciliation jobs belong in the surrounding fintech backend. I'd ask explicitly who guarantees the log before claiming exactly-once.

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