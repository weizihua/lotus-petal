package miner

import (
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
	cbg "github.com/whyrusleeping/cbor-gen"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/cbor"
	"github.com/filecoin-project/go-state-types/dline"
	builtin0 "github.com/filecoin-project/specs-actors/actors/builtin"
	miner0 "github.com/filecoin-project/specs-actors/actors/builtin/miner"

	"github.com/filecoin-project/lotus/chain/actors/adt"
	"github.com/filecoin-project/lotus/chain/types"
)

// Unchanged between v0 and v1 actors
var WPoStProvingPeriod = miner0.WPoStProvingPeriod
var WPoStPeriodDeadlines = miner0.WPoStPeriodDeadlines
var WPoStChallengeWindow = miner0.WPoStChallengeWindow
var WPoStChallengeLookback = miner0.WPoStChallengeLookback
var FaultDeclarationCutoff = miner0.FaultDeclarationCutoff

const MinSectorExpiration = miner0.MinSectorExpiration

func Load(store adt.Store, act *types.Actor) (st State, err error) {
	switch act.Code {
	case builtin0.StorageMinerActorCodeID:
		out := state0{store: store}
		err := store.Get(store.Context(), act.Head, &out)
		if err != nil {
			return nil, err
		}
		return &out, nil
	}
	return nil, xerrors.Errorf("unknown actor code %s", act.Code)
}

type State interface {
	cbor.Marshaler

	// Total available balance to spend.
	AvailableBalance(abi.TokenAmount) (abi.TokenAmount, error)
	// Funds that will vest by the given epoch.
	VestedFunds(abi.ChainEpoch) (abi.TokenAmount, error)
	// Funds locked for various reasons.
	LockedFunds() (LockedFunds, error)

	GetSector(abi.SectorNumber) (*SectorOnChainInfo, error)
	FindSector(abi.SectorNumber) (*SectorLocation, error)
	GetSectorExpiration(abi.SectorNumber) (*SectorExpiration, error)
	GetPrecommittedSector(abi.SectorNumber) (*SectorPreCommitOnChainInfo, error)
	LoadSectors(sectorNos *bitfield.BitField) ([]*SectorOnChainInfo, error)
	NumLiveSectors() (uint64, error)
	IsAllocated(abi.SectorNumber) (bool, error)

	LoadDeadline(idx uint64) (Deadline, error)
	ForEachDeadline(cb func(idx uint64, dl Deadline) error) error
	NumDeadlines() (uint64, error)
	DeadlinesChanged(State) (bool, error)

	Info() (MinerInfo, error)

	DeadlineInfo(epoch abi.ChainEpoch) (*dline.Info, error)

	// Diff helpers. Used by Diff* functions internally.
	sectors() (adt.Array, error)
	decodeSectorOnChainInfo(*cbg.Deferred) (SectorOnChainInfo, error)
	precommits() (adt.Map, error)
	decodeSectorPreCommitOnChainInfo(*cbg.Deferred) (SectorPreCommitOnChainInfo, error)
}

type Deadline interface {
	LoadPartition(idx uint64) (Partition, error)
	ForEachPartition(cb func(idx uint64, part Partition) error) error
	PostSubmissions() (bitfield.BitField, error)

	PartitionsChanged(Deadline) (bool, error)
}

type Partition interface {
	AllSectors() (bitfield.BitField, error)
	FaultySectors() (bitfield.BitField, error)
	RecoveringSectors() (bitfield.BitField, error)
	LiveSectors() (bitfield.BitField, error)
	ActiveSectors() (bitfield.BitField, error)
}

type SectorOnChainInfo struct {
	SectorNumber          abi.SectorNumber
	SealProof             abi.RegisteredSealProof
	SealedCID             cid.Cid
	DealIDs               []abi.DealID
	Activation            abi.ChainEpoch
	Expiration            abi.ChainEpoch
	DealWeight            abi.DealWeight
	VerifiedDealWeight    abi.DealWeight
	InitialPledge         abi.TokenAmount
	ExpectedDayReward     abi.TokenAmount
	ExpectedStoragePledge abi.TokenAmount
}

type SectorPreCommitInfo = miner0.SectorPreCommitInfo

type SectorPreCommitOnChainInfo struct {
	Info               SectorPreCommitInfo
	PreCommitDeposit   abi.TokenAmount
	PreCommitEpoch     abi.ChainEpoch
	DealWeight         abi.DealWeight
	VerifiedDealWeight abi.DealWeight
}

type PoStPartition = miner0.PoStPartition
type RecoveryDeclaration = miner0.RecoveryDeclaration
type FaultDeclaration = miner0.FaultDeclaration

// Params
type DeclareFaultsParams = miner0.DeclareFaultsParams
type DeclareFaultsRecoveredParams = miner0.DeclareFaultsRecoveredParams
type SubmitWindowedPoStParams = miner0.SubmitWindowedPoStParams
type ProveCommitSectorParams = miner0.ProveCommitSectorParams

type MinerInfo struct {
	Owner                      address.Address   // Must be an ID-address.
	Worker                     address.Address   // Must be an ID-address.
	NewWorker                  address.Address   // Must be an ID-address.
	ControlAddresses           []address.Address // Must be an ID-addresses.
	WorkerChangeEpoch          abi.ChainEpoch
	PeerId                     *peer.ID
	Multiaddrs                 []abi.Multiaddrs
	SealProofType              abi.RegisteredSealProof
	SectorSize                 abi.SectorSize
	WindowPoStPartitionSectors uint64
}

type SectorExpiration struct {
	OnTime abi.ChainEpoch

	// non-zero if sector is faulty, epoch at which it will be permanently
	// removed if it doesn't recover
	Early abi.ChainEpoch
}

type SectorLocation struct {
	Deadline  uint64
	Partition uint64
}

type SectorChanges struct {
	Added    []SectorOnChainInfo
	Extended []SectorExtensions
	Removed  []SectorOnChainInfo
}

type SectorExtensions struct {
	From SectorOnChainInfo
	To   SectorOnChainInfo
}

type PreCommitChanges struct {
	Added   []SectorPreCommitOnChainInfo
	Removed []SectorPreCommitOnChainInfo
}

type LockedFunds struct {
	VestingFunds             abi.TokenAmount
	InitialPledgeRequirement abi.TokenAmount
	PreCommitDeposits        abi.TokenAmount
}
