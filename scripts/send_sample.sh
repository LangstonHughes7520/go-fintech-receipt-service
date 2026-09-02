#!/bin/sh
set -eu

curl --fail-with-body --silent --show-error \
  --request POST http://localhost:8080/payment-events \
  --header 'Content-Type: application/json' \
  --data '{"event_id":"evt_demo_001","order_id":"ord_1042","customer_email":"buyer@example.com","amount_cents":2599,"currency":"usd","status":"captured","risk_action":"allow"}'
