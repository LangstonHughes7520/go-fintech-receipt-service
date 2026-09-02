package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReceiptDecision(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		riskAction string
		want       string
		wantCalls  int
	}{
		{name: "captured payment sends", status: "captured", riskAction: "allow", want: "sent", wantCalls: 1},
		{name: "pending payment waits", status: "pending", riskAction: "allow", want: "skipped", wantCalls: 0},
		{name: "risk review suppresses receipt", status: "captured", riskAction: "review", want: "skipped", wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s", r.Method)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
					t.Fatalf("authorization = %q", got)
				}
				if got := r.Header.Get("Idempotency-Key"); got != "receipt:evt_123" {
					t.Fatalf("idempotency key = %q", got)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if _, exists := body["from"]; exists {
					t.Fatal("request must use the account default sender")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"ok":true,"data":{"message_id":"msg_456"},"error":null,"metadata":{}}`))
			}))
			defer server.Close()

			sender := NewReceiptSender("test-key", server.Client())
			sender.url = server.URL
			result, err := sender.Notify(context.Background(), PaymentEvent{
				EventID: "evt_123", OrderID: "ord_456", Customer: "buyer@example.com",
				AmountCents: 2599, Currency: "usd", Status: tt.status, RiskAction: tt.riskAction,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != tt.want || calls != tt.wantCalls {
				t.Fatalf("decision = %q, calls = %d; want %q, %d", result.Decision, calls, tt.want, tt.wantCalls)
			}
		})
	}
}

func TestSendRetriesRateLimit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"ok":false,"data":null,"error":{"message":"retry later"},"metadata":{}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"data":{"message_id":"msg_after_retry"},"error":null,"metadata":{}}`))
	}))
	defer server.Close()

	sender := NewReceiptSender("test-key", server.Client())
	sender.url = server.URL
	sender.sleep = func(context.Context, time.Duration) error { return nil }
	messageID, err := sender.send(context.Background(), emailRequest{To: "buyer@example.com", Subject: "Receipt", HTML: "<p>Paid</p>"}, "receipt:evt_retry")
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "msg_after_retry" || calls != 2 {
		t.Fatalf("messageID = %q, calls = %d", messageID, calls)
	}
}
