package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/datamitsu/datamitsu/internal/httpretry"
)

// RetryNotifier is told about every repeated registry request, so a command
// can show what it is waiting for. Nil retries silently.
var RetryNotifier httpretry.Notifier

// errNotFound marks a 404, which a caller turns into a message naming what
// was looked up.
var errNotFound = errors.New("not found")

// getJSON performs a GET under the shared retry policy and decodes the body,
// capped at limit bytes, into target. A 404 is errNotFound and any other 4xx
// is permanent; 5xx, 429 (waiting out a Retry-After), network failures and a
// body cut short are retried and reported through RetryNotifier. source names
// the service in errors.
func getJSON(ctx context.Context, client *http.Client, source, url string, limit int64, target any) error {
	return httpretry.Retry(ctx, "GET "+url, RetryNotifier, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return httpretry.Permanent(fmt.Errorf("failed to build request: %w", err))
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%s request failed: %w", source, err)
		}
		defer func() { _ = resp.Body.Close() }()

		switch {
		case resp.StatusCode == http.StatusNotFound:
			return httpretry.Permanent(errNotFound)
		case resp.StatusCode != http.StatusOK:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			err := fmt.Errorf("%s returned status %d: %s", source, resp.StatusCode, string(body))
			if httpretry.RetryableStatus(resp.StatusCode) {
				return httpretry.WithRetryAfter(err, resp)
			}
			return httpretry.Permanent(err)
		}

		if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(target); err != nil {
			return httpretry.ClassifyDecode(fmt.Errorf("failed to decode %s response: %w", source, err))
		}
		return nil
	})
}
