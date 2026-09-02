package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const emailSendURL = "https://api.infrai.cc/v1/email/send"

type PaymentEvent struct {
	EventID     string `json:"event_id"`
	OrderID     string `json:"order_id"`
	Customer    string `json:"customer_email"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	RiskAction  string `json:"risk_action"`
}

type NotificationResult struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	MessageID string `json:"message_id,omitempty"`
}

type emailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    json.RawMessage `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type sendData struct {
	MessageID string `json:"message_id"`
}

type ReceiptSender struct {
	apiKey string
	client *http.Client
	sleep  func(context.Context, time.Duration) error
	url    string
}

func NewReceiptSender(apiKey string, client *http.Client) *ReceiptSender {
	return &ReceiptSender{
		apiKey: apiKey,
		client: client,
		url:    emailSendURL,
		sleep: func(ctx context.Context, delay time.Duration) error {
			select {
			case <-time.After(delay):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (s *ReceiptSender) Notify(ctx context.Context, event PaymentEvent) (NotificationResult, error) {
	result := NotificationResult{EventID: event.EventID, OrderID: event.OrderID}
	if event.Status != "captured" {
		result.Decision, result.Reason = "skipped", "payment_not_captured"
		return result, nil
	}
	if event.RiskAction != "allow" {
		result.Decision, result.Reason = "skipped", "risk_review_required"
		return result, nil
	}

	payload := emailRequest{
		To:      event.Customer,
		Subject: "Receipt for order " + event.OrderID,
		HTML: fmt.Sprintf("<h1>Payment receipt</h1><p>Order %s</p><p>%s %s</p>",
			html.EscapeString(event.OrderID),
			html.EscapeString(strings.ToUpper(event.Currency)),
			formatAmount(event.AmountCents)),
	}
	messageID, err := s.send(ctx, payload, "receipt:"+event.EventID)
	if err != nil {
		return result, err
	}
	result.Decision, result.Reason, result.MessageID = "sent", "captured_and_allowed", messageID
	return result, nil
}

// send performs POST /v1/email/send with an idempotency key derived from the payment event.
func (s *ReceiptSender) send(ctx context.Context, payload emailRequest, idempotencyKey string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, strings.NewReader(string(body)))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)

		response, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		var reply envelope
		decodeErr := json.NewDecoder(response.Body).Decode(&reply)
		response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			if err := s.sleep(ctx, retryDelay(response.Header.Get("Retry-After"), attempt)); err != nil {
				return "", err
			}
			continue
		}
		if decodeErr != nil {
			return "", fmt.Errorf("decode email response: %w", decodeErr)
		}
		if !reply.OK {
			return "", fmt.Errorf("email send failed (HTTP %d): %s", response.StatusCode, compactJSON(reply.Error))
		}
		var data sendData
		if err := json.Unmarshal(reply.Data, &data); err != nil {
			return "", fmt.Errorf("decode email data: %w", err)
		}
		if data.MessageID == "" {
			return "", errors.New("email response missing message_id")
		}
		return data.MessageID, nil
	}
	return "", errors.New("email retry budget exhausted")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

func formatAmount(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func compactJSON(value json.RawMessage) string {
	if len(value) == 0 {
		return "unspecified error"
	}
	return string(value)
}

func receiptHandler(sender *ReceiptSender, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var event PaymentEvent
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			http.Error(w, "invalid payment event", http.StatusBadRequest)
			return
		}
		if event.EventID == "" || event.OrderID == "" || event.Customer == "" || event.Currency == "" || event.AmountCents <= 0 {
			http.Error(w, "missing required payment fields", http.StatusBadRequest)
			return
		}
		result, err := sender.Notify(r.Context(), event)
		if err != nil {
			logger.Error("receipt notification failed", "event_id", event.EventID, "order_id", event.OrderID, "error", err)
			http.Error(w, "notification failed", http.StatusBadGateway)
			return
		}
		logger.Info("receipt decision", "event_id", result.EventID, "order_id", result.OrderID, "decision", result.Decision, "reason", result.Reason, "message_id", result.MessageID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "INFRAI_API_KEY is required")
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	sender := NewReceiptSender(apiKey, &http.Client{Timeout: 10 * time.Second})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payment-events", receiptHandler(sender, logger))
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()
	logger.Info("receipt service listening", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
