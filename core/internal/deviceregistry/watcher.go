package deviceregistry

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// StartLeaseWatcher launches a goroutine that watches the lease file
// for changes and calls RefreshFromLeases. Also runs a periodic save
// of dirty in-memory state to the store file.
//
// Watching the parent directory (not the file directly) handles the
// case where dnsmasq atomically rewrites the file via rename — a
// pure-file watcher would lose the inode and stop firing.
//
// The goroutine returns when ctx is cancelled.
func (r *Registry) StartLeaseWatcher(ctx context.Context) error {
	if r.leaseFile == "" {
		// No lease file configured (dev mode, tests). Still kick off
		// the periodic flush so user-set names are saved.
		go r.runFlusher(ctx)
		return nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.leaseFile)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return err
	}

	// Initial pass so we don't wait for the first event.
	if err := r.RefreshFromLeases(); err != nil {
		r.logger.Printf("deviceregistry: initial lease refresh: %v", err)
	}

	go func() {
		defer func() { _ = w.Close() }()
		// Debounce: dnsmasq can fire several events in a row when it
		// rewrites the file. Coalesce within 500ms.
		var debounce *time.Timer
		fire := func() {
			if err := r.RefreshFromLeases(); err != nil {
				r.logger.Printf("deviceregistry: refresh: %v", err)
			}
		}
		for {
			select {
			case <-ctx.Done():
				if debounce != nil {
					debounce.Stop()
				}
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// We only care about events that touch our specific file.
				if filepath.Clean(ev.Name) != filepath.Clean(r.leaseFile) {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(500*time.Millisecond, fire)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				r.logger.Printf("deviceregistry: watcher error: %v", err)
			}
		}
	}()

	go r.runFlusher(ctx)
	return nil
}

// runFlusher periodically saves dirty state to disk so user changes
// (renames, profile assignments) survive a hard reboot. The 30s
// cadence is a compromise between responsiveness and SD-card wear.
func (r *Registry) runFlusher(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final flush on shutdown.
			if err := r.FlushIfDirty(); err != nil {
				r.logger.Printf("deviceregistry: shutdown flush: %v", err)
			}
			return
		case <-ticker.C:
			if err := r.FlushIfDirty(); err != nil {
				r.logger.Printf("deviceregistry: periodic flush: %v", err)
			}
		}
	}
}
