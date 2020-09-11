package storage

import (
	"context"
	"github.com/filecoin-project/specs-actors/actors/builtin/miner"
	"github.com/filecoin-project/specs-actors/actors/util/adt"
	"time"

	"github.com/filecoin-project/go-state-types/dline"

	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/specs-storage/storage"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/store"
	"github.com/filecoin-project/lotus/chain/types"
	sectorstorage "github.com/filecoin-project/lotus/extern/sector-storage"
	"github.com/filecoin-project/lotus/node/config"

	"go.opencensus.io/trace"
)

const StartConfidence = 4 // TODO: config

type WindowPoStScheduler struct {
	api              storageMinerApi
	feeCfg           config.MinerFeeConfig
	prover           storage.Prover
	faultTracker     sectorstorage.FaultTracker
	proofType        abi.RegisteredPoStProof
	partitionSectors uint64

	actor  address.Address
	worker address.Address

	cur *types.TipSet

	// if a post is in progress, this indicates for which ElectionPeriodStart
	activeDeadline *dline.Info
	abort          context.CancelFunc

	//failed abi.ChainEpoch // eps
	//failLk sync.Mutex
}

func NewWindowedPoStScheduler(api storageMinerApi, fc config.MinerFeeConfig, sb storage.Prover, ft sectorstorage.FaultTracker, actor address.Address, worker address.Address) (*WindowPoStScheduler, error) {
	mi, err := api.StateMinerInfo(context.TODO(), actor, types.EmptyTSK)
	if err != nil {
		return nil, xerrors.Errorf("getting sector size: %w", err)
	}

	rt, err := mi.SealProofType.RegisteredWindowPoStProof()
	if err != nil {
		return nil, err
	}

	return &WindowPoStScheduler{
		api:              api,
		feeCfg:           fc,
		prover:           sb,
		faultTracker:     ft,
		proofType:        rt,
		partitionSectors: mi.WindowPoStPartitionSectors,

		actor:  actor,
		worker: worker,
	}, nil
}

func deadlineEquals(a, b *dline.Info) bool {
	if a == nil || b == nil {
		return b == a
	}

	return a.PeriodStart == b.PeriodStart && a.Index == b.Index && a.Challenge == b.Challenge
}

func (s *WindowPoStScheduler) Run(ctx context.Context) {
	defer s.abortActivePoSt()

	var notifs <-chan []*api.HeadChange
	var err error
	var gotCur bool

	// not fine to panic after this point
	for {
		if notifs == nil {
			notifs, err = s.api.ChainNotify(ctx)
			if err != nil {
				log.Errorf("ChainNotify error: %+v", err)

				build.Clock.Sleep(10 * time.Second)
				continue
			}

			gotCur = false
		}

		select {
		case changes, ok := <-notifs:
			if !ok {
				log.Warn("WindowPoStScheduler notifs channel closed")
				notifs = nil
				continue
			}

			if !gotCur {
				if len(changes) != 1 {
					log.Errorf("expected first notif to have len = 1")
					continue
				}
				if changes[0].Type != store.HCCurrent {
					log.Errorf("expected first notif to tell current ts")
					continue
				}

				if err := s.update(ctx, changes[0].Val); err != nil {
					log.Errorf("%+v", err)
				}

				gotCur = true
				continue
			}

			ctx, span := trace.StartSpan(ctx, "WindowPoStScheduler.headChange")

			var lowest, highest *types.TipSet = s.cur, nil

			for _, change := range changes {
				if change.Val == nil {
					log.Errorf("change.Val was nil")
				}
				switch change.Type {
				case store.HCRevert:
					lowest = change.Val
				case store.HCApply:
					highest = change.Val
				}
			}

			if err := s.revert(ctx, lowest); err != nil {
				log.Error("handling head reverts in windowPost sched: %+v", err)
			}
			if err := s.update(ctx, highest); err != nil {
				log.Error("handling head updates in windowPost sched: %+v", err)
			}

			span.End()
		case <-ctx.Done():
			return
		}
	}
}

func (s *WindowPoStScheduler) revert(ctx context.Context, newLowest *types.TipSet) error {
	if s.cur == newLowest {
		return nil
	}
	s.cur = newLowest

	newDeadline, err := s.api.StateMinerProvingDeadline(ctx, s.actor, newLowest.Key())
	if err != nil {
		return err
	}

	if !deadlineEquals(s.activeDeadline, newDeadline) {
		s.abortActivePoSt()
	}

	return nil
}

func (s *WindowPoStScheduler) update(ctx context.Context, new *types.TipSet) error {
	if new == nil {
		return xerrors.Errorf("no new tipset in WindowPoStScheduler.update")
	}

	di, err := s.api.StateMinerProvingDeadline(ctx, s.actor, new.Key())
	if err != nil {
		return err
	}

	log.Infof("deadline: %+v", di)
	log.Infof("activeDeadline: %+v", s.activeDeadline)

	if deadlineEquals(s.activeDeadline, di) {
		log.Warn("activeDeadline equals newDeadline")
		return nil // already working on this deadline
	}

	if !di.PeriodStarted() {
		log.Warn("verification period has not yet started")
		return nil // not proving anything yet
	}

	s.abortActivePoSt()

	// TODO: wait for di.Challenge here, will give us ~10min more to compute windowpost
	//  (Need to get correct deadline above, which is tricky)

	if di.Open+StartConfidence >= new.Height() {
		log.Info("not starting windowPost yet, waiting for startconfidence", di.Open, di.Open+StartConfidence, new.Height())
		return nil
	}

	/*s.failLk.Lock()
	if s.failed > 0 {
		s.failed = 0
		s.activeEPS = 0
	}
	s.failLk.Unlock()*/
	log.Infof("at %d, doPost for P %d, dd %d", new.Height(), di.PeriodStart, di.Index)

	s.doPost(ctx, di, new)

	return nil
}

func (s *WindowPoStScheduler) abortActivePoSt() {
	if s.activeDeadline == nil {
		return // noop
	}

	if s.abort != nil {
		s.abort()
	}

	log.Warnf("Aborting Window PoSt (Deadline: %+v)", s.activeDeadline)

	s.activeDeadline = nil
	s.abort = nil
}

func (s *WindowPoStScheduler) TryRecoverPoSt(ctx context.Context, store adt.Store, mas *miner.State) error {
	var hc = make([]*api.HeadChange, 0)

	for {
		notifys, err := s.api.ChainNotify(ctx)
		if err != nil {
			return err
		}

		hc = <-notifys
		if len(hc) == 1 {
			if hc[0] != nil {
				break
			}
		}
	}

	di, err := s.api.StateMinerProvingDeadline(ctx, s.actor, hc[0].Val.Key())
	if err != nil {
		return err
	}

	//dls, err := mas.LoadDeadlines(store)
	//if err != nil {
	//	return err
	//}
	//
	//dls.UpdateDeadline(store, 0, dls)

	for i := uint64(0); i < miner.WPoStPeriodDeadlines; i++ {
		par, err := s.api.StateMinerPartitions(ctx, s.actor, i, hc[0].Val.Key())
		if err != nil {
			return err
		}

		if len(par) > 0 {
			di.Index = i

			s.doPost(ctx, di, hc[0].Val)
		}

	}

	return nil
}
