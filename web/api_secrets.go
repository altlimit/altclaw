package web

import (
	"context"
	"net/http"
	"strings"

	"altclaw.ai/internal/config"
	"github.com/altlimit/restruct"
)

// Secrets returns all secrets (workspace + profile) with masked values.
func (a *Api) Secrets(ctx context.Context) any {
	secrets, _ := a.server.store.ListSecrets(ctx, a.server.store.Workspace().ID)

	result := make([]config.Secret, 0, len(secrets))
	for _, s := range secrets {
		result = append(result, s.Masked())
	}
	return result
}

// SaveSecret creates or updates a secret.
func (a *Api) SaveSecret(ctx context.Context, sec *config.Secret) any {
	if sec.ID == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "name is required"}
	}
	if sec.Workspace != "" {
		sec.Workspace = a.server.store.Workspace().ID
	}

	// Block saving a local secret that shadows a profile-provisioned one
	if pd := a.server.store.GetProfile(); pd != nil {
		for _, ps := range pd.Secrets {
			if ps.ID == sec.ID {
				return restruct.Error{Status: http.StatusBadRequest, Message: "secret is managed by profile and cannot be overridden locally"}
			}
		}
	}

	// On update, preserve existing encrypted value if caller sends masked/empty value
	existing, err := a.server.store.GetSecret(ctx, sec.Workspace, sec.ID)
	if err == nil {
		if sec.Value == "" || strings.Contains(sec.Value, "***") {
			sec.Value = existing.Value
		}
	} else if sec.Value == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "value is required for new secret"}
	}

	if err := a.server.store.SaveSecret(ctx, sec); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]any{"status": "saved", "id": sec.ID}
}

// DeleteSecret removes a secret.
func (a *Api) DeleteSecret(ctx context.Context, req *struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
}) any {
	ns := ""
	if req.Workspace != "" {
		ns = a.server.store.Workspace().ID
	}
	if err := a.server.store.DeleteSecret(ctx, ns, req.ID); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Message: err.Error()}
	}
	return map[string]string{"status": "deleted"}
}
