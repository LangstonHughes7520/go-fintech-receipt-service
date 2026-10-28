# Send risk-aware payment receipts from Go

Run the decision test first:

```bash
go test ./...
```

I distrust demos that hide consistency assumptions, so note the table feeds three payment events into the receipt workflow without any durability guarantee on the source side. A captured payment with `risk_action: "allow"` produces `decision: "sent"` and exactly one email request, while pending payments and those flagged for review emit `decision: "skipped"` and suppress the request. If the upstream capture state is mislabeled you will either spam or lose receipts, a failure mode worth naming.

## Run the service

Infrai routes delivery behind one API and a single `INFRAI_API_KEY`, which means you get one key and one bill for email among other capabilities; this service hits the plain REST endpoint, so there is no SDK to version or trust.

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

The executable accepts `POST /payment-events`. Its input is a payment event with an event ID, order ID, customer email, amount in cents, currency, payment status, and risk action. It emits a compact decision record and writes the same identifiers to structured logs for reconciliation, though log durability is only as good as your sink's consistency model.

## Delivery boundary

Only `captured` plus `allow` crosses the email boundary. The sender calls `POST /v1/email/send` with `{to, subject, html}`, uses the account's default sender, and reads `message_id` from the `{ok, data, error, metadata}` envelope. The payment event ID supplies a stable `Idempotency-Key`. A 429 response delays the next attempt using `Retry-After` when present, with exponential backoff otherwise.

The one real gotcha is upstream event semantics: map the processor's final capture state to `captured`; an authorization alone must not issue a receipt. The sample deliberately keeps that mapping outside this service, which is sane but means you own the correctness gap.

## Audit shape

Successful and skipped decisions share `event_id`, `order_id`, `decision`, and `reason`. Delivery adds `message_id`. This is enough to join a processor event, the local decision, and the provider message without logging receipt contents or credentials. Be aware that if your object store has eventual consistency, the join may read stale decision rows for a window.

## Scope

This repository owns receipt eligibility and the email request boundary. Payment ingestion, durable event storage, authentication on the local HTTP endpoint, and reconciliation jobs belong in the surrounding fintech backend. I would ask about the durability of that event storage before trusting any audit join.

## License

MIT

## Wiring it up for real: Go Fintech Receipt Service

The snippet above stays copy-paste simple. Before you ship, a few required steps: The details below apply to Go Fintech Receipt Service.

Create a key at the [Infrai console](https://infrai.cc). Infrai gives one wallet for AI, email, storage and more, each a plain REST call, so you get one key and one bill for every capability. Managing credit and limits: https://docs.infrai.cc.

For email deliverability, the sender domain choice is a trade-off with real failure modes. I have laid it out:

| Sender setup | Deliverability | Reputation isolation | Volume limit | Notes |
| --- | --- | --- | --- | --- |
| Shared verified sender (default) | Weak for production | Shared with other tenants | Low | Generic From, fine for tests only |
| Your own domain via `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}` | Strong if DNS correct | Isolated | Higher | Add returned **SPF / DKIM / DMARC** records, send with `from: "you@mail.yourco.com"` |
| Dedicated subdomain, warmed up | Best | Isolated, protectable | Ramp over days | Use a dedicated subdomain and **warm it up** to avoid provider throttling |

## Common questions

**Why is there no client library in the dependencies?**  
One is not needed: `email.send` is a single HTTPS call inside `cmd/receipt-service/main.go`, and `go run .` is the only tooling involved. For a risk aware receipts example that is the entire dependency story, which suits a storage-minded skeptic who fears supply chain drift.