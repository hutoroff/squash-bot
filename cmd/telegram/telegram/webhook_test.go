package telegram

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func validUpdateBody(t *testing.T) []byte {
	t.Helper()
	u := tgbotapi.Update{UpdateID: 42}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	return b
}

func TestWebhookHandler_ValidSecretAndBody(t *testing.T) {
	const secret = "test-secret"
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler(secret, ch)

	body := validUpdateBody(t)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case u := <-ch:
		if u.UpdateID != 42 {
			t.Errorf("expected update_id 42, got %d", u.UpdateID)
		}
	default:
		t.Error("expected update enqueued, channel was empty")
	}
}

func TestWebhookHandler_WrongSecret(t *testing.T) {
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler("correct-secret", ch)

	body := validUpdateBody(t)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-secret")
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if len(ch) != 0 {
		t.Error("update should not be enqueued on auth failure")
	}
}

func TestWebhookHandler_MissingSecret(t *testing.T) {
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler("required-secret", ch)

	body := validUpdateBody(t)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	// no secret header
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWebhookHandler_NoSecret_Skips_Validation(t *testing.T) {
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler("", ch) // empty secret = no validation

	body := validUpdateBody(t)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestWebhookHandler_NonPOST(t *testing.T) {
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler("", ch)

	req := httptest.NewRequest(http.MethodGet, "/hook", nil)
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestWebhookHandler_BadBody(t *testing.T) {
	ch := make(chan tgbotapi.Update, 1)
	h := webhookHandler("", ch)

	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte("not json")))
	rr := httptest.NewRecorder()

	h(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if len(ch) != 0 {
		t.Error("update should not be enqueued on decode failure")
	}
}
