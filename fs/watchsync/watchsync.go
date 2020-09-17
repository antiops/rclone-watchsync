// Package watchsync implements continuously watching and directory and sync any
// changes to the destination
package watchsync

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/knthmn/rclone-watchsync/fs/tracker"
	"github.com/knthmn/rclone-watchsync/fs/watcher"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
)

// WatchSync (continuously watch and sync) fsrc into fdst
func WatchSync(ctx context.Context, fdst, fsrc fs.Fs, delay int) error {
	tracker := tracker.NewTracker(ctx, fsrc, fdst, time.Duration(delay)*time.Second)
	watcher, err := watcher.NewWatcher(ctx, fsrc, tracker)
	if err != nil {
		fs.Errorf("Watchsync", "Error creating watcher on %v: %v", fsrc, err)
		return err
	}

	ws := &watchSync{
		fdst:      fdst,
		fsrc:      fsrc,
		ctx:       ctx,
		maxActive: fs.Config.Transfers,
		tracker:   tracker,
		watcher:   watcher,
	}

	return ws.run()
}

type watchSync struct {
	ctx       context.Context
	fdst      fs.Fs
	fsrc      fs.Fs
	maxActive int
	tracker   *tracker.Tracker
	watcher   *watcher.Watcher
}

func (ws *watchSync) run() error {
	fs.Infof(ws, "Starting WatchSync")

	go ws.watcher.Run()

	// Currently there is no handling of directories, empty directories on
	// destination must be removed for things to work
	fs.Debugf(ws, "Removing empty directories on destination")
	operations.Rmdirs(ws.ctx, ws.fdst, "", true)

	fs.Debugf(ws, "Starting Initial Scan")
	var wg sync.WaitGroup
	wg.Add(2)
	go ws.tracker.Scan(ws.fsrc, "", &wg)
	go ws.tracker.Scan(ws.fdst, "", &wg)
	wg.Wait()

	fs.Debugf(ws, "Starting workers")
	for i := 0; i < ws.maxActive; i++ {
		go ws.startWorker()
	}

	<-make(chan struct{})

	return nil
}

func (ws *watchSync) startWorker() {
	for {
		remote, src, dst, status := ws.tracker.NextTask()
		fs.Debugf(ws, "Processing %v %v", remote, status)

		switch status {
		case tracker.MissingSrc:
			err := operations.DeleteFile(ws.ctx, dst)
			if err == nil && ws.fdst.Features().CanHaveEmptyDirectories {
				dir := filepath.Dir(remote)
				if dir != "" {
					err = operations.TryRmdir(ws.ctx, ws.fdst, dir)
				}
			}
			ws.tracker.FinishTask(ws.fdst, remote, nil, err)
		case tracker.Diff, tracker.MissingDst:
			newDst, err := operations.Copy(ws.ctx, ws.fdst, nil, remote, src)
			// TODO newDst can be nil?
			ws.tracker.FinishTask(ws.fdst, remote, newDst, err)
		default:
			fs.Errorf("%v has invalid %v", remote, status)
		}
	}
}

func (ws *watchSync) String() string {
	return "WatchSync"
}
