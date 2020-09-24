// +build !debug
// +build !2k
// +build !testground

package build

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/specs-actors/actors/builtin"
	miner0 "github.com/filecoin-project/specs-actors/actors/builtin/miner"
	power0 "github.com/filecoin-project/specs-actors/actors/builtin/power"
)

var DrandSchedule = map[abi.ChainEpoch]DrandEnum{
	0:                  DrandIncentinet,
	UpgradeSmokeHeight: DrandMainnet,
}

const UpgradeBreezeHeight = 41280
const BreezeGasTampingDuration = 120

const UpgradeSmokeHeight = 51000

func init() {
	power0.ConsensusMinerMinPower = big.NewInt(10 << 40)
	miner0.SupportedProofTypes = map[abi.RegisteredSealProof]struct{}{
		abi.RegisteredSealProof_StackedDrg32GiBV1: {},
		abi.RegisteredSealProof_StackedDrg64GiBV1: {},
	}
}

const BlockDelaySecs = uint64(builtin.EpochDurationSeconds)

const PropagationDelaySecs = uint64(6)
