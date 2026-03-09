package authfile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type File struct {
	OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
	AuthMode     string  `json:"auth_mode,omitempty"`
	LastRefresh  string  `json:"last_refresh,omitempty"`
	Tokens       Tokens  `json:"tokens"`
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	AccountID    string `json:"account_id"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

type Metadata struct {
	Email             string
	PlanType          string
	SubscriptionUntil string
	AccountID         string
	UserID            string
}

func Parse(raw []byte) (File, error) {
	var file File
	if err := json.Unmarshal(raw, &file); err != nil {
		return File{}, err
	}
	return file, nil
}

func DecodeMetadata(file File) Metadata {
	claims, err := decodeJWTClaims(file.Tokens.IDToken)
	if err != nil {
		return Metadata{AccountID: file.Tokens.AccountID}
	}

	authSection, _ := claims["https://api.openai.com/auth"].(map[string]any)
	return Metadata{
		Email:             stringValue(claims["email"]),
		PlanType:          stringValue(authSection["chatgpt_plan_type"]),
		SubscriptionUntil: stringValue(authSection["chatgpt_subscription_active_until"]),
		AccountID:         firstNonEmpty(stringValue(authSection["chatgpt_account_id"]), file.Tokens.AccountID),
		UserID:            stringValue(authSection["user_id"]),
	}
}

func DefaultLabel(meta Metadata) string {
	if meta.Email != "" {
		return meta.Email
	}
	if meta.UserID != "" {
		return meta.UserID
	}
	if meta.AccountID != "" {
		return meta.AccountID
	}
	return "unnamed-account"
}

func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt")
	}

	payload := parts[1]
	payload += strings.Repeat("=", (4-len(payload)%4)%4)
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
