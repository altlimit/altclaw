// Package cron provides a persistent task scheduler for the AI agent.
// Supports cron expressions (recurring), duration delays, and datetime one-shots.
// Persistence is handled by dsorm via config.Store.
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"altclaw.ai/internal/config"
	"github.com/robfig/cron/v3"
)

// dateFormats lists the accepted date/time formats for one-shot scheduling.
var dateFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseDateTime tries to parse a date/time string in various formats.
func parseDateTime(s string) (time.Time, bool) {
	for _, f := range dateFormats {
		if t, err := time.ParseInLocation(f, s, time.Now().Location()); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// JobInfo is the view returned by List().
type JobInfo struct {
	ID           int64  `json:"id"`
	ChatID       int64  `json:"chat_id,omitempty"`
	Schedule     string `json:"schedule"`
	Instructions string `json:"instructions"`
	OneShot      bool   `json:"one_shot"`
	Script       bool   `json:"script"`
	CreatedAt    string `json:"created_at"`
	NextRun      string `json:"next_run,omitempty"`
}

// Runner is called when a job fires.
// jobID: the cron job's ID (for updating state like chatID).
// chatID: the chat session that created this job (0 if none).
// instructions: the task text or JS code.
// isScript: true if the job should be run as JS directly.
type Runner func(jobID, chatID int64, instructions string, isScript bool)

// Manager handles scheduling, persistence, and execution of cron jobs.
type Manager struct {
	mu          sync.RWMutex
	store       *config.Store
	workspaceID string
	entries     map[int64]*jobEntry // keyed by CronJob.ID
	runner      Runner
	cron        *cron.Cron
	stopOnce    sync.Once
}

type jobEntry struct {
	config.CronJob
	cronID cron.EntryID // 0 for one-shot (uses time.AfterFunc)
	timer  *time.Timer  // non-nil for one-shot
}

// New creates a new Manager, loading existing jobs from dsorm and scheduling them.
func New(store *config.Store, workspaceID string, runner Runner) (*Manager, error) {
	m := &Manager{
		store:       store,
		workspaceID: workspaceID,
		entries:     make(map[int64]*jobEntry),
		runner:      runner,
		cron: cron.New(cron.WithParser(cron.NewParser(
			cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
		))),
	}
	ctx := context.Background()
	// Load existing jobs from dsorm
	jobs, err := store.ListCronJobs(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("cron: load: %w", err)
	}

	for _, job := range jobs {
		entry := &jobEntry{CronJob: *job}
		m.entries[job.ID] = entry
		if err := m.schedule(ctx, entry); err != nil {
			slog.Error("failed to schedule cron job", "id", job.ID, "err", err)
		}
	}

	m.cron.Start()
	return m, nil
}

// Add creates a new scheduled job scoped to a chat.
func (m *Manager) Add(ctx context.Context, chatID int64, schedule, instructions string, script bool) (int64, error) {
	now := time.Now()

	oneShot := false
	fireAt := ""

	// 1) Try duration ("30s", "5m", "1h30m")
	if d, err := time.ParseDuration(schedule); err == nil {
		oneShot = true
		fireAt = now.Add(d).Format(time.RFC3339)
	} else if t, ok := parseDateTime(schedule); ok {
		// 2) Try absolute date/time (past dates fire immediately as missed jobs)
		oneShot = true
		fireAt = t.Format(time.RFC3339)
	}
	// 3) Otherwise treat as cron expression (validated in schedule())

	job := &config.CronJob{
		Workspace:    m.workspaceID,
		ChatID:       chatID,
		Schedule:     schedule,
		Instructions: instructions,
		OneShot:      oneShot,
		Script:       script,
		FireAt:       fireAt,
	}

	// Save to dsorm
	if err := m.store.SaveCronJob(ctx, job); err != nil {
		return 0, fmt.Errorf("cron: save: %w", err)
	}

	entry := &jobEntry{CronJob: *job}

	m.mu.Lock()
	m.entries[job.ID] = entry
	m.mu.Unlock()

	if err := m.schedule(ctx, entry); err != nil {
		m.mu.Lock()
		delete(m.entries, job.ID)
		m.mu.Unlock()
		_ = m.store.DeleteCronJob(ctx, job)
		return 0, fmt.Errorf("invalid schedule %q: %w", schedule, err)
	}

	return job.ID, nil
}

// Remove deletes a scheduled job.
func (m *Manager) Remove(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[id]
	if !ok {
		return fmt.Errorf("cron: job %d not found", id)
	}

	if entry.cronID != 0 {
		m.cron.Remove(entry.cronID)
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}

	delete(m.entries, id)
	return m.store.DeleteCronJob(ctx, &entry.CronJob)
}

// List returns all active jobs.
func (m *Manager) List() []JobInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]JobInfo, 0, len(m.entries))
	cronEntries := m.cron.Entries()

	for _, entry := range m.entries {
		info := JobInfo{
			ID:           entry.ID,
			ChatID:       entry.ChatID,
			Schedule:     entry.Schedule,
			Instructions: entry.Instructions,
			OneShot:      entry.OneShot,
			Script:       entry.Script,
			CreatedAt:    entry.CreatedAt.Format(time.RFC3339),
		}

		// Find next run for cron jobs
		for _, e := range cronEntries {
			if e.ID == entry.cronID {
				info.NextRun = e.Next.Format(time.RFC3339)
				break
			}
		}

		// For one-shot, use FireAt
		if entry.OneShot && info.NextRun == "" && entry.FireAt != "" {
			info.NextRun = entry.FireAt
		}

		result = append(result, info)
	}
	return result
}

// Stop gracefully stops the scheduler.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		ctx := m.cron.Stop()
		<-ctx.Done()

		m.mu.Lock()
		for _, entry := range m.entries {
			if entry.timer != nil {
				entry.timer.Stop()
			}
		}
		m.mu.Unlock()
	})
}

// schedule adds a job to the cron scheduler or sets up a one-shot timer.
func (m *Manager) schedule(ctx context.Context, entry *jobEntry) error {
	if entry.OneShot {
		if entry.FireAt == "" {
			return fmt.Errorf("one-shot job missing fire_at")
		}
		fireTime, err := time.Parse(time.RFC3339, entry.FireAt)
		if err != nil {
			return err
		}

		remaining := time.Until(fireTime)
		if remaining <= 0 {
			// Already expired — fire immediately and remove
			go m.fireAndRemove(ctx, entry.ID, entry.ChatID, entry.Instructions, entry.Script)
			return nil
		}

		entry.timer = time.AfterFunc(remaining, func() {
			m.fireAndRemove(ctx, entry.ID, entry.ChatID, entry.Instructions, entry.Script)
		})
		return nil
	}

	// Recurring cron expression
	isScript := entry.Script
	id, err := m.cron.AddFunc(entry.Schedule, func() {
		if m.runner != nil {
			// Read chatID from entry at fire time so ClearChatID updates are visible
			m.mu.RLock()
			chatID := entry.ChatID
			m.mu.RUnlock()
			m.runner(entry.ID, chatID, entry.Instructions, isScript)
		}
	})
	if err != nil {
		return err
	}
	entry.cronID = id
	return nil
}

// fireAndRemove fires a one-shot job and removes it from storage.
func (m *Manager) fireAndRemove(ctx context.Context, id, chatID int64, instructions string, isScript bool) {
	if m.runner != nil {
		m.runner(id, chatID, instructions, isScript)
	}

	m.mu.Lock()
	entry, ok := m.entries[id]
	if ok {
		delete(m.entries, id)
	}
	m.mu.Unlock()

	if ok {
		_ = m.store.DeleteCronJob(ctx, &entry.CronJob)
	}
}

// ClearChatID clears chatID from all in-memory job entries for a given chat.
// Called when a chat is deleted so recurring jobs fire with chatID=0.
func (m *Manager) ClearChatID(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.entries {
		if entry.ChatID == chatID {
			entry.ChatID = 0
		}
	}
}

// UpdateJobChatID updates the chatID for a specific cron job (in-memory + DB).
// Used by lazy chat creation: when a cron script calls agent.run() and its
// original chat was deleted, a new chat is created and the job is re-associated.
func (m *Manager) UpdateJobChatID(jobID, newChatID int64) {
	m.mu.Lock()
	entry, ok := m.entries[jobID]
	if ok {
		entry.ChatID = newChatID
	}
	m.mu.Unlock()

	if ok {
		entry.CronJob.ChatID = newChatID
		_ = m.store.SaveCronJob(context.Background(), &entry.CronJob)
	}
}

// IDStr returns the string form of a job ID (for bridge compatibility).
func IDStr(id int64) string {
	return strconv.FormatInt(id, 10)
}

// ParseID parses a string job ID back to int64.
func ParseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
