package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

type Screen struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Orientation string `json:"orientation"`
}

type Config struct {
	StationID         int      `json:"station_id"`
	OfflineCacheHours int      `json:"offline_cache_hours"`
	Screens           []Screen `json:"screens"`
}

type Media struct {
	ID       int    `json:"id"`
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

type Entry struct {
	Position        int     `json:"position"`
	StartsAt        *string `json:"starts_at"`
	DurationSeconds int     `json:"duration_seconds"`
	ScreenIDs       []int   `json:"screen_ids"`
	Media           Media   `json:"media"`
}

type Package struct {
	Version    string  `json:"version"`
	ETag       string  `json:"etag"`
	Entries    []Entry `json:"entries"`
	HeaderETag string  `json:"-"`
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				req.Header.Del("Authorization")
				return nil
			},
		},
	}
}

func (c *Client) FetchConfig(ctx context.Context) (Config, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/agent/v1/config", "", nil)
	if err != nil {
		return Config{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return Config{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Config{}, errStatus(resp.StatusCode)
	}
	var cfg Config
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Client) FetchPackage(ctx context.Context, ifNoneMatch string) (Package, bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/agent/v2/package", ifNoneMatch, nil)
	if err != nil {
		return Package{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return Package{}, false, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusNotModified {
		return Package{}, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Package{}, false, errStatus(resp.StatusCode)
	}
	var pkg Package
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return Package{}, false, err
	}
	pkg.HeaderETag = resp.Header.Get("ETag")
	return pkg, false, nil
}

func (c *Client) PostPlayEvent(ctx context.Context, screenID, mediaID int, startedAt time.Time) (int, string, error) {
	payload := map[string]any{
		"events": []map[string]any{{
			"screen_id":      screenID,
			"media_asset_id": mediaID,
			"started_at":     startedAt.UTC().Format(time.RFC3339),
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/agent/v1/play_events", "", raw)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(body), nil
}

func (c *Client) do(ctx context.Context, method, path, ifNoneMatch string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	return c.HTTP.Do(req)
}

type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("hub status %d", e.code) }

func errStatus(code int) error { return statusError{code: code} }
