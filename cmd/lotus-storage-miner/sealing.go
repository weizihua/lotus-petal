package main

import (
	"encoding/json"
	"fmt"
	sectorstorage "github.com/filecoin-project/lotus/extern/sector-storage"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/xerrors"

	"github.com/fatih/color"
	"github.com/urfave/cli/v2"

	"github.com/filecoin-project/lotus/extern/sector-storage/sealtasks"
	"github.com/filecoin-project/lotus/extern/sector-storage/storiface"

	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/modood/table"
)

type taskLoadTable struct {
	AddPiece     uint64
	PreCommit1   uint64
	PreCommit2   uint64
	Commit1      uint64
	Commit2      uint64
	Finalize     uint64
	Fetch        uint64
	Unseal       uint64
	ReadUnsealed uint64
}

type workerTodosTable struct {
	SectorID  uint64
	TaskType  string
	Priority  int
	Start     time.Time
	Index     int
	IndexHeap int

}

var sealingCmd = &cli.Command{
	Name:  "sealing",
	Usage: "interact with sealing pipeline",
	Subcommands: []*cli.Command{
		sealingJobsCmd,
		sealingWorkersCmd,
		sealingSchedDiagCmd,
		sealingWorkerLoad,
		sealingQueueCmd,
		sealingWorkerTodosCmd,
		sealingListIndexSectorsCmd,
		sealingListMatchesCmd,
	},
}

var sealingWorkersCmd = &cli.Command{
	Name:  "workers",
	Usage: "list workers",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "color"},
	},
	Action: func(cctx *cli.Context) error {
		color.NoColor = !cctx.Bool("color")

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := lcli.ReqContext(cctx)

		stats, err := nodeApi.WorkerStats(ctx)
		if err != nil {
			return err
		}

		type sortableStat struct {
			id uint64
			storiface.WorkerStats
		}

		st := make([]sortableStat, 0, len(stats))
		for id, stat := range stats {
			st = append(st, sortableStat{id, stat})
		}

		sort.Slice(st, func(i, j int) bool {
			return st[i].id < st[j].id
		})

		workerTasks := nodeApi.SchedWorkerTaskTypes(ctx)
		workerLoad := nodeApi.SchedWorkerLoad(ctx)

		for _, stat := range st {
			gpuUse := "not "
			gpuCol := color.FgBlue
			if stat.GpuUsed {
				gpuCol = color.FgGreen
				gpuUse = ""
			}

			fmt.Printf("Worker %d, host %s\n", stat.id, color.MagentaString(stat.Info.Hostname))

			var barCols = uint64(64)
			cpuBars := int(stat.CpuUse * barCols / stat.Info.Resources.CPUs)
			cpuBar := strings.Repeat("|", cpuBars) + strings.Repeat(" ", int(barCols)-cpuBars)

			fmt.Printf("\tCPU:  [%s] %d/%d core(s) in use\n",
				color.GreenString(cpuBar), stat.CpuUse, stat.Info.Resources.CPUs)

			ramBarsRes := int(stat.Info.Resources.MemReserved * barCols / stat.Info.Resources.MemPhysical)
			ramBarsUsed := int(stat.MemUsedMin * barCols / stat.Info.Resources.MemPhysical)
			ramBar := color.YellowString(strings.Repeat("|", ramBarsRes)) +
				color.GreenString(strings.Repeat("|", ramBarsUsed)) +
				strings.Repeat(" ", int(barCols)-ramBarsUsed-ramBarsRes)

			vmem := stat.Info.Resources.MemPhysical + stat.Info.Resources.MemSwap

			vmemBarsRes := int(stat.Info.Resources.MemReserved * barCols / vmem)
			vmemBarsUsed := int(stat.MemUsedMax * barCols / vmem)
			vmemBar := color.YellowString(strings.Repeat("|", vmemBarsRes)) +
				color.GreenString(strings.Repeat("|", vmemBarsUsed)) +
				strings.Repeat(" ", int(barCols)-vmemBarsUsed-vmemBarsRes)

			fmt.Printf("\tRAM:  [%s] %d%% %s/%s\n", ramBar,
				(stat.Info.Resources.MemReserved+stat.MemUsedMin)*100/stat.Info.Resources.MemPhysical,
				types.SizeStr(types.NewInt(stat.Info.Resources.MemReserved+stat.MemUsedMin)),
				types.SizeStr(types.NewInt(stat.Info.Resources.MemPhysical)))

			fmt.Printf("\tVMEM: [%s] %d%% %s/%s\n", vmemBar,
				(stat.Info.Resources.MemReserved+stat.MemUsedMax)*100/vmem,
				types.SizeStr(types.NewInt(stat.Info.Resources.MemReserved+stat.MemUsedMax)),
				types.SizeStr(types.NewInt(vmem)))

			for _, gpu := range stat.Info.Resources.GPUs {
				fmt.Printf("\tGPU: %s\n", color.New(gpuCol).Sprintf("%s, %sused", gpu, gpuUse))
			}
			fmt.Printf("\tTaskTypes: %v\n", workerTasks[sectorstorage.WorkerID(stat.id)])
			fmt.Printf("\tMaxParallelSealingSector: %d\n", workerLoad[sectorstorage.WorkerID(stat.id)].MaxLoad)
		}

		return nil
	},
}

var sealingJobsCmd = &cli.Command{
	Name:  "jobs",
	Usage: "list workers",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "color"},
	},
	Action: func(cctx *cli.Context) error {
		color.NoColor = !cctx.Bool("color")

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := lcli.ReqContext(cctx)

		jobs, err := nodeApi.WorkerJobs(ctx)
		if err != nil {
			return xerrors.Errorf("getting worker jobs: %w", err)
		}

		type line struct {
			storiface.WorkerJob
			wid uint64
		}

		lines := make([]line, 0)

		for wid, jobs := range jobs {
			for _, job := range jobs {
				lines = append(lines, line{
					WorkerJob: job,
					wid:       wid,
				})
			}
		}

		// oldest first
		sort.Slice(lines, func(i, j int) bool {
			if lines[i].RunWait != lines[j].RunWait {
				return lines[i].RunWait < lines[j].RunWait
			}
			return lines[i].Start.Before(lines[j].Start)
		})

		workerHostnames := map[uint64]string{}

		wst, err := nodeApi.WorkerStats(ctx)
		if err != nil {
			return xerrors.Errorf("getting worker stats: %w", err)
		}

		for wid, st := range wst {
			workerHostnames[wid] = st.Info.Hostname
		}

		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(tw, "ID\tSector\tWorker\tHostname\tTask\tState\tTime\n")

		for _, l := range lines {
			state := "running"
			if l.RunWait != 0 {
				state = fmt.Sprintf("assigned(%d)", l.RunWait-1)
			}
			_, _ = fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\t%s\t%s\n", l.ID, l.Sector.Number, l.wid, workerHostnames[l.wid], l.Task.Short(), state, time.Now().Sub(l.Start).Truncate(time.Millisecond*100))
		}

		return tw.Flush()
	},
}

var sealingSchedDiagCmd = &cli.Command{
	Name:  "sched-diag",
	Usage: "Dump internal scheduler state",
	Action: func(cctx *cli.Context) error {
		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := lcli.ReqContext(cctx)

		st, err := nodeApi.SealingSchedDiag(ctx)
		if err != nil {
			return err
		}

		j, err := json.MarshalIndent(&st, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(j))

		return nil
	},
}

var sealingWorkerLoad = &cli.Command{
	Name:  "load",
	Usage: "Print workers load",
	Action: func(cctx *cli.Context) error {
		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		jobs, err := nodeApi.WorkerJobs(lcli.ReqContext(cctx))
		if err != nil {
			return err
		}

		hostnames := map[uint64]string{}

		wst, err := nodeApi.WorkerStats(lcli.ReqContext(cctx))
		if err != nil {
			return xerrors.Errorf("getting worker stats: %w", err)
		}

		for wid, st := range wst {
			hostnames[wid] = st.Info.Hostname
		}

		var keys []uint64
		for k := range jobs {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i] < keys[j]
		})

		wload := nodeApi.SchedWorkerLoad(lcli.ReqContext(cctx))

		for _, i := range keys {
			loads := map[sealtasks.TaskType]uint64{}

			for _, j := range jobs[i] {
				loads[j.Task] += 1
			}

			fmt.Printf("worker(%d) %s(%d/%d):\n", i, hostnames[i], wload[sectorstorage.WorkerID(i)].CurrLoad, wload[sectorstorage.WorkerID(i)].MaxLoad)
			loadsTab := []taskLoadTable{
				{
					func() uint64 {if load,ok := loads[sealtasks.TTAddPiece];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTPreCommit1];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTPreCommit2];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTCommit1];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTCommit2];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTFinalize];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTFetch];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTUnseal];ok{return load}else{return 0}}(),
					func() uint64 {if load,ok := loads[sealtasks.TTReadUnsealed];ok{return load}else{return 0}}(),
				},
			}
			table.Output(loadsTab)
		}

		return nil
	},
}

var sealingQueueCmd = &cli.Command{
	Name: "sched-queue",
	Usage: "Print task queue",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		queue := api.SchedQueue(lcli.ReqContext(cctx))
		if len(queue) == 0 {
			fmt.Println("Task queue is empty")
		} else  {
			tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintf(tw, "No.\tSector\tTaskType\tPriority\tIndex\tIndexHeap\tStart\n")
			for i, t := range queue {
				_, _ = fmt.Fprintf(tw, "%d\t%d\t%s\t%d\t%d\t%d\t%s\n",
					i,
					t.SectorID,
					string(t.TaskType),
					t.Priority,
					t.Index,
					t.IndexHeap,
					t.Start.String())
			}

			return tw.Flush()
		}

		return nil
	},
}

var sealingWorkerTodosCmd = &cli.Command{
	Name: "worker-todos",
	Usage: "Print worker todo tasks",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		workersTodo := api.SchedWorkerTodos(lcli.ReqContext(cctx))
		if len(workersTodo) == 0 {
			fmt.Printf("Can't find any worker")
			return nil
		}

		hostnames := map[uint64]string{}

		wst, err := api.WorkerStats(lcli.ReqContext(cctx))
		if err != nil {
			return xerrors.Errorf("getting worker stats: %w", err)
		}

		for wid, st := range wst {
			hostnames[wid] = st.Info.Hostname
		}

		for wid, todos := range workersTodo {
			if len(todos) == 0 {
				continue
			}

			fmt.Printf("worker %d(%s):\n", wid, hostnames[uint64(wid)])

			var todosTab = make([]workerTodosTable, len(todos))
			for i, todo := range todos {
				todosTab[i] = workerTodosTable{
					SectorID:  uint64(todo.SectorID.Number),
					TaskType:  todo.TaskType.Short(),
					Priority:  todo.Priority,
					Start:     todo.Start,
					Index:     todo.Index,
					IndexHeap: todo.IndexHeap,
				}
			}

			table.Output(todosTab)
		}

		return nil
	},
}

var sealingListIndexSectorsCmd = &cli.Command{
	Name: "list-index-sectors",
	Usage: "Print sectors in index",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		index := api.ListIndexSectors(lcli.ReqContext(cctx))

		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(tw, "Sector\tFileType\tStorageIDs\n")
		for _, info := range index {
			_, _ = fmt.Fprintf(tw, "%v\t%s\t%v\n", info.SectorID.Number, info.FileType.String(), info.StorageIDs)
		}

		return tw.Flush()
	},
}

var sealingListMatchesCmd = &cli.Command{
	Name: "list-matches",
	Usage: "Print matches storage",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		matches := api.SchedListMatches(lcli.ReqContext(cctx))
		tw := tabwriter.NewWriter(os.Stdout, 2, 4,2,' ', 0)
		_, _ = fmt.Fprintf(tw, "UUID\tMatchHosts\n")
		for id, hosts := range matches {
			_, _ = fmt.Fprintf(tw, "%s\t%v\n", id, hosts)
		}

		return tw.Flush()
	},
}
