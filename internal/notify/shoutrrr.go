// Package notify handles all outbound messaging: fire-and-forget multi-channel
// notifications via shoutrrr, and the interactive Telegram approval flow.
package notify

import (
	"fmt"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/types"
)

// SendAll sends a plain-text message to every configured shoutrrr URL.
// It returns a combined error list (nil if all succeeded). Sending is
// best-effort: one bad URL does not stop the others.
func SendAll(urls []string, title, message string) []error {
	var errs []error
	for _, u := range urls {
		if u == "" {
			continue
		}
		sender, err := shoutrrr.CreateSender(u)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", redact(u), err))
			continue
		}
		params := types.Params{}
		if title != "" {
			params["title"] = title
		}
		if sendErrs := sender.Send(message, &params); len(sendErrs) > 0 {
			for _, e := range sendErrs {
				if e != nil {
					errs = append(errs, fmt.Errorf("%s: %w", redact(u), e))
				}
			}
		}
	}
	return errs
}

// redact hides credentials in a shoutrrr URL for logging.
func redact(u string) string {
	if len(u) < 12 {
		return u
	}
	// keep scheme, mask the rest lightly
	for i := 0; i < len(u); i++ {
		if u[i] == ':' && i+2 < len(u) && u[i+1] == '/' {
			return u[:i] + "://***"
		}
	}
	return u[:8] + "***"
}
