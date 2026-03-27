package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"altclaw.ai/internal/config"
	webpush "github.com/SherClockHolmes/webpush-go"
)

// pushPayload is the JSON body sent to the browser's push service.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
}

// SendPush sends a Web Push notification to all subscriptions for the workspace.
// Invalid/expired subscriptions (HTTP 410) are auto-removed.
func SendPush(store *config.Store, title, body string) {
	ctx := context.Background()

	cfg := store.Config()
	if cfg == nil || cfg.VAPIDPublicKey == "" || cfg.VAPIDPrivateKey == "" {
		return // VAPID not configured
	}

	workspaceID := store.Workspace().ID

	subs, err := store.ListPushSubscriptions(ctx, workspaceID)
	if err != nil || len(subs) == 0 {
		return
	}

	payload, _ := json.Marshal(pushPayload{
		Title: title,
		Body:  body,
		URL:   "/app/",
	})

	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		go func(s *config.PushSubscription) {
			defer wg.Done()
			resp, err := webpush.SendNotification(payload, &webpush.Subscription{
				Endpoint: s.Endpoint,
				Keys: webpush.Keys{
					P256dh: s.P256dh,
					Auth:   s.Auth,
				},
			}, &webpush.Options{
				VAPIDPublicKey:  cfg.VAPIDPublicKey,
				VAPIDPrivateKey: cfg.VAPIDPrivateKey,
				Subscriber:      "mailto:altclaw@localhost",
			})
			if err != nil {
				slog.Warn("push notification failed", "endpoint", s.Endpoint, "error", err)
				return
			}
			defer resp.Body.Close()
			// Remove expired/invalid subscriptions (410 Gone or 404 Not Found)
			if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
				slog.Info("removing expired push subscription", "endpoint", s.Endpoint)
				_ = store.DeletePushSubscriptionByEndpoint(ctx, workspaceID, s.Endpoint)
			}
		}(sub)
	}
	wg.Wait()
}
