package sectorstorage

import (
	"context"
	"time"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"github.com/filecoin-project/lotus/extern/sector-storage/storiface"
)

type Task struct {
	SectorID  abi.SectorID
	TaskType  sealtasks.TaskType
	Priority  int
	Start     time.Time
	Index     int
	IndexHeap int
}

type Todo struct {
	SectorID  abi.SectorID
	TaskType  sealtasks.TaskType
	Priority  int
	Start     time.Time
	Index     int
	IndexHeap int
}

type LoadInfo struct {
	MaxLoad  uint64
	CurrLoad uint64
}

func (m *Manager) WorkerStats() map[uint64]storiface.WorkerStats {
	m.sched.workersLk.RLock()
	defer m.sched.workersLk.RUnlock()

	out := map[uint64]storiface.WorkerStats{}

	for id, handle := range m.sched.workers {
		out[uint64(id)] = storiface.WorkerStats{
			Info:       handle.info,
			MemUsedMin: handle.active.memUsedMin,
			MemUsedMax: handle.active.memUsedMax,
			GpuUsed:    handle.active.gpuUsed,
			CpuUse:     handle.active.cpuUse,
		}
	}

	return out
}

func (m *Manager) WorkerJobs() map[uint64][]storiface.WorkerJob {
	m.sched.workersLk.RLock()
	defer m.sched.workersLk.RUnlock()

	out := map[uint64][]storiface.WorkerJob{}

	for id, handle := range m.sched.workers {
		out[uint64(id)] = handle.wt.Running()

		handle.wndLk.Lock()
		for wi, window := range handle.activeWindows {
			for _, request := range window.todo {
				out[uint64(id)] = append(out[uint64(id)], storiface.WorkerJob{
					ID:      0,
					Sector:  request.sector,
					Task:    request.taskType,
					RunWait: wi + 1,
					Start:   request.start,
				})
			}
		}
		handle.wndLk.Unlock()
	}

	return out
}

func (m *Manager) SchedQueue() []Task {
	var tasks = make([]Task, 0)

	m.sched.workersLk.RLock()
	defer m.sched.workersLk.RUnlock()

	if m.sched.schedQueue != nil {
		for _, r := range *m.sched.schedQueue {
			tasks = append(tasks, Task{
				SectorID:  r.sector,
				TaskType:  r.taskType,
				Priority:  r.priority,
				Start:     r.start,
				Index:     r.index,
				IndexHeap: r.indexHeap,
			})
		}
	}

	return tasks
}

func (m *Manager) WorkerTodos() map[WorkerID][]Todo {
	var workersTodo = make(map[WorkerID][]Todo, 0)
	for wid, worker := range m.sched.workers {
		if worker == nil {
			log.Warnf("worker %d got nil", wid)
			continue
		}

		var todos = make([]Todo, 0)
		for _, wnd := range worker.activeWindows {
			for _, todo := range wnd.todo {
				if todo == nil {
					continue
				}

				todos = append(todos, Todo{
					SectorID:  todo.sector,
					TaskType:  todo.taskType,
					Priority:  todo.priority,
					Start:     todo.start,
					Index:     todo.index,
					IndexHeap: todo.indexHeap,
				})
			}
		}

		workersTodo[wid] = todos
	}

	return workersTodo
}

func (m *Manager) WorkerLoad() map[WorkerID]LoadInfo {
	var out = make(map[WorkerID]LoadInfo)
	for wid, whandle := range m.sched.workers {
		max := whandle.w.MaxParallelSealingSector(context.Background())
		curr := uint64(len(whandle.wt.Running()))

		out[wid] = LoadInfo{
			MaxLoad:  max,
			CurrLoad: curr,
		}
	}

	return out
}

func (m *Manager) WorkerTaskTypes() map[WorkerID][]string {
	var out = make(map[WorkerID][]string)
	for wid, whandel := range m.sched.workers {

		tt, err := whandel.w.TaskTypes(context.Background())
		if err != nil {
			log.Warn("get worker %d tasktypes failed: %s")
			continue
		}

		types := make([]string, 0)
		for typ, _ := range tt {
			types = append(types, typ.Short())
		}

		out[wid] = types
	}

	return out
}
