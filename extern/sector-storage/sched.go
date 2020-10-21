package sectorstorage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-state-types/abi"

	"github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"github.com/filecoin-project/lotus/extern/sector-storage/storiface"
)

type schedPrioCtxKey int

var SchedPriorityKey schedPrioCtxKey
var DefaultSchedPriority = 0
var SelectorTimeout = 5 * time.Second
var InitWait = 3 * time.Second

var (
	SchedWindows = 1
)

func getPriority(ctx context.Context) int {
	sp := ctx.Value(SchedPriorityKey)
	if p, ok := sp.(int); ok {
		return p
	}

	return DefaultSchedPriority
}

func WithPriority(ctx context.Context, priority int) context.Context {
	return context.WithValue(ctx, SchedPriorityKey, priority)
}

const mib = 1 << 20

type WorkerAction func(ctx context.Context, w Worker) error

type WorkerSelector interface {
	Ok(ctx context.Context, task sealtasks.TaskType, spt abi.RegisteredSealProof, a *workerHandle) (bool, error) // true if worker is acceptable for performing a task

	Cmp(ctx context.Context, task sealtasks.TaskType, a, b *workerHandle) (bool, error) // true if a is preferred over b
}

type scheduler struct {
	spt abi.RegisteredSealProof

	workersLk  sync.RWMutex
	nextWorker WorkerID
	workers    map[WorkerID]*workerHandle

	newWorkers chan *workerHandle

	watchClosing  chan WorkerID
	workerClosing chan WorkerID

	schedule       chan *workerRequest
	windowRequests chan *schedWindowRequest

	// owned by the sh.runSched goroutine
	schedQueue  *requestQueue
	openWindows []*schedWindowRequest

	info chan func(interface{})

	closing  chan struct{}
	closed   chan struct{}
	testSync chan struct{} // used for testing

	matches    map[string][]matchInfo
	forceMatch bool
}

type matchInfo struct {
	id     WorkerID
	handle *workerHandle
}

type workerHandle struct {
	w Worker

	info storiface.WorkerInfo

	preparing *activeResources
	active    *activeResources

	lk sync.Mutex

	wndLk         sync.Mutex
	activeWindows []*schedWindow

	// stats / tracking
	wt *workTracker

	// for sync manager goroutine closing
	cleanupStarted bool
	closedMgr      chan struct{}
	closingMgr     chan struct{}
}

type schedWindowRequest struct {
	worker WorkerID

	done chan *schedWindow
}

type schedWindow struct {
	allocated activeResources
	todo      []*workerRequest
}

type activeResources struct {
	memUsedMin uint64
	memUsedMax uint64
	gpuUsed    bool
	cpuUse     uint64

	cond *sync.Cond
}

type workerRequest struct {
	sector   abi.SectorID
	taskType sealtasks.TaskType
	priority int // larger values more important
	sel      WorkerSelector

	prepare WorkerAction
	work    WorkerAction

	start time.Time

	index int // The index of the item in the heap.

	indexHeap int
	ret       chan<- workerResponse
	ctx       context.Context
}

type workerResponse struct {
	err error
}

func newScheduler(spt abi.RegisteredSealProof, forceMatch bool) *scheduler {
	return &scheduler{
		spt: spt,

		nextWorker: 0,
		workers:    map[WorkerID]*workerHandle{},

		newWorkers: make(chan *workerHandle),

		watchClosing:  make(chan WorkerID),
		workerClosing: make(chan WorkerID),

		schedule:       make(chan *workerRequest),
		windowRequests: make(chan *schedWindowRequest, 20),

		schedQueue: &requestQueue{},

		info: make(chan func(interface{})),

		closing: make(chan struct{}),
		closed:  make(chan struct{}),

		matches: map[string][]matchInfo{
			"undefined": {},
		},

		forceMatch: forceMatch,
	}
}

func (sh *scheduler) Schedule(ctx context.Context, sector abi.SectorID, taskType sealtasks.TaskType, sel WorkerSelector, prepare WorkerAction, work WorkerAction) error {
	ret := make(chan workerResponse)

	select {
	case sh.schedule <- &workerRequest{
		sector:   sector,
		taskType: taskType,
		priority: getPriority(ctx),
		sel:      sel,

		prepare: prepare,
		work:    work,

		start: time.Now(),

		ret: ret,
		ctx: ctx,
	}:
	case <-sh.closing:
		return xerrors.New("closing")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case resp := <-ret:
		return resp.err
	case <-sh.closing:
		return xerrors.New("closing")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *workerRequest) respond(err error) {
	select {
	case r.ret <- workerResponse{err: err}:
	case <-r.ctx.Done():
		log.Warnf("request got cancelled before we could respond")
	}
}

type SchedDiagRequestInfo struct {
	Sector   abi.SectorID
	TaskType sealtasks.TaskType
	Priority int
}

type SchedDiagInfo struct {
	Requests    []SchedDiagRequestInfo
	OpenWindows []WorkerID
}

func (sh *scheduler) runSched() {
	defer close(sh.closed)

	go sh.runWorkerWatcher()

	iw := time.After(InitWait)
	var initialised bool

	timer := time.NewTimer(1 * time.Minute)

	for {
		var doSched bool

		select {
		case w := <-sh.newWorkers:
			sh.newWorker(w)

		case wid := <-sh.workerClosing:
			sh.dropWorker(wid)

		case req := <-sh.schedule:
			sh.schedQueue.Push(req)
			doSched = true

			if sh.testSync != nil {
				sh.testSync <- struct{}{}
			}
		case req := <-sh.windowRequests:
			sh.openWindows = append(sh.openWindows, req)
			doSched = true
		case ireq := <-sh.info:
			ireq(sh.diag())

		case <-iw:
			initialised = true
			iw = nil
			doSched = true
		case <-sh.closing:
			sh.schedClose()
			return
		case <-timer.C:
			doSched = true
		}

		if doSched && initialised {
			// First gather any pending tasks, so we go through the scheduling loop
			// once for every added task
		loop:
			for {
				select {
				case req := <-sh.schedule:
					sh.schedQueue.Push(req)
					if sh.testSync != nil {
						sh.testSync <- struct{}{}
					}
				case req := <-sh.windowRequests:
					sh.openWindows = append(sh.openWindows, req)
				default:
					break loop
				}
			}

			sh.trySched()
			timer.Reset(1 * time.Minute)
		}

	}
}

func (sh *scheduler) diag() SchedDiagInfo {
	var out SchedDiagInfo

	for sqi := 0; sqi < sh.schedQueue.Len(); sqi++ {
		task := (*sh.schedQueue)[sqi]

		out.Requests = append(out.Requests, SchedDiagRequestInfo{
			Sector:   task.sector,
			TaskType: task.taskType,
			Priority: task.priority,
		})
	}

	for _, window := range sh.openWindows {
		out.OpenWindows = append(out.OpenWindows, window.worker)
	}

	return out
}

func (sh *scheduler) trySched() {
	log.Debugf("start sched")
	st := time.Now()

	if sh.schedQueue.Len() == 0 || len(sh.openWindows) == 0 {
		return
	}

	sh.workersLk.RLock()
	defer sh.workersLk.RUnlock()

	var schedWorkers = make(map[WorkerID]*schedWindowRequest)

	log.Debugf("openWindows: %+v", sh.openWindows)

	for i, window := range sh.openWindows {
		if window == nil {
			log.Warnf("openWindows[%d] is nil point", i)
			continue
		}

		if _, exist := sh.workers[window.worker]; !exist {
			log.Warnf("worker %d does not in worker map", window.worker)
			continue
		}

		//curActive := 0
		//for _, window := range sh.workers[window.worker].activeWindows {
		//	curActive += len(window.todo)
		//}

		//if !whandle.w.CanHandleMoreTask(context.Background(), uint64(len(whandle.wt.Running())), uint64(curActive)) {
		//	log.Warnf("worker %d can't handle more task", window.worker)
		//	continue
		//}

		schedWorkers[window.worker] = window
	}

	log.Debugf("schedWorkers: %+v", schedWorkers)

	if len(schedWorkers) == 0 {
		return
	}

	for i := 0; i < sh.schedQueue.Len(); i++ {
		task := (*sh.schedQueue)[i]
		matchID := sh.workers[WorkerID(0)].w.FindSectorPreviousSealer(context.Background(), task.sector)
		noMatchTask := false

		if task.taskType == sealtasks.TTAddPiece || task.taskType == sealtasks.TTFetch {
			noMatchTask = true
		}

		if !noMatchTask {
			var sched = false
			for _, match := range sh.matches[matchID] {
				if req, has := schedWorkers[match.id]; has {
					if sh.doSched(task, req, match.id) {
						log.Debug("assign to match worker")
						sh.schedQueue.Remove(i)
						i--
						sched = true
						break
					}
				}
			}
			if sched { continue }
		}

		if sh.forceMatch || noMatchTask {
			for wid, req := range schedWorkers {
				if sh.doSched(task, req, wid) {
					sh.schedQueue.Remove(i)
					i--
					break
				}
			}
		}
	}

	//for wid, window := range schedWorkers {
	//	schedWindow := schedWindow{}
	//	maxParallelSectors := sh.workers[wid].w.MaxParallelSealingSector(context.Background())
	//	running := len(sh.workers[wid].wt.Running())
	//
	//	for i := 0; i < sh.schedQueue.Len(); i++ {
	//		running = len(sh.workers[wid].wt.Running())
	//		task := (*sh.schedQueue)[i]
	//		needRes := ResourceTable[task.taskType][sh.spt]
	//
	//		ok, err := task.sel.Ok(context.Background(), task.taskType, sh.spt, sh.workers[wid])
	//		if err != nil {
	//			log.Warnf("sel.Ok: %s", err)
	//			continue
	//		}
	//		if !ok {
	//			log.Debugf("worker %d: can't handle %d:%s", wid, task.sector.Number, task.taskType)
	//			continue
	//		}
	//
	//		if !schedWindow.allocated.canHandleRequest(needRes, wid, "schedAcceptable", sh.workers[wid].info.Resources) {
	//			log.Debugf("worker %d: not enough resource to handle %d:%s", wid, task.sector.Number, task.taskType)
	//			continue
	//		}
	//
	//		if maxParallelSectors != 0 {
	//			curActive := 0
	//			for _, window := range sh.workers[wid].activeWindows {
	//				curActive += len(window.todo)
	//			}
	//			log.Debugw(fmt.Sprintf("worker %d", wid), "curActive", curActive, "curRunning", running,
	//				"curWindowTodo", len(schedWindow.todo))
	//
	//			if uint64(len(schedWindow.todo)+running+curActive+1) <= maxParallelSectors {
	//				log.Debugf("append task %s sector %d to worker %d", task.taskType, task.sector.Number,
	//					window.worker)
	//				schedWindow.todo = append(schedWindow.todo, task)
	//				schedWindow.allocated.add(sh.workers[wid].info.Resources, needRes)
	//				sh.schedQueue.Remove(i)
	//				i--
	//			} else {
	//				break
	//			}
	//		} else {
	//			log.Debugf("append task %s sector %d to worker %d", task.taskType, task.sector.Number,
	//				window.worker)
	//			schedWindow.todo = append(schedWindow.todo, task)
	//			schedWindow.allocated.add(sh.workers[wid].info.Resources, needRes)
	//			sh.schedQueue.Remove(i)
	//			i--
	//		}
	//	}
	//
	//	if len(schedWindow.todo) > 0 {
	//		log.Infof("assign %d jobs to worker %d", len(schedWindow.todo), wid)
	//		window.done <- &schedWindow
	//	}
	//}

	log.Debugf("sched take: %s", time.Now().Sub(st))
}

func (sh *scheduler) doSched(task *workerRequest, window *schedWindowRequest, wid WorkerID) bool {
	worker := sh.workers[wid]
	var schedWindow = &schedWindow{}
	needRes := ResourceTable[task.taskType][sh.spt]

	ok, err := task.sel.Ok(context.Background(), task.taskType, sh.spt, worker)
	if err != nil || !ok {
		if err != nil {
			log.Warnf("sel.Ok failed: %+v", err)
		}
		return false
	}

	if !schedWindow.allocated.canHandleRequest(needRes, wid, "schedAcceptable", worker.info.Resources) {
		log.Warnf("worker %d: not enough resource to handle %d:%s", wid, task.sector.Number, task.taskType)
		return false
	}

	schedWindow.todo = []*workerRequest{ task }
	schedWindow.allocated.add(worker.info.Resources, needRes)

	if worker.w.MaxParallelSealingSector(context.Background()) == 0 {
		log.Debugf("assign sector %d(%s) to worker %d", task.sector.Number, task.taskType, wid)
		window.done <- schedWindow
		return true
	}

	var curActive int
	for _, w := range worker.activeWindows {
		curActive += len(w.todo)
	}
	if uint64(len(worker.wt.running)+curActive+1) < worker.w.MaxParallelSealingSector(context.Background()) {
		log.Debugf("assign sector %d(%s) to worker %d", task.sector.Number, task.taskType, wid)
		window.done <- schedWindow
		return true
	}

	return false
}

func (sh *scheduler) runWorker(wid WorkerID) {
	var ready sync.WaitGroup
	ready.Add(1)
	defer ready.Wait()

	go func() {
		sh.workersLk.RLock()
		worker, found := sh.workers[wid]
		sh.workersLk.RUnlock()

		ready.Done()

		if !found {
			panic(fmt.Sprintf("worker %d not found", wid))
		}

		defer close(worker.closedMgr)

		scheduledWindows := make(chan *schedWindow, SchedWindows)
		taskDone := make(chan struct{}, 1)
		windowsRequested := 0

		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()

		workerClosing, err := worker.w.Closing(ctx)
		if err != nil {
			return
		}

		defer func() {
			log.Warnw("Worker closing", "workerid", wid)

			// TODO: close / return all queued tasks
		}()

		for ; windowsRequested < SchedWindows; windowsRequested++ {
			sh.windowRequests <- &schedWindowRequest{
				worker: wid,
				done:   scheduledWindows,
			}
			log.Infof("worker %d open new window", wid)
		}

		for {
			for ; windowsRequested < SchedWindows; windowsRequested++ {
			}

			select {
			case w := <-scheduledWindows:
				worker.wndLk.Lock()
				worker.activeWindows = append(worker.activeWindows, w)
				worker.wndLk.Unlock()
			case <-taskDone:
				log.Debugw("task done", "workerid", wid)
			case <-sh.closing:
				return
			case <-workerClosing:
				return
			case <-worker.closingMgr:
				return
			}

			sh.workersLk.RLock()
			worker.wndLk.Lock()

			windowsRequested -= sh.workerCompactWindows(worker, wid)

		assignLoop:
			// process windows in order
			for len(worker.activeWindows) > 0 {
				firstWindow := worker.activeWindows[0]

				// process tasks within a window, preferring tasks at lower indexes
				for len(firstWindow.todo) > 0 {
					tidx := -1

					worker.lk.Lock()
					for t, todo := range firstWindow.todo {
						needRes := ResourceTable[todo.taskType][sh.spt]
						if worker.preparing.canHandleRequest(needRes, wid, "startPreparing", worker.info.Resources) {
							tidx = t
							break
						}
					}
					worker.lk.Unlock()

					if tidx == -1 {
						break assignLoop
					}

					todo := firstWindow.todo[tidx]

					log.Debugf("assign worker sector %d", todo.sector.Number)
					err := sh.assignWorker(taskDone, wid, worker, todo)

					if err != nil {
						log.Error("assignWorker error: %+v", err)
						go todo.respond(xerrors.Errorf("assignWorker error: %w", err))
					}

					// Note: we're not freeing window.allocated resources here very much on purpose
					copy(firstWindow.todo[tidx:], firstWindow.todo[tidx+1:])
					firstWindow.todo[len(firstWindow.todo)-1] = nil
					firstWindow.todo = firstWindow.todo[:len(firstWindow.todo)-1]
				}

				copy(worker.activeWindows, worker.activeWindows[1:])
				worker.activeWindows[len(worker.activeWindows)-1] = nil
				worker.activeWindows = worker.activeWindows[:len(worker.activeWindows)-1]

				windowsRequested--
				log.Debugf("worker %d windowsRequested: %d", wid, windowsRequested)
			}

			worker.wndLk.Unlock()
			sh.workersLk.RUnlock()
		}
	}()
}

func (sh *scheduler) workerCompactWindows(worker *workerHandle, wid WorkerID) int {
	// move tasks from older windows to newer windows if older windows
	// still can fit them
	if len(worker.activeWindows) > 1 {
		for wi, window := range worker.activeWindows[1:] {
			lower := worker.activeWindows[wi]
			var moved []int

			for ti, todo := range window.todo {
				needRes := ResourceTable[todo.taskType][sh.spt]
				if !lower.allocated.canHandleRequest(needRes, wid, "compactWindows", worker.info.Resources) {
					continue
				}

				moved = append(moved, ti)
				lower.todo = append(lower.todo, todo)
				lower.allocated.add(worker.info.Resources, needRes)
				window.allocated.free(worker.info.Resources, needRes)
			}

			if len(moved) > 0 {
				newTodo := make([]*workerRequest, 0, len(window.todo)-len(moved))
				for i, t := range window.todo {
					if len(moved) > 0 && moved[0] == i {
						moved = moved[1:]
						continue
					}

					newTodo = append(newTodo, t)
				}
				window.todo = newTodo
			}
		}
	}

	var compacted int
	var newWindows []*schedWindow

	for _, window := range worker.activeWindows {
		if len(window.todo) == 0 {
			compacted++
			continue
		}

		newWindows = append(newWindows, window)
	}

	worker.activeWindows = newWindows

	return compacted
}

func (sh *scheduler) assignWorker(taskDone chan struct{}, wid WorkerID, w *workerHandle, req *workerRequest) error {
	needRes := ResourceTable[req.taskType][sh.spt]

	w.lk.Lock()
	w.preparing.add(w.info.Resources, needRes)
	w.lk.Unlock()

	go func() {
		err := req.prepare(req.ctx, w.wt.worker(w.w))
		sh.workersLk.Lock()

		if err != nil {
			w.lk.Lock()
			w.preparing.free(w.info.Resources, needRes)
			w.lk.Unlock()
			sh.workersLk.Unlock()

			select {
			case taskDone <- struct{}{}:
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response (prepare error: %+v)", err)
			}

			select {
			case req.ret <- workerResponse{err: err}:
			case <-req.ctx.Done():
				log.Warnf("request got cancelled before we could respond (prepare error: %+v)", err)
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response (prepare error: %+v)", err)
			}
			return
		}

		err = w.active.withResources(wid, w.info.Resources, needRes, &sh.workersLk, func() error {
			w.lk.Lock()
			w.preparing.free(w.info.Resources, needRes)
			w.lk.Unlock()
			sh.workersLk.Unlock()
			defer sh.workersLk.Lock() // we MUST return locked from this function

			select {
			case taskDone <- struct{}{}:
			case <-sh.closing:
			}

			err = req.work(req.ctx, w.wt.worker(w.w))

			select {
			case req.ret <- workerResponse{err: err}:
			case <-req.ctx.Done():
				log.Warnf("request got cancelled before we could respond")
			case <-sh.closing:
				log.Warnf("scheduler closed while sending response")
			}

			return nil
		})

		sh.workersLk.Unlock()

		// This error should always be nil, since nothing is setting it, but just to be safe:
		if err != nil {
			log.Errorf("error executing worker (withResources): %+v", err)
		}
	}()

	return nil
}

func (sh *scheduler) newWorker(w *workerHandle) {
	w.closedMgr = make(chan struct{})
	w.closingMgr = make(chan struct{})

	sh.workersLk.Lock()

	id := sh.nextWorker
	sh.workers[id] = w
	sh.nextWorker++

	sh.workersLk.Unlock()

	sh.runWorker(id)

	var machineID string
	if fakeID, exist := os.LookupEnv("LOTUS_FAKE_MACHINE_ID"); exist {
		machineID = fakeID
	} else {
		info, _ := w.w.Info(context.Background())
		machineID = info.MachineID
		if len(machineID) == 0 {
			machineID = "undefined"
		}
	}

	if _, exist := sh.matches[machineID]; !exist {
		sh.matches[machineID] = []matchInfo{
			{
				id:     id,
				handle: w,
			},
		}
	} else {
		sh.matches[machineID] = append(sh.matches[machineID], matchInfo{
			id:     id,
			handle: w,
		})
	}

	select {
	case sh.watchClosing <- id:
	case <-sh.closing:
		return
	}
}

func (sh *scheduler) dropWorker(wid WorkerID) {
	sh.workersLk.Lock()
	defer sh.workersLk.Unlock()

	w := sh.workers[wid]

	sh.workerCleanup(wid, w)

	delete(sh.workers, wid)
}

func (sh *scheduler) workerCleanup(wid WorkerID, w *workerHandle) {
	select {
	case <-w.closingMgr:
	default:
		close(w.closingMgr)
	}

	select {
	case <-w.closedMgr:
	case <-time.After(time.Second):
		log.Errorf("timeout closing worker manager goroutine %d", wid)
	}

	if !w.cleanupStarted {
		w.cleanupStarted = true

		newWindows := make([]*schedWindowRequest, 0, len(sh.openWindows))
		for _, window := range sh.openWindows {
			if window.worker != wid {
				newWindows = append(newWindows, window)
			}
		}
		sh.openWindows = newWindows

		var action = 0
		var delID string

		for macID, workerHandles := range sh.matches {
			for i, info := range workerHandles {
				if info.id == wid {
					if len(sh.matches[macID]) == 1 {
						action = 1
						delID = macID
					} else {
						sh.matches[macID][i] = sh.matches[macID][0]
						action = 2
						delID = macID
					}
					break
				}
			}
			if action != 0 {
				break
			}
		}

		switch action {
		case 1:
			delete(sh.matches, delID)
		case 2:
			sh.matches[delID] = sh.matches[delID][1:]
		default:
		}

		log.Debugf("dropWorker %d", wid)

		go func() {
			if err := w.w.Close(); err != nil {
				log.Warnf("closing worker %d: %+v", wid, err)
			}
		}()
	}
}

func (sh *scheduler) schedClose() {
	sh.workersLk.Lock()
	defer sh.workersLk.Unlock()
	log.Debugf("closing scheduler")

	for i, w := range sh.workers {
		sh.workerCleanup(i, w)
	}
}

func (sh *scheduler) Info(ctx context.Context) (interface{}, error) {
	ch := make(chan interface{}, 1)

	sh.info <- func(res interface{}) {
		ch <- res
	}

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (sh *scheduler) Close(ctx context.Context) error {
	close(sh.closing)
	select {
	case <-sh.closed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (sh *scheduler) getMatchID(sid abi.SectorID) string {
	return sh.workers[WorkerID(0)].w.FindSectorPreviousSealer(context.Background(), sid)
}
