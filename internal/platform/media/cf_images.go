package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CFImagesClient wraps the Cloudflare Images API for direct-upload
// URL generation and image deletion.
type CFImagesClient struct {
	accountID  string
	apiToken   string
	httpClient *http.Client
}

// NewCFImagesClient creates a Cloudflare Images client.
func NewCFImagesClient(accountID, apiToken string) *CFImagesClient {
	return &CFImagesClient{
		accountID: accountID,
		apiToken:  apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// UploadURLResult contains the one-time upload URL and the image ID
// that Cloudflare will assign to the uploaded image.
type UploadURLResult struct {
	UploadURL string
	ImageID   string
}

// UploadURL requests a one-time direct upload URL from Cloudflare Images.
// The browser uploads directly to this URL — no file data passes through
// the Go server. The returned ImageID should be persisted in the DB after
// the browser confirms the upload succeeded.
func (c *CFImagesClient) UploadURL(ctx context.Context) (*UploadURLResult, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/images/v2/direct_upload", c.accountID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cf images create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cf images upload url: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cf images read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cf images upload url: status %d: %s", resp.StatusCode, body)
	}

	var cfResp cfDirectUploadResponse
	if err := json.Unmarshal(body, &cfResp); err != nil {
		return nil, fmt.Errorf("cf images parse response: %w", err)
	}

	if !cfResp.Success {
		return nil, fmt.Errorf("cf images upload url: %v", cfResp.Errors)
	}

	return &UploadURLResult{
		UploadURL: cfResp.Result.UploadURL,
		ImageID:   cfResp.Result.ID,
	}, nil
}

// Delete removes an image from Cloudflare Images by ID.
func (c *CFImagesClient) Delete(ctx context.Context, imageID string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/images/v1/%s", c.accountID, imageID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("cf images create delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cf images delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cf images delete: status %d: %s", resp.StatusCode, body)
	}

	return nil
}

// Cloudflare API response types.

type cfDirectUploadResponse struct {
	Success bool             `json:"success"`
	Errors  []cfError        `json:"errors"`
	Result  cfDirectUpResult `json:"result"`
}

type cfDirectUpResult struct {
	ID        string `json:"id"`
	UploadURL string `json:"uploadURL"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
