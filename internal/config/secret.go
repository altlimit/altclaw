package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/altlimit/dsorm"
)

// Secret is a securely stored credential for AI blind-usage.
// ID is the secret name (e.g. "OPENAI_KEY"), unique per namespace.
type Secret struct {
	dsorm.Base
	ID        string    `model:"id" json:"id"`
	Workspace string    `model:"ns" json:"workspace"`                  // empty = global, ID = workspace
	Value     string    `model:"value,encrypt" json:"value,omitempty"` // encrypted at rest
	CreatedAt time.Time `model:"created" datastore:"created,omitempty" json:"created"`
	UpdatedAt time.Time `model:"modified" datastore:"modified,omitempty" json:"modified"`
	// InMemory marks this secret as profile-provisioned (from hub profile).
	// It is never persisted — BeforeSave will reject it.
	InMemory bool `datastore:"-" json:"in_memory,omitempty"`
}

func (sec *Secret) BeforeSave(ctx context.Context, old dsorm.Model) error {
	if sec.InMemory {
		return fmt.Errorf("profile-provisioned secret cannot be saved locally")
	}
	if sec.ID == "" {
		return fmt.Errorf("secret name (ID) must not be empty")
	}
	return nil
}

func (sec *Secret) AfterSave(ctx context.Context, old dsorm.Model) error {
	action := "saved"
	if old == nil || old.IsNew() {
		action = "added"
	}
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"secret_updated","action":"%s","id":"%s"}`, action, sec.ID)))
	return nil
}

func (sec *Secret) AfterDelete(ctx context.Context) error {
	broadcast(ctx, []byte(fmt.Sprintf(`{"type":"secret_updated","action":"deleted","id":"%s"}`, sec.ID)))
	return nil
}

// Masked returns a copy of the Secret with the Value partially redacted.
// Safe to serialize for API responses without exposing the real secret.
func (sec *Secret) Masked() Secret {
	s := *sec
	v := s.Value
	if len(v) > 8 {
		v = v[:4] + "***" + v[len(v)-4:]
	} else if len(v) > 4 {
		v = v[:2] + "***"
	} else if len(v) > 0 {
		v = "***"
	}
	s.Value = v
	return s
}

// ── Secret CRUD ───────────────────────────────────────────────────────

// ListSecrets returns all secrets for a given namespace, with profile secrets first.
// Use workspace="" for user-level secrets.
func (s *Store) ListSecrets(ctx context.Context, workspace string) ([]*Secret, error) {
	q := dsorm.NewQuery("Secret").Namespace(workspace).Order("-created")
	secrets, _, err := dsorm.Query[*Secret](ctx, s.Client, q, "")
	if err != nil {
		return nil, err
	}
	if pd := s.GetProfile(); pd != nil && len(pd.Secrets) > 0 {
		return append(pd.Secrets, secrets...), nil
	}
	return secrets, nil
}

// GetSecret returns a secret by ID (name) within a namespace.
func (s *Store) GetSecret(ctx context.Context, workspace string, id string) (*Secret, error) {
	sec := &Secret{ID: id, Workspace: workspace}
	if err := s.Client.Get(ctx, sec); err != nil {
		return nil, err
	}
	return sec, nil
}

// SaveSecret creates or updates a secret.
// Uniqueness is enforced by the string ID (the secret name) + namespace.
func (s *Store) SaveSecret(ctx context.Context, sec *Secret) error {
	return s.Client.Put(ctx, sec)
}

// DeleteSecret removes a secret by ID (name) within a namespace.
func (s *Store) DeleteSecret(ctx context.Context, workspace string, id string) error {
	sec := &Secret{ID: id, Workspace: workspace}
	return s.Client.Delete(ctx, sec)
}

// MaskRecentSecrets searches recent ChatMessages and History in a workspace and replaces occurrences of 'value' with 'mask'.
// This ensures that plain-text secrets dynamically generated or pasted into the recent chat are not kept in history.
func (s *Store) MaskRecentSecrets(ctx context.Context, workspace, value, mask string) error {
	if len(value) < 8 {
		return nil // Safety: avoid masking short generic strings and corrupting data
	}

	// 1. Mask in recent chat messages
	qMsg := dsorm.NewQuery("ChatMessage").Namespace(workspace).Order("-created").Limit(50)
	msgs, _, err := dsorm.Query[*ChatMessage](ctx, s.Client, qMsg, "")
	if err == nil {
		for _, m := range msgs {
			if strings.Contains(m.Content, value) {
				m.Content = strings.ReplaceAll(m.Content, value, mask)
				_ = s.Client.Put(ctx, m)
			}
		}
	}

	// 2. Mask in recent execution history
	qHist := dsorm.NewQuery("History").Namespace(workspace).Order("-created").Limit(50)
	hists, _, err := dsorm.Query[*History](ctx, s.Client, qHist, "")
	if err == nil {
		for _, h := range hists {
			changed := false
			if strings.Contains(h.Code, value) {
				h.Code = strings.ReplaceAll(h.Code, value, mask)
				changed = true
			}
			if strings.Contains(h.Result, value) {
				h.Result = strings.ReplaceAll(h.Result, value, mask)
				changed = true
			}
			if strings.Contains(h.Response, value) {
				h.Response = strings.ReplaceAll(h.Response, value, mask)
				changed = true
			}
			if changed {
				_ = s.Client.Put(ctx, h)
			}
		}
	}

	return nil
}
