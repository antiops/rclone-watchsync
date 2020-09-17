// Package watcher implements filesystem watching
package watcher

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/knthmn/rclone-watchsync/fs/tracker"
	"github.com/rclone/rclone/fs"
	"github.com/rjeczalik/notify"
	"golang.org/x/sys/unix"
)

// Watcher watches a directory and notifies a tracker to act according to any changes
type Watcher struct {
	ctx     context.Context
	tracker *tracker.Tracker
	f       fs.Fs
	ch      chan notify.EventInfo
}

// NewWatcher creates a watcher for filesystem f that notifies tracker
func NewWatcher(ctx context.Context, f fs.Fs, tracker *tracker.Tracker) (*Watcher, error) {
	if f.Name() != "local" {
		return nil, errors.New("Watcher not supported by file system")
	}

	ch := make(chan notify.EventInfo, 69)
	w := &Watcher{
		ctx:     ctx,
		tracker: tracker,
		f:       f,
		ch:      ch,
	}
	err := notify.Watch(w.f.Root()+"/...", ch,
		notify.Remove|
			notify.Write|
			notify.InMovedFrom|
			notify.InMovedTo|
			notify.InCreate)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Run starts the watcher
func (w *Watcher) Run() {
	fs.Debugf(w, "Started watcher on %v", w.f)

	for eventInfo := range w.ch {
		fs.Debugf(w, "Received notify event %v", eventInfo)
		remote := w.relPath(eventInfo.Path())

		inotifyEvent := eventInfo.Sys().(*unix.InotifyEvent)
		mask := inotifyEvent.Mask
		isDir := mask&unix.IN_ISDIR != 0

		overflow := mask&unix.IN_Q_OVERFLOW != 0
		if overflow {
			fs.Errorf(w, "Notify overflew")
		}

		switch eventInfo.Event() {
		case notify.InCreate, notify.Write:
			if !isDir {
				obj, err := w.f.NewObject(w.ctx, remote)
				if err != nil {
					fs.Errorf(w, "Error creating object from InCreate or Write of %v: %v",
						remote, err)
					continue
				}
				w.tracker.UpdateSingle(w.f, remote, obj)
			}
		case notify.Remove:
			if !isDir {
				w.tracker.UpdateSingle(w.f, remote, nil)
			}
		case notify.InMovedFrom:
			if isDir {
				w.tracker.Cleanup(w.f, remote)
			} else {
				w.tracker.UpdateSingle(w.f, remote, nil)
			}
		case notify.InMovedTo:
			if isDir {
				w.tracker.Scan(w.f, remote, nil)
			} else {
				obj, err := w.f.NewObject(w.ctx, remote)
				if err != nil {
					fs.Logf(w, "Error creating object from InMovedTo of %v: %v", remote, err)
					continue
				}
				w.tracker.UpdateSingle(w.f, remote, obj)
			}
		}
	}
}

// absPath converts a relative path to an absolute path (as on the native filesystem)
func (w *Watcher) absPath(remote string) string {
	return filepath.Join(w.f.Root(), remote)
}

// relPath converts an absolute path to a relative path (i.e. remote)
func (w *Watcher) relPath(absPath string) string {
	remote, err := filepath.Rel(w.f.Root(), absPath)
	if err != nil {
		fs.Errorf("Cannot find relative path for", absPath, "at", w.f.Root())
		panic("cannot find relative path")
	}
	return remote
}

func (w *Watcher) String() string {
	return "watcher"
}
