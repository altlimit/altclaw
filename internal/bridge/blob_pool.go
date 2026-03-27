package bridge

import (
	"log/slog"
	"sync"
	"time"

	"gocloud.dev/blob"
)

// blobPoolEntry holds a bucket handle and its last-access time.
type blobPoolEntry struct {
	bucket     *blob.Bucket
	lastAccess time.Time
}

// BlobPool manages a set of *blob.Bucket connections keyed by "driver:bucket".
// Idle connections are automatically closed after idleTimeout.
type BlobPool struct {
	mu          sync.Mutex
	conns       map[string]*blobPoolEntry
	idleTimeout time.Duration
	stopReaper  chan struct{}
}

// NewBlobPool creates a new blob connection pool with background idle reaper.
func NewBlobPool() *BlobPool {
	p := &BlobPool{
		conns:       make(map[string]*blobPoolEntry),
		idleTimeout: 5 * time.Minute,
		stopReaper:  make(chan struct{}),
	}
	go p.reapLoop()
	return p
}

// Put stores a bucket handle in the pool under the given key.
// If a bucket already exists for the key, the old one is closed and replaced.
func (p *BlobPool) Put(key string, bucket *blob.Bucket) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.conns[key]; ok {
		old.bucket.Close()
	}
	p.conns[key] = &blobPoolEntry{bucket: bucket, lastAccess: time.Now()}
	slog.Info("blob pool: opened connection", "key", key)
}

// Get returns an existing bucket for the key, or nil if not found.
// Updates the last-access time on hit.
func (p *BlobPool) Get(key string) *blob.Bucket {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.conns[key]; ok {
		entry.lastAccess = time.Now()
		return entry.bucket
	}
	return nil
}

// Close force-closes a single bucket by key.
func (p *BlobPool) Close(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.conns[key]; ok {
		entry.bucket.Close()
		delete(p.conns, key)
		slog.Info("blob pool: closed connection", "key", key)
	}
}

// CloseAll closes all bucket connections and stops the reaper goroutine.
func (p *BlobPool) CloseAll() {
	close(p.stopReaper)
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, entry := range p.conns {
		entry.bucket.Close()
		slog.Info("blob pool: closed connection", "key", key)
	}
	p.conns = make(map[string]*blobPoolEntry)
}

// List returns the keys of all active bucket connections.
func (p *BlobPool) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.conns))
	for k := range p.conns {
		keys = append(keys, k)
	}
	return keys
}

// reapLoop periodically evicts idle connections.
func (p *BlobPool) reapLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopReaper:
			return
		case <-ticker.C:
			p.reap()
		}
	}
}

func (p *BlobPool) reap() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, entry := range p.conns {
		if now.Sub(entry.lastAccess) > p.idleTimeout {
			entry.bucket.Close()
			delete(p.conns, key)
			slog.Info("blob pool: evicted idle connection", "key", key)
		}
	}
}
