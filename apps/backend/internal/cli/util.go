package cli

import (
	"context"
	"net/http"
)

func httpRequest(ctx context.Context, method, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, nil)
}
