// Package polar implements Polar AccessLink's OAuth and exercise APIs.
package polar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/activity"
)

const (
	Provider         = activity.Provider("polar")
	authorizationURL = "https://flow.polar.com/oauth2/authorization"
	tokenURL         = "https://polarremote.com/v2/oauth2/token"
	apiURL           = "https://www.polaraccesslink.com/v3"
)

var (
	ErrUnauthorized = errors.New("polar authorization is no longer valid")
	ErrRateLimited  = errors.New("polar is temporarily rate limited")
)

type Client struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	HTTPClient       *http.Client
	Now              func() time.Time
	AuthorizationURL string
	TokenURL         string
	APIURL           string
}

type Token struct{ AccessToken, UserID string }

func (c Client) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}
func (c Client) AuthorizeURL(state string) string {
	endpoint := c.AuthorizationURL
	if endpoint == "" {
		endpoint = authorizationURL
	}
	values := url.Values{"response_type": {"code"}, "client_id": {c.ClientID}, "redirect_uri": {c.RedirectURL}, "state": {state}}
	return endpoint + "?" + values.Encode()
}

func (c Client) ExchangeCode(ctx context.Context, code string) (Token, error) {
	if !c.Enabled() {
		return Token{}, errors.New("polar integration is not configured")
	}
	endpoint := c.TokenURL
	if endpoint == "" {
		endpoint = tokenURL
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(c.ClientID, c.ClientSecret)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("exchange Polar authorization: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Token{}, c.responseError(response)
	}
	var payload struct {
		AccessToken string          `json:"access_token"`
		XUserID     json.RawMessage `json:"x_user_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return Token{}, errors.New("read Polar authorization response")
	}
	userID := strings.Trim(string(payload.XUserID), `"`)
	if payload.AccessToken == "" || userID == "" {
		return Token{}, errors.New("Polar authorization response is incomplete")
	}
	return Token{AccessToken: payload.AccessToken, UserID: userID}, nil
}

func (c Client) Provider() activity.Provider { return Provider }
func (c Client) Capabilities() activity.Capabilities {
	return activity.Capabilities{RecentSync: true, Backfill: false, Webhooks: false, Disconnect: true}
}
func (c Client) ConnectionStatus(ctx context.Context, credentials activity.Secret) (activity.ConnectionStatus, error) {
	if len(credentials.Bytes()) == 0 {
		return activity.ConnectionStatus{RequiresReauthorization: true}, nil
	}
	return activity.ConnectionStatus{Connected: true}, nil
}
func (c Client) Backfill(context.Context, activity.Secret, activity.SyncRequest) (activity.SyncPage, error) {
	return activity.SyncPage{}, activity.ErrUnsupported
}
func (c Client) IngestWebhook(context.Context, activity.WebhookEnvelope) ([]activity.WebhookEvent, error) {
	return nil, activity.ErrUnsupported
}
func (c Client) Disconnect(ctx context.Context, credentials activity.Secret) error {
	// AccessLink has no token-revocation endpoint. Local credential destruction
	// is therefore the effective disconnect operation.
	return nil
}

func (c Client) SyncRecent(ctx context.Context, credentials activity.Secret, request activity.SyncRequest) (activity.SyncPage, error) {
	if err := request.Validate(); err != nil {
		return activity.SyncPage{}, err
	}
	token, remoteUserID, err := splitCredential(credentials)
	if err != nil {
		return activity.SyncPage{}, err
	}
	if err = c.registerUser(ctx, token, remoteUserID); err != nil {
		return activity.SyncPage{}, err
	}
	transactionID, err := c.createTransaction(ctx, token, remoteUserID, request.Since, request.Until)
	if err != nil {
		return activity.SyncPage{}, err
	}
	defer c.deleteTransaction(context.Background(), token, remoteUserID, transactionID)
	exercises, err := c.listExercises(ctx, token, remoteUserID, transactionID)
	if err != nil {
		return activity.SyncPage{}, err
	}
	page := activity.SyncPage{Complete: true}
	for _, exercise := range exercises {
		normalized, normalizeErr := normalizeExercise(exercise)
		if normalizeErr != nil {
			return activity.SyncPage{}, normalizeErr
		}
		page.Activities = append(page.Activities, normalized)
	}
	return page, nil
}

func Credential(token Token) activity.Secret {
	payload, _ := json.Marshal(struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
	}{token.AccessToken, token.UserID})
	return activity.NewSecret(payload)
}
func splitCredential(credentials activity.Secret) (string, string, error) {
	var value struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
	}
	if err := json.Unmarshal(credentials.Bytes(), &value); err != nil || value.AccessToken == "" || value.UserID == "" {
		return "", "", ErrUnauthorized
	}
	return value.AccessToken, value.UserID, nil
}

func (c Client) registerUser(ctx context.Context, token, userID string) error {
	response, err := c.request(ctx, http.MethodPost, "/users", token, strings.NewReader(`{"member-id":"`+userID+`"}`))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusConflict {
		return nil
	}
	return c.responseError(response)
}
func (c Client) createTransaction(ctx context.Context, token, userID string, since, until time.Time) (string, error) {
	payload, _ := json.Marshal(map[string]string{"from": since.Format("2006-01-02"), "to": until.Add(-time.Nanosecond).Format("2006-01-02")})
	response, err := c.request(ctx, http.MethodPost, "/users/"+url.PathEscape(userID)+"/exercise-transactions", token, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", c.responseError(response)
	}
	var value struct {
		ID json.RawMessage `json:"transaction-id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value); err != nil {
		return "", errors.New("read Polar sync transaction")
	}
	id := strings.Trim(string(value.ID), `"`)
	if id == "" {
		return "", errors.New("Polar sync transaction is incomplete")
	}
	return id, nil
}
func (c Client) listExercises(ctx context.Context, token, userID, transactionID string) ([]exercise, error) {
	response, err := c.request(ctx, http.MethodGet, "/users/"+url.PathEscape(userID)+"/exercise-transactions/"+url.PathEscape(transactionID), token, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, c.responseError(response)
	}
	var value struct {
		Exercises []exercise `json:"exercises"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&value); err != nil {
		return nil, errors.New("read Polar exercises")
	}
	return value.Exercises, nil
}
func (c Client) deleteTransaction(ctx context.Context, token, userID, transactionID string) {
	response, err := c.request(ctx, http.MethodDelete, "/users/"+url.PathEscape(userID)+"/exercise-transactions/"+url.PathEscape(transactionID), token, nil)
	if err == nil && response != nil {
		response.Body.Close()
	}
}
func (c Client) request(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, error) {
	endpoint := strings.TrimRight(c.APIURL, "/")
	if endpoint == "" {
		endpoint = apiURL
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Polar: %w", err)
	}
	return response, nil
}
func (c Client) responseError(response *http.Response) error {
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return fmt.Errorf("Polar returned %s", response.Status)
	}
}
func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

type exercise struct {
	ID                json.RawMessage `json:"id"`
	StartTime         string          `json:"start_time"`
	Duration          string          `json:"duration"`
	Sport             string          `json:"sport"`
	DetailedSportInfo string          `json:"detailed_sport_info"`
	Distance          *float64        `json:"distance"`
	HeartRate         struct {
		Average *int16 `json:"average"`
		Maximum *int16 `json:"maximum"`
	} `json:"heart_rate"`
	Modified string `json:"modified"`
}

func normalizeExercise(value exercise) (activity.NormalizedActivity, error) {
	id := strings.Trim(string(value.ID), `"`)
	start, err := time.Parse(time.RFC3339, value.StartTime)
	if err != nil {
		return activity.NormalizedActivity{}, errors.New("Polar exercise has an invalid start time")
	}
	duration, err := parseDuration(value.Duration)
	if err != nil || duration <= 0 {
		return activity.NormalizedActivity{}, errors.New("Polar exercise has an invalid duration")
	}
	sport := strings.TrimSpace(value.DetailedSportInfo)
	if sport == "" {
		sport = strings.TrimSpace(value.Sport)
	}
	if sport == "" {
		return activity.NormalizedActivity{}, errors.New("Polar exercise has no sport")
	}
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	normalized := normalizeSport(sport)
	var updated *time.Time
	if value.Modified != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value.Modified)
		if parseErr == nil {
			updated = &parsed
		}
	}
	result := activity.NormalizedActivity{ProviderActivityID: id, ProviderUpdatedAt: updated, StartsAt: start, EndsAt: start.Add(duration), Sport: sport, NormalizedSport: normalized, DurationSeconds: int(duration.Seconds()), DistanceMetres: value.Distance, AverageHeartRate: value.HeartRate.Average, MaximumHeartRate: value.HeartRate.Maximum, ProviderMetricsJSON: raw, RawSummaryJSON: raw, PayloadSHA256: hash, NormalizationVersion: 1}
	return result, result.Validate()
}
func parseDuration(value string) (time.Duration, error) {
	if !strings.HasPrefix(value, "PT") || len(value) == 2 {
		return 0, errors.New("invalid duration")
	}
	remaining := value[2:]
	var duration time.Duration
	for remaining != "" {
		end := 0
		for end < len(remaining) && ((remaining[end] >= '0' && remaining[end] <= '9') || remaining[end] == '.') {
			end++
		}
		if end == 0 || end == len(remaining) {
			return 0, errors.New("invalid duration")
		}
		amount, err := strconv.ParseFloat(remaining[:end], 64)
		if err != nil || amount < 0 {
			return 0, errors.New("invalid duration")
		}
		switch remaining[end] {
		case 'H':
			duration += time.Duration(amount * float64(time.Hour))
		case 'M':
			duration += time.Duration(amount * float64(time.Minute))
		case 'S':
			duration += time.Duration(amount * float64(time.Second))
		default:
			return 0, errors.New("invalid duration")
		}
		remaining = remaining[end+1:]
	}
	return duration, nil
}
func normalizeSport(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "_", "-", "_").Replace(value)
	if value == "" {
		return "other"
	}
	return value
}
