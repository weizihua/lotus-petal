package sectorstorage

import (
	"time"

	"github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"github.com/filecoin-project/lotus/extern/sector-storage/storiface"
	"github.com/filecoin-project/specs-actors/actors/abi"
)

type Task struct {
	SectorID  abi.SectorID
	TaskType  sealtasks.TaskType
	Priority  int
	Start     time.Time
	Index     int
	IndexHeap int
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
