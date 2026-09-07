// Package polar implements the allowlisted Polar AccessLink protocol boundary.
package polar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/activity"
)

const (
	ProviderCode       = activity.Provider("polar")
	AuthorizationScope = "accesslink.read_all"
	defaultAuthURL     = "https://flow.polar.com/oauth2/authorization"
	defaultTokenURL    = "https://polarremote.com/v2/oauth2/token"
	defaultAPIBaseURL  = "https://www.polaraccesslink.com"
	maxResponseBytes   = 4 << 20
)

type ErrorKind string

const (
	ErrorReauthorization ErrorKind = "reauthorization_required"
	ErrorConsent         ErrorKind = "consent_required"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorConflict        ErrorKind = "identity_conflict"
	ErrorTransient       ErrorKind = "provider_unavailable"
	ErrorInvalidResponse ErrorKind = "invalid_provider_response"
)

type ProviderError struct {
	Kind       ErrorKind
	StatusCode int
	RetryAfter time.Time
}

func (e *ProviderError) Error() string { return "Polar request failed: " + string(e.Kind) }

func ErrorKindOf(err error) ErrorKind {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Kind
	}
	return ErrorTransient
}

func RetryAfterOf(err error) *time.Time {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.RetryAfter.IsZero() {
		return nil
	}
	value := providerErr.RetryAfter
	return &value
}

type Config struct {
	ClientID, ClientSecret, RedirectURL    string
	AuthorizationURL, TokenURL, APIBaseURL string
	HTTPClient                             *http.Client
	Now                                    func() time.Time
}

type Client struct {
	clientID, clientSecret, redirectURL    string
	authorizationURL, tokenURL, apiBaseURL string
	httpClient                             *http.Client
	now                                    func() time.Time
}

func NewClient(config Config) (*Client, error) {
	config.ClientID, config.ClientSecret, config.RedirectURL = strings.TrimSpace(config.ClientID), strings.TrimSpace(config.ClientSecret), strings.TrimSpace(config.RedirectURL)
	if config.ClientID == "" || config.ClientSecret == "" {
		return nil, errors.New("Polar client id and secret are required")
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" || redirect.RawQuery != "" || redirect.Fragment != "" {
		return nil, errors.New("Polar redirect URL must be an absolute URL without query or fragment")
	}
	if config.AuthorizationURL == "" {
		config.AuthorizationURL = defaultAuthURL
	}
	if config.TokenURL == "" {
		config.TokenURL = defaultTokenURL
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = defaultAPIBaseURL
	}
	for _, raw := range []string{config.AuthorizationURL, config.TokenURL, config.APIBaseURL} {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("Polar endpoint must be an absolute URL")
		}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	httpClient := *config.HTTPClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Client{clientID: config.ClientID, clientSecret: config.ClientSecret, redirectURL: config.RedirectURL, authorizationURL: config.AuthorizationURL, tokenURL: config.TokenURL, apiBaseURL: strings.TrimRight(config.APIBaseURL, "/"), httpClient: &httpClient, now: config.Now}, nil
}

func (c *Client) AuthorizationURL(state string) (string, error) {
	if len(state) < 32 || len(state) > 512 {
		return "", errors.New("Polar OAuth state must contain 32 to 512 characters")
	}
	u, err := url.Parse(c.authorizationURL)
	if err != nil {
		return "", errors.New("parse Polar authorization URL")
	}
	query := u.Query()
	query.Set("response_type", "code")
	query.Set("client_id", c.clientID)
	query.Set("redirect_uri", c.redirectURL)
	query.Set("scope", AuthorizationScope)
	query.Set("state", state)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

type Credentials struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RemoteUserID int64     `json:"remote_user_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (c Credentials) Validate() error {
	if strings.TrimSpace(c.AccessToken) == "" || !strings.EqualFold(c.TokenType, "bearer") || c.RemoteUserID <= 0 || c.ExpiresAt.IsZero() {
		return errors.New("Polar credentials are incomplete")
	}
	return nil
}

func (c Credentials) Secret() (activity.Secret, error) {
	if err := c.Validate(); err != nil {
		return activity.Secret{}, err
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return activity.Secret{}, errors.New("encode Polar credentials")
	}
	return activity.NewSecret(encoded), nil
}

func CredentialsFromSecret(secret activity.Secret) (Credentials, error) {
	var credentials Credentials
	if err := json.Unmarshal(secret.Bytes(), &credentials); err != nil || credentials.Validate() != nil {
		return Credentials{}, errors.New("decode Polar credentials")
	}
	return credentials, nil
}

func (c *Client) ExchangeCode(ctx context.Context, code string) (Credentials, error) {
	if strings.TrimSpace(code) == "" || len(code) > 2048 {
		return Credentials{}, errors.New("Polar authorization code is invalid")
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.redirectURL}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return Credentials{}, errors.New("create Polar token request")
	}
	request.SetBasicAuth(c.clientID, c.clientSecret)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Credentials{}, &ProviderError{Kind: ErrorTransient}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Credentials{}, c.providerError(response)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RemoteUserID int64  `json:"x_user_id"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil || payload.ExpiresIn <= 0 {
		return Credentials{}, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
	}
	credentials := Credentials{AccessToken: payload.AccessToken, TokenType: payload.TokenType, RemoteUserID: payload.RemoteUserID, ExpiresAt: c.now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)}
	if err := credentials.Validate(); err != nil {
		return Credentials{}, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
	}
	return credentials, nil
}

func (c *Client) RegisterUser(ctx context.Context, credentials Credentials, memberID string) error {
	if err := credentials.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(memberID) == "" || len(memberID) > 255 {
		return errors.New("Polar member id is invalid")
	}
	body, _ := json.Marshal(map[string]string{"member-id": memberID})
	response, err := c.doAPI(ctx, http.MethodPost, "/v3/users", credentials.AccessToken, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.providerError(response)
	}
	return &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
}

func (c *Client) DeregisterUser(ctx context.Context, credentials Credentials) error {
	if err := credentials.Validate(); err != nil {
		return err
	}
	response, err := c.doAPI(ctx, http.MethodDelete, "/v3/users/"+strconv.FormatInt(credentials.RemoteUserID, 10), credentials.AccessToken, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return c.providerError(response)
	}
	return nil
}

type exercise struct {
	ID                 string   `json:"id"`
	UploadTime         string   `json:"upload_time"`
	StartTime          string   `json:"start_time"`
	StartOffsetMinutes int      `json:"start_time_utc_offset"`
	Duration           string   `json:"duration"`
	Distance           *float64 `json:"distance"`
	Sport              string   `json:"sport"`
	DetailedSport      string   `json:"detailed_sport_info"`
	HeartRate          *struct {
		Average int16 `json:"average"`
		Maximum int16 `json:"maximum"`
	} `json:"heart_rate"`
	HeartRateZones []struct {
		Index  int    `json:"index"`
		Lower  int    `json:"lower-limit"`
		Upper  int    `json:"upper-limit"`
		InZone string `json:"in-zone"`
	} `json:"heart_rate_zones"`
	TrainingLoadPro *struct {
		Date                    string   `json:"date"`
		CardioLoad              *float64 `json:"cardio-load"`
		MuscleLoad              *float64 `json:"muscle-load"`
		PerceivedLoad           *float64 `json:"perceived-load"`
		CardioInterpretation    string   `json:"cardio-load-interpretation"`
		MuscleInterpretation    string   `json:"muscle-load-interpretation"`
		PerceivedInterpretation string   `json:"perceived-load-interpretation"`
		UserRPE                 string   `json:"user-rpe"`
	} `json:"training_load_pro"`
}

func (c *Client) Exercises(ctx context.Context, credentials Credentials) ([]activity.NormalizedActivity, error) {
	response, err := c.doAPI(ctx, http.MethodGet, "/v3/exercises?samples=false&zones=true&route=false", credentials.AccessToken, nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, c.providerError(response)
	}
	var exercises []exercise
	if err := decodeJSON(response.Body, &exercises); err != nil {
		return nil, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
	}
	result := make([]activity.NormalizedActivity, 0, len(exercises))
	for _, source := range exercises {
		normalized, normalizeErr := normalizeExercise(source)
		if normalizeErr != nil {
			return nil, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
		}
		result = append(result, normalized)
	}
	return result, nil
}

type cardioLoad struct {
	Date      string   `json:"date"`
	Status    string   `json:"cardio_load_status"`
	Load      *float64 `json:"cardio_load"`
	Strain    *float64 `json:"strain"`
	Tolerance *float64 `json:"tolerance"`
	Ratio     *float64 `json:"cardio_load_ratio"`
}

func (c *Client) CardioLoads(ctx context.Context, credentials Credentials) ([]activity.LoadObservation, error) {
	response, err := c.doAPI(ctx, http.MethodGet, "/v3/users/cardio-load", credentials.AccessToken, nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, c.providerError(response)
	}
	var loads []cardioLoad
	if err := decodeJSON(response.Body, &loads); err != nil {
		return nil, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
	}
	fetchedAt := c.now().UTC()
	result := make([]activity.LoadObservation, 0, len(loads))
	for _, source := range loads {
		observedOn, parseErr := time.Parse("2006-01-02", source.Date)
		if parseErr != nil || strings.TrimSpace(source.Status) == "" {
			return nil, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
		}
		observation := activity.LoadObservation{ObservedOn: observedOn.UTC(), Method: "polar_cardio_load_trimp", AvailabilityStatus: source.Status, LoadValue: source.Load, Strain7Days: source.Strain, Tolerance28Days: source.Tolerance, Ratio: source.Ratio, ProviderMetricsJSON: []byte(`{}`), FetchedAt: fetchedAt}
		if source.Status == "LOAD_STATUS_NOT_AVAILABLE" {
			observation.LoadValue, observation.Strain7Days, observation.Tolerance28Days, observation.Ratio = nil, nil, nil, nil
		}
		if validateErr := observation.Validate(); validateErr != nil {
			return nil, &ProviderError{Kind: ErrorInvalidResponse, StatusCode: response.StatusCode}
		}
		result = append(result, observation)
	}
	return result, nil
}

func (c *Client) doAPI(ctx context.Context, method, path, token string, body io.Reader, contentType string) (*http.Response, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Polar access token is missing")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.apiBaseURL+path, body)
	if err != nil {
		return nil, errors.New("create Polar API request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Kind: ErrorTransient}
	}
	return response, nil
}

func (c *Client) providerError(response *http.Response) error {
	kind := ErrorTransient
	switch response.StatusCode {
	case http.StatusUnauthorized:
		kind = ErrorReauthorization
	case http.StatusForbidden:
		kind = ErrorConsent
	case http.StatusConflict:
		kind = ErrorConflict
	case http.StatusTooManyRequests:
		kind = ErrorRateLimited
	default:
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			kind = ErrorInvalidResponse
		}
	}
	err := &ProviderError{Kind: kind, StatusCode: response.StatusCode}
	if kind == ErrorRateLimited {
		if seconds, parseErr := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); parseErr == nil && seconds >= 0 {
			err.RetryAfter = c.now().UTC().Add(time.Duration(seconds) * time.Second)
		}
	}
	return err
}

func decodeJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBytes+1))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("provider response contains trailing data or exceeds the size limit")
	}
	return nil
}

var polarDuration = regexp.MustCompile(`^PT(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?$`)

func parseDuration(value string) (time.Duration, error) {
	matches := polarDuration.FindStringSubmatch(value)
	if matches == nil || (matches[1] == "" && matches[2] == "" && matches[3] == "") {
		return 0, errors.New("invalid Polar duration")
	}
	seconds := 0.0
	for index, multiplier := range []float64{3600, 60, 1} {
		if matches[index+1] == "" {
			continue
		}
		part, err := strconv.ParseFloat(matches[index+1], 64)
		if err != nil {
			return 0, errors.New("invalid Polar duration")
		}
		seconds += part * multiplier
	}
	if seconds <= 0 {
		return 0, errors.New("invalid Polar duration")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func normalizeExercise(source exercise) (activity.NormalizedActivity, error) {
	duration, err := parseDuration(source.Duration)
	if err != nil {
		return activity.NormalizedActivity{}, err
	}
	location := time.FixedZone("Polar exercise", source.StartOffsetMinutes*60)
	startsAt, err := time.ParseInLocation("2006-01-02T15:04:05", source.StartTime, location)
	if err != nil {
		return activity.NormalizedActivity{}, err
	}
	uploadedAt, err := time.Parse(time.RFC3339Nano, source.UploadTime)
	if err != nil {
		return activity.NormalizedActivity{}, err
	}
	metrics := map[string]any{"detailed_sport_info": source.DetailedSport, "heart_rate_zones": source.HeartRateZones, "training_load_pro": source.TrainingLoadPro}
	providerMetrics, _ := json.Marshal(metrics)
	allowlisted := map[string]any{"id": source.ID, "upload_time": source.UploadTime, "start_time": source.StartTime, "start_time_utc_offset": source.StartOffsetMinutes, "duration": source.Duration, "distance": source.Distance, "heart_rate": source.HeartRate, "sport": source.Sport, "detailed_sport_info": source.DetailedSport, "heart_rate_zones": source.HeartRateZones, "training_load_pro": source.TrainingLoadPro}
	rawSummary, _ := json.Marshal(allowlisted)
	hash := sha256.Sum256(rawSummary)
	var average, maximum *int16
	if source.HeartRate != nil {
		average, maximum = &source.HeartRate.Average, &source.HeartRate.Maximum
	}
	seconds := int(duration.Round(time.Second) / time.Second)
	normalized := activity.NormalizedActivity{ProviderActivityID: source.ID, ProviderUpdatedAt: &uploadedAt, StartsAt: startsAt.UTC(), EndsAt: startsAt.Add(duration).UTC(), Sport: source.Sport, NormalizedSport: normalizedSport(source.Sport, source.DetailedSport), DurationSeconds: seconds, DistanceMetres: source.Distance, AverageHeartRate: average, MaximumHeartRate: maximum, ProviderMetricsJSON: providerMetrics, RawSummaryJSON: rawSummary, PayloadSHA256: hash, NormalizationVersion: 1}
	if err := normalized.Validate(); err != nil {
		return activity.NormalizedActivity{}, err
	}
	return normalized, nil
}

func normalizedSport(sport, detail string) string {
	value := strings.ToUpper(sport + " " + detail)
	for _, token := range []string{"KAYAK", "CANOE", "PADDL", "WATERSPORT"} {
		if strings.Contains(value, token) {
			return "paddling"
		}
	}
	if strings.Contains(value, "RUN") {
		return "running"
	}
	if strings.Contains(value, "CYCL") || strings.Contains(value, "BIKE") {
		return "cycling"
	}
	if strings.Contains(value, "SWIM") {
		return "swimming"
	}
	if strings.Contains(value, "STRENGTH") || strings.Contains(value, "GYM") {
		return "strength"
	}
	return "other"
}

// Adapter implements the provider-neutral activity boundary with the Polar
// non-transactional recent-exercise and Cardio Load resources.
type Adapter struct{ Client *Client }

func (Adapter) Provider() activity.Provider { return ProviderCode }
func (Adapter) Capabilities() activity.Capabilities {
	return activity.Capabilities{RecentSync: true, Disconnect: true}
}
func (a Adapter) ConnectionStatus(_ context.Context, secret activity.Secret) (activity.ConnectionStatus, error) {
	credentials, err := CredentialsFromSecret(secret)
	if err != nil {
		return activity.ConnectionStatus{}, err
	}
	expired := !credentials.ExpiresAt.After(a.Client.now().UTC())
	return activity.ConnectionStatus{Connected: !expired, RequiresReauthorization: expired, RemoteUserID: strconv.FormatInt(credentials.RemoteUserID, 10)}, nil
}
func (a Adapter) SyncRecent(ctx context.Context, secret activity.Secret, request activity.SyncRequest) (activity.SyncPage, error) {
	if err := request.Validate(); err != nil {
		return activity.SyncPage{}, err
	}
	credentials, err := CredentialsFromSecret(secret)
	if err != nil {
		return activity.SyncPage{}, err
	}
	if !credentials.ExpiresAt.After(a.Client.now().UTC()) {
		return activity.SyncPage{}, &ProviderError{Kind: ErrorReauthorization}
	}
	exercises, err := a.Client.Exercises(ctx, credentials)
	if err != nil {
		return activity.SyncPage{}, err
	}
	loads, err := a.Client.CardioLoads(ctx, credentials)
	if err != nil {
		return activity.SyncPage{}, err
	}
	filtered := exercises[:0]
	for _, exercise := range exercises {
		if !exercise.StartsAt.Before(request.Since) && exercise.StartsAt.Before(request.Until) {
			filtered = append(filtered, exercise)
		}
	}
	if len(filtered) > request.Limit {
		filtered = filtered[:request.Limit]
	}
	return activity.SyncPage{Activities: filtered, LoadObservations: loads, Complete: true}, nil
}
func (Adapter) Backfill(context.Context, activity.Secret, activity.SyncRequest) (activity.SyncPage, error) {
	return activity.SyncPage{}, activity.ErrUnsupported
}
func (Adapter) IngestWebhook(context.Context, activity.WebhookEnvelope) ([]activity.WebhookEvent, error) {
	return nil, activity.ErrUnsupported
}
func (a Adapter) Disconnect(ctx context.Context, secret activity.Secret) error {
	credentials, err := CredentialsFromSecret(secret)
	if err != nil {
		return err
	}
	return a.Client.DeregisterUser(ctx, credentials)
}

func (c *Client) String() string { return "Polar AccessLink client" }
