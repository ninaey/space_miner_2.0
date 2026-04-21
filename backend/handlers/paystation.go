package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// ── PayStation token creation (Xsolla merchant token API) ────────

type createPaymentRequest struct {
	SKU string `json:"sku"`
}

type payStationTokenRequest struct {
	User             payStationUser      `json:"user"`
	Settings         payStationSettings  `json:"settings"`
	Purchase         *payStationPurchase `json:"purchase,omitempty"`
	Sandbox          bool                `json:"sandbox,omitempty"`
	CustomParameters map[string]string   `json:"custom_parameters,omitempty"`
}

type payStationUser struct {
	ID      payStationField  `json:"id"`
	Name    *payStationField `json:"name,omitempty"`
	Email   *payStationField `json:"email,omitempty"`
	Country *payStationField `json:"country,omitempty"`
}

type payStationField struct {
	Value string `json:"value"`
}

type payStationSettings struct {
	ProjectID  int           `json:"project_id"`
	ExternalID string        `json:"external_id,omitempty"`
	Language   string        `json:"language,omitempty"`
	Currency   string        `json:"currency,omitempty"`
	UI         *payStationUI `json:"ui,omitempty"`
}

type payStationUI struct {
	Components *payStationComponents `json:"components,omitempty"`
}

type payStationComponents struct {
	VirtualItems *payStationVirtualItems `json:"virtual_items,omitempty"`
}

type payStationVirtualItems struct {
	SelectedItem string `json:"selected_item,omitempty"`
}

type payStationPurchase struct {
	Items        []payStationPurchaseItem `json:"items,omitempty"`
}

type payStationPurchaseItems struct {
	Items []payStationPurchaseItem `json:"items,omitempty"`
}

type payStationPurchaseItem struct {
	SKU    string `json:"sku"`
	Quantity int   `json:"quantity"`
}

type payStationTokenResponse struct {
	Token   string `json:"token"`
	OrderID int64  `json:"order_id"`
}

type xsollaErrorResponse struct {
	HTTPStatusCode  int    `json:"http_status_code"`
	Message         string `json:"message"`
	ErrorMessage    string `json:"errorMessage"`
	ExtendedMessage struct {
		GlobalErrors   []string            `json:"global_errors"`
		PropertyErrors map[string][]string `json:"property_errors"`
	} `json:"extended_message"`
	RequestID string `json:"request_id"`
}

// CreatePayment creates a PayStation token via Xsolla v3 Admin Payment Token API.
func (h *StoreHandler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	playerID, ok := PlayerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated player")
		return
	}
	xsollaUserID := makeXsollaUserID(playerID)

	var missing []string
	if h.apiKey == "" {
		missing = append(missing, "XSOLLA_API_KEY")
	}
	if h.projectID == 0 {
		missing = append(missing, "XSOLLA_PROJECT_ID")
	}
	if h.merchantID == 0 {
		missing = append(missing, "XSOLLA_MERCHANT_ID")
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("PayStation not configured: missing %s", strings.Join(missing, ", ")))
		return
	}

	var req createPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.SKU == "" {
		writeError(w, http.StatusBadRequest, "sku is required")
		return
	}

	player, err := h.service.GetPlayerByID(r.Context(), playerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load player")
		return
	}

	item, err := h.service.GetStoreItem(r.Context(), req.SKU)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown sku")
		return
	}
	if item.CurrencyType != "real" {
		writeError(w, http.StatusBadRequest, "sku is not a real-money item")
		return
	}

	tokenReq := payStationTokenRequest{
		User: payStationUser{
			ID: payStationField{Value: xsollaUserID},
		},
		Settings: payStationSettings{
			ProjectID:  h.projectID,
			ExternalID: fmt.Sprintf("%s-%s-%d", playerID, req.SKU, time.Now().UnixMilli()),
			Language:   "en",
			Currency:   h.payCurrency,
		},
		Sandbox: h.sandbox,
		CustomParameters: map[string]string{
			"sku":         req.SKU,
			"player_id":   playerID,
			"xsolla_user": xsollaUserID,
		},
	}
	if h.payCountry != "" {
		tokenReq.User.Country = &payStationField{Value: h.payCountry}
	}
	if player.Username != "" {
		tokenReq.User.Name = &payStationField{Value: player.Username}
	}
	if player.Email != "" {
		tokenReq.User.Email = &payStationField{Value: player.Email}
	}
	tokenReq.Settings.UI = &payStationUI{
		Components: &payStationComponents{
			VirtualItems: &payStationVirtualItems{SelectedItem: req.SKU},
		},
	}
	tokenReq.Purchase = &payStationPurchase{
		Items: []payStationPurchaseItem{{
			SKU:      req.SKU,
			Quantity: 1,
		}},
	}

	body, err := json.Marshal(tokenReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build token request")
		return
	}

	url := fmt.Sprintf("https://store.xsolla.com/api/v3/project/%d/admin/payment/token", h.projectID)
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create HTTP request")
		return
	}

	credentials := fmt.Sprintf("%d:%s", h.projectID, h.apiKey)
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	httpReq.Header.Set("Content-Type", "application/json")

	if ip := clientIP(r); ip != "" {
		httpReq.Header.Set("X-User-Ip", ip)
	}

	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("paystation: token request failed: %v", err)
		writeError(w, http.StatusBadGateway, "failed to contact Xsolla")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("paystation: xsolla returned %d: %s", resp.StatusCode, string(respBody))
		details := fmt.Sprintf("Xsolla token creation failed (status %d)", resp.StatusCode)
		var xsErr xsollaErrorResponse
		if err := json.Unmarshal(respBody, &xsErr); err == nil {
			if xsErr.Message != "" {
				details = fmt.Sprintf("%s: %s", details, xsErr.Message)
			} else if xsErr.ErrorMessage != "" {
				details = fmt.Sprintf("%s: %s", details, xsErr.ErrorMessage)
			}
			if len(xsErr.ExtendedMessage.GlobalErrors) > 0 {
				details = fmt.Sprintf("%s (global: %s)", details, strings.Join(xsErr.ExtendedMessage.GlobalErrors, "; "))
			}
			if len(xsErr.ExtendedMessage.PropertyErrors) > 0 {
				var propertyErrors []string
				for field, messages := range xsErr.ExtendedMessage.PropertyErrors {
					propertyErrors = append(propertyErrors, fmt.Sprintf("%s: %s", field, strings.Join(messages, "; ")))
				}
				slices.Sort(propertyErrors)
				details = fmt.Sprintf("%s (fields: %s)", details, strings.Join(propertyErrors, " | "))
			}
		}
		writeError(w, http.StatusBadGateway, details)
		return
	}

	var tokenResp payStationTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		log.Printf("paystation: failed to decode token response: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to parse Xsolla response")
		return
	}

	log.Printf("paystation: created token for player=%s xsolla_user=%s sku=%s order=%d", playerID, xsollaUserID, req.SKU, tokenResp.OrderID)

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    tokenResp.Token,
		"order_id": tokenResp.OrderID,
	})
}

// makeXsollaUserID returns a stable, Pay Station-safe user id format.
// Some payment providers reject ids that start with a digit or contain symbols.
func makeXsollaUserID(playerID string) string {
	base := strings.ToLower(strings.TrimSpace(playerID))
	var b strings.Builder
	b.Grow(len(base) + 2)
	b.WriteString("u_")

	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune(r)
		case r == '-', r == '.', r == '@':
			b.WriteRune('_')
		}
	}

	result := b.String()
	if result == "u_" {
		result = "u_player"
	}
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

// clientIP extracts the best-guess public IP of the caller so Xsolla can
// detect the user's country/currency when creating a payment token.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xrip := r.Header.Get("X-Real-Ip"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return strings.Trim(host, "[]")
}

// ── Webhook notification types and parsing ───────────────────────

type webhookNotification struct {
	NotificationType string              `json:"notification_type"`
	User             *webhookUser        `json:"user,omitempty"`
	Transaction      *webhookTransaction `json:"transaction,omitempty"`
	Purchase         *webhookPurchase    `json:"purchase,omitempty"`
	CustomParameters map[string]string   `json:"custom_parameters,omitempty"`
}

type webhookUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type webhookTransaction struct {
	ID            int64   `json:"id"`
	ExternalID    string  `json:"external_id"`
	DryRun        int     `json:"dry_run"`
	PaymentDate   string  `json:"payment_date"`
	PaymentMethod int     `json:"payment_method"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
}

type webhookPurchase struct {
	VirtualCurrency *webhookVCPurchase   `json:"virtual_currency,omitempty"`
	VirtualItems    *webhookVirtualItems `json:"virtual_items,omitempty"`
	Total           *webhookTotal        `json:"total,omitempty"`
}

type webhookVCPurchase struct {
	Name     string  `json:"name"`
	SKU      string  `json:"sku"`
	Quantity float64 `json:"quantity"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type webhookVirtualItems struct {
	Items []webhookVItem `json:"items"`
}

type webhookVItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type webhookTotal struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}
