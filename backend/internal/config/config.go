package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                string
	DatabaseURL         string
	XsollaJWKSURL       string
	XsollaIssuer        string
	XsollaAudience      string
	XsollaProjectID     int
	XsollaMerchantID    int
	XsollaAPIKey        string
	XsollaSandbox       bool
	XsollaPayCountry    string
	XsollaPayCurrency   string
	XsollaCatalogURL    string
	XsollaWebhookSecret string
	AllowedOrigins      string
	StaticDir           string
}

func Load() Config {
	projectID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("XSOLLA_PROJECT_ID")))
	merchantID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("XSOLLA_MERCHANT_ID")))
	sandbox, err := strconv.ParseBool(getEnv("XSOLLA_SANDBOX", "true"))
	if err != nil {
		sandbox = true
	}

	cfg := Config{
		Port:                getEnv("PORT", "8080"),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://postgres:123456@localhost:5432/spaceminergame?sslmode=disable"),
		XsollaJWKSURL:       strings.TrimSpace(os.Getenv("XSOLLA_JWKS_URL")),
		XsollaIssuer:        strings.TrimSpace(os.Getenv("XSOLLA_ISSUER")),
		XsollaAudience:      strings.TrimSpace(os.Getenv("XSOLLA_AUDIENCE")),
		XsollaProjectID:     projectID,
		XsollaMerchantID:    merchantID,
		XsollaAPIKey:        strings.TrimSpace(os.Getenv("XSOLLA_API_KEY")),
		XsollaSandbox:       sandbox,
		XsollaPayCountry:    strings.TrimSpace(os.Getenv("XSOLLA_PAY_COUNTRY")),
		XsollaPayCurrency:   getEnv("XSOLLA_PAY_CURRENCY", "USD"),
		XsollaCatalogURL:    strings.TrimSpace(os.Getenv("XSOLLA_CATALOG_URL")),
		XsollaWebhookSecret: strings.TrimSpace(os.Getenv("XSOLLA_WEBHOOK_SECRET")),
		AllowedOrigins:      getEnv("ALLOWED_ORIGINS", "*"),
		StaticDir:           os.Getenv("STATIC_DIR"),
	}

	if cfg.XsollaJWKSURL == "" {
		log.Println("warning: XSOLLA_JWKS_URL is empty; JWT middleware will reject all tokens")
	}
	if cfg.XsollaProjectID == 0 {
		log.Println("warning: XSOLLA_PROJECT_ID is empty; catalog will use database fallback")
	}
	if cfg.XsollaAPIKey == "" {
		log.Println("warning: XSOLLA_API_KEY is empty; PayStation token creation will be unavailable")
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
