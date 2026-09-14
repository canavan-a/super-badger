package opencode

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TestModelConnection dials the model endpoint's own OpenAI-compatible
// /models route directly — bypassing opencode entirely — so a user can
// validate a base URL/API key before ever creating a Station or an opencode
// session against it.
func TestModelConnection(ctx context.Context, baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s responded with status %d", url, resp.StatusCode)
	}
	return nil
}
