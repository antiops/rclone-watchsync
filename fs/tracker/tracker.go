// Package tracker implements tracking two file systems
package tracker

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/elliotchance/orderedmap"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
	"github.com/rclone/rclone/fs/walk"
)

// Tracker tracks the differences between two filesystems, provides a list of
// steps to make the two filesystems identical. All public methods on this struct
// are thread-safe
type Tracker struct {
	ctx          context.Context
	fsrc         fs.Fs
	fdst         fs.Fs
	records      map[string]record // guarded by recordsLock
	recordsLock  sync.RWMutex
	queue        *orderedmap.OrderedMap // guarded by queueLock
	wakeUp       chan struct{}          // guarded by queueLock
	queueLock    sync.RWMutex
	delay        time.Duration
	nextTaskLock sync.Mutex
}

// NewTracker creates a new tracker
func NewTracker(ctx context.Context, fsrc fs.Fs, fdst fs.Fs, delay time.Duration) *Tracker {
	return &Tracker{
		ctx:     ctx,
		fsrc:    fsrc,
		fdst:    fdst,
		records: make(map[string]record),
		queue:   orderedmap.NewOrderedMap(),
		delay:   delay,
		wakeUp:  make(chan struct{}, 1),
	}
}

type record struct {
	src    fs.Object
	dst    fs.Object
	status Status
}

// Status is used to determine the action that must be completed
type Status int

// Possible values of Status. NonInit indicates that the record is not initialized 
// (probably for error checking later)
const (
	NonInit Status = iota 
	MissingSrc
	MissingDst
	Match
	Diff
)

var statusStr = [...]string{
	"nonInit",
	"missingSrc",
	"missingDst",
	"match",
	"diff",
}

func (s Status) String() string {
	return statusStr[s]
}

// Scan populates the tracker with every object under dir. An optional waitgroup
// can be supplied for the caller to be notified that the scanning is finished. 
func (t *Tracker) Scan(ftar fs.Fs, dir string, wg *sync.WaitGroup) {
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()

	walk.Walk(t.ctx, ftar, dir, false, -1, func(remote string, entries fs.DirEntries, err error) error {
		for _, entry := range entries {
			switch entry := entry.(type) {
			case fs.Object:
				t.update(ftar, entry.Remote(), entry, false)
			}
		}

		return nil
	})
}

// Cleanup checks if every object under the directory still exists
func (t *Tracker) Cleanup(ftar fs.Fs, dir string) {
	toBeRecheck := []string{}
	t.recordsLock.RLock()
	for remote := range t.records {
		if isChild(dir, remote) {
			toBeRecheck = append(toBeRecheck, remote)
		}
	}
	t.recordsLock.RUnlock()

	for _, remote := range toBeRecheck {
		t.update(ftar, remote, nil, true)
	}
}

// isChild returns if remote is a file under dir
func isChild(dir, remote string) bool {
	_, err := filepath.Rel(dir, remote)
	return err == nil
}

// findObject returns the object or nil if it doesn't exist
func findObject(ctx context.Context, ftar fs.Fs, remote string) (fs.Object, error) {
	obj, err := ftar.NewObject(ctx, remote)
	if err == nil {
		return obj, nil
	} else if err == fs.ErrorObjectNotFound {
		return nil, nil
	} else {
		return nil, err
	}
}

// UpdateSingle updates a single object
func (t *Tracker) UpdateSingle(ftar fs.Fs, remote string, obj fs.Object) {
	t.update(ftar, remote, obj, false)
}

// NextTask waits and returns the next task to process for workers
func (t *Tracker) NextTask() (remote string, src fs.Object, dst fs.Object, status Status) {
	// TODO need to check for valid task (not necessary for non folder structure?)
	defer t.nextTaskLock.Unlock()
	t.nextTaskLock.Lock()

	for {
		t.queueLock.Lock()
		el := t.queue.Front()

		if el == nil {
			fs.Debugf(t, "Nothing to do")
			t.queueLock.Unlock()
			<-t.wakeUp
			continue
		}
		eventTime := el.Value.(time.Time)
		elasped := time.Now().Sub(eventTime)
		if elasped < t.delay {
			fs.Debugf(t, "Wating for delay %2f", (t.delay - elasped).Seconds())
			t.queueLock.Unlock()
			time.Sleep(t.delay - elasped)
			continue
		}
		t.queue.Delete(el.Key)
		t.queueLock.Unlock()

		remote := el.Key.(string)
		t.recordsLock.RLock()
		record, ok := t.records[remote]
		t.recordsLock.RUnlock()

		if !ok || record.status == Match {
			continue
		}
		return remote, record.src, record.dst, record.status
	}
}

// FinishTask is used to notify the tracker after completing (or failing) a task
func (t *Tracker) FinishTask(f fs.Fs, remote string, newObj fs.Object, err error) {
	if err != nil {
		fs.Errorf(t, "Error when processing %v: %v", remote, err)
		t.update(t.fsrc, remote, nil, true)
		t.update(t.fdst, remote, nil, true)
	} else {
		fs.Debugf(t, "Finished processing %v", remote)
		t.update(f, remote, newObj, false)
	}
}

// update updates the tracker with the object, determine the status of its records
// and put it in the task queue if necessary
func (t *Tracker) update(ftar fs.Fs, remote string, obj fs.Object, recheck bool) {
	if recheck {
		var err error
		obj, err = findObject(t.ctx, ftar, remote)
		if err != nil {
			fs.Errorf(t, "Error when trying to find object %v on %v: %v", remote, ftar, err)
			return
		}
	}

	t.recordsLock.Lock()
	record := t.records[remote]
	srcSide := ftar == t.fsrc
	if srcSide {
		record.src = obj
	} else {
		record.dst = obj
	}

	if record.src == nil && record.dst == nil {
		record.status = Match
		delete(t.records, remote)
	} else {
		if record.src == nil {
			record.status = MissingSrc
		} else if record.dst == nil {
			record.status = MissingDst
		} else if operations.Equal(t.ctx, record.src, record.dst) {
			record.status = Match
		} else {
			record.status = Diff
		}
		t.records[remote] = record
	}

	t.recordsLock.Unlock()

	fs.Debugf(t, "Updated record %v %v", remote, record.status)

	t.queueLock.Lock()
	t.queue.Delete(remote)
	if record.status != Match {
		t.queue.Set(remote, time.Now())

	}

	if t.queue.Len() > 0 {
		// eat the old signal if there is any
		if len(t.wakeUp) == 1 {
			<-t.wakeUp
		}
		t.wakeUp <- struct{}{}
	}
	t.queueLock.Unlock()
}

func (t *Tracker) String() string {
	return "Tracker"
}
