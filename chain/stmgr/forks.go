package stmgr

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"

	multisig0 "github.com/filecoin-project/specs-actors/actors/builtin/multisig"

	"github.com/filecoin-project/lotus/chain/actors/builtin/multisig"

	"github.com/filecoin-project/lotus/chain/state"

	"github.com/filecoin-project/specs-actors/actors/migration/nv3"

	"github.com/ipfs/go-cid"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"

	builtin0 "github.com/filecoin-project/specs-actors/actors/builtin"
	init0 "github.com/filecoin-project/specs-actors/actors/builtin/init"
	miner0 "github.com/filecoin-project/specs-actors/actors/builtin/miner"
	power0 "github.com/filecoin-project/specs-actors/actors/builtin/power"
	adt0 "github.com/filecoin-project/specs-actors/actors/util/adt"

	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/vm"
	cbor "github.com/ipfs/go-ipld-cbor"
	"golang.org/x/xerrors"
)

var ForksAtHeight = map[abi.ChainEpoch]func(context.Context, *StateManager, ExecCallback, cid.Cid, *types.TipSet) (cid.Cid, error){
	build.UpgradeBreezeHeight:   UpgradeFaucetBurnRecovery,
	build.UpgradeIgnitionHeight: UpgradeIgnition,
	build.UpgradeLiftoffHeight:  UpgradeLiftoff,
}

func (sm *StateManager) handleStateForks(ctx context.Context, root cid.Cid, height abi.ChainEpoch, cb ExecCallback, ts *types.TipSet) (cid.Cid, error) {
	retCid := root
	var err error
	f, ok := ForksAtHeight[height]
	if ok {
		retCid, err = f(ctx, sm, cb, root, ts)
		if err != nil {
			return cid.Undef, err
		}
	}

	return retCid, nil
}

func doTransfer(cb ExecCallback, tree types.StateTree, from, to address.Address, amt abi.TokenAmount) error {
	fromAct, err := tree.GetActor(from)
	if err != nil {
		return xerrors.Errorf("failed to get 'from' actor for transfer: %w", err)
	}

	fromAct.Balance = types.BigSub(fromAct.Balance, amt)
	if fromAct.Balance.Sign() < 0 {
		return xerrors.Errorf("(sanity) deducted more funds from target account than it had (%s, %s)", from, types.FIL(amt))
	}

	if err := tree.SetActor(from, fromAct); err != nil {
		return xerrors.Errorf("failed to persist from actor: %w", err)
	}

	toAct, err := tree.GetActor(to)
	if err != nil {
		return xerrors.Errorf("failed to get 'to' actor for transfer: %w", err)
	}

	toAct.Balance = types.BigAdd(toAct.Balance, amt)

	if err := tree.SetActor(to, toAct); err != nil {
		return xerrors.Errorf("failed to persist to actor: %w", err)
	}

	if cb != nil {
		// record the transfer in execution traces

		fakeMsg := &types.Message{
			From:  from,
			To:    to,
			Value: amt,
			Nonce: math.MaxUint64,
		}
		fakeRct := &types.MessageReceipt{
			ExitCode: 0,
			Return:   nil,
			GasUsed:  0,
		}

		if err := cb(fakeMsg.Cid(), fakeMsg, &vm.ApplyRet{
			MessageReceipt: *fakeRct,
			ActorErr:       nil,
			ExecutionTrace: types.ExecutionTrace{
				Msg:        fakeMsg,
				MsgRct:     fakeRct,
				Error:      "",
				Duration:   0,
				GasCharges: nil,
				Subcalls:   nil,
			},
			Duration: 0,
			GasCosts: vm.ZeroGasOutputs(),
		}); err != nil {
			return xerrors.Errorf("recording transfer: %w", err)
		}
	}

	return nil
}

func UpgradeFaucetBurnRecovery(ctx context.Context, sm *StateManager, cb ExecCallback, root cid.Cid, ts *types.TipSet) (cid.Cid, error) {
	// Some initial parameters
	FundsForMiners := types.FromFil(1_000_000)
	LookbackEpoch := abi.ChainEpoch(32000)
	AccountCap := types.FromFil(0)
	BaseMinerBalance := types.FromFil(20)
	DesiredReimbursementBalance := types.FromFil(5_000_000)

	isSystemAccount := func(addr address.Address) (bool, error) {
		id, err := address.IDFromAddress(addr)
		if err != nil {
			return false, xerrors.Errorf("id address: %w", err)
		}

		if id < 1000 {
			return true, nil
		}
		return false, nil
	}

	minerFundsAlloc := func(pow, tpow abi.StoragePower) abi.TokenAmount {
		return types.BigDiv(types.BigMul(pow, FundsForMiners), tpow)
	}

	// Grab lookback state for account checks
	lbts, err := sm.ChainStore().GetTipsetByHeight(ctx, LookbackEpoch, ts, false)
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to get tipset at lookback height: %w", err)
	}

	lbtree, err := sm.ParentState(lbts)
	if err != nil {
		return cid.Undef, xerrors.Errorf("loading state tree failed: %w", err)
	}

	ReserveAddress, err := address.NewFromString("t090")
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to parse reserve address: %w", err)
	}

	tree, err := sm.StateTree(root)
	if err != nil {
		return cid.Undef, xerrors.Errorf("getting state tree: %w", err)
	}

	type transfer struct {
		From address.Address
		To   address.Address
		Amt  abi.TokenAmount
	}

	var transfers []transfer

	// Take all excess funds away, put them into the reserve account
	err = tree.ForEach(func(addr address.Address, act *types.Actor) error {
		switch act.Code {
		case builtin0.AccountActorCodeID, builtin0.MultisigActorCodeID, builtin0.PaymentChannelActorCodeID:
			sysAcc, err := isSystemAccount(addr)
			if err != nil {
				return xerrors.Errorf("checking system account: %w", err)
			}

			if !sysAcc {
				transfers = append(transfers, transfer{
					From: addr,
					To:   ReserveAddress,
					Amt:  act.Balance,
				})
			}
		case builtin0.StorageMinerActorCodeID:
			var st miner0.State
			if err := sm.ChainStore().Store(ctx).Get(ctx, act.Head, &st); err != nil {
				return xerrors.Errorf("failed to load miner state: %w", err)
			}

			var available abi.TokenAmount
			{
				defer func() {
					if err := recover(); err != nil {
						log.Warnf("Get available balance failed (%s, %s, %s): %s", addr, act.Head, act.Balance, err)
					}
					available = abi.NewTokenAmount(0)
				}()
				// this panics if the miner doesnt have enough funds to cover their locked pledge
				available = st.GetAvailableBalance(act.Balance)
			}

			transfers = append(transfers, transfer{
				From: addr,
				To:   ReserveAddress,
				Amt:  available,
			})
		}
		return nil
	})
	if err != nil {
		return cid.Undef, xerrors.Errorf("foreach over state tree failed: %w", err)
	}

	// Execute transfers from previous step
	for _, t := range transfers {
		if err := doTransfer(cb, tree, t.From, t.To, t.Amt); err != nil {
			return cid.Undef, xerrors.Errorf("transfer %s %s->%s failed: %w", t.Amt, t.From, t.To, err)
		}
	}

	// pull up power table to give miners back some funds proportional to their power
	var ps power0.State
	powAct, err := tree.GetActor(builtin0.StoragePowerActorAddr)
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to load power actor: %w", err)
	}

	cst := cbor.NewCborStore(sm.ChainStore().Blockstore())
	if err := cst.Get(ctx, powAct.Head, &ps); err != nil {
		return cid.Undef, xerrors.Errorf("failed to get power actor state: %w", err)
	}

	totalPower := ps.TotalBytesCommitted

	var transfersBack []transfer
	// Now, we return some funds to places where they are needed
	err = tree.ForEach(func(addr address.Address, act *types.Actor) error {
		lbact, err := lbtree.GetActor(addr)
		if err != nil {
			if !xerrors.Is(err, types.ErrActorNotFound) {
				return xerrors.Errorf("failed to get actor in lookback state")
			}
		}

		prevBalance := abi.NewTokenAmount(0)
		if lbact != nil {
			prevBalance = lbact.Balance
		}

		switch act.Code {
		case builtin0.AccountActorCodeID, builtin0.MultisigActorCodeID, builtin0.PaymentChannelActorCodeID:
			nbalance := big.Min(prevBalance, AccountCap)
			if nbalance.Sign() != 0 {
				transfersBack = append(transfersBack, transfer{
					From: ReserveAddress,
					To:   addr,
					Amt:  nbalance,
				})
			}
		case builtin0.StorageMinerActorCodeID:
			var st miner0.State
			if err := sm.ChainStore().Store(ctx).Get(ctx, act.Head, &st); err != nil {
				return xerrors.Errorf("failed to load miner state: %w", err)
			}

			var minfo miner0.MinerInfo
			if err := cst.Get(ctx, st.Info, &minfo); err != nil {
				return xerrors.Errorf("failed to get miner info: %w", err)
			}

			sectorsArr, err := adt0.AsArray(sm.ChainStore().Store(ctx), st.Sectors)
			if err != nil {
				return xerrors.Errorf("failed to load sectors array: %w", err)
			}

			slen := sectorsArr.Length()

			power := types.BigMul(types.NewInt(slen), types.NewInt(uint64(minfo.SectorSize)))

			mfunds := minerFundsAlloc(power, totalPower)
			transfersBack = append(transfersBack, transfer{
				From: ReserveAddress,
				To:   minfo.Worker,
				Amt:  mfunds,
			})

			// Now make sure to give each miner who had power at the lookback some FIL
			lbact, err := lbtree.GetActor(addr)
			if err == nil {
				var lbst miner0.State
				if err := sm.ChainStore().Store(ctx).Get(ctx, lbact.Head, &lbst); err != nil {
					return xerrors.Errorf("failed to load miner state: %w", err)
				}

				lbsectors, err := adt0.AsArray(sm.ChainStore().Store(ctx), lbst.Sectors)
				if err != nil {
					return xerrors.Errorf("failed to load lb sectors array: %w", err)
				}

				if lbsectors.Length() > 0 {
					transfersBack = append(transfersBack, transfer{
						From: ReserveAddress,
						To:   minfo.Worker,
						Amt:  BaseMinerBalance,
					})
				}

			} else {
				log.Warnf("failed to get miner in lookback state: %s", err)
			}
		}
		return nil
	})
	if err != nil {
		return cid.Undef, xerrors.Errorf("foreach over state tree failed: %w", err)
	}

	for _, t := range transfersBack {
		if err := doTransfer(cb, tree, t.From, t.To, t.Amt); err != nil {
			return cid.Undef, xerrors.Errorf("transfer %s %s->%s failed: %w", t.Amt, t.From, t.To, err)
		}
	}

	// transfer all burnt funds back to the reserve account
	burntAct, err := tree.GetActor(builtin0.BurntFundsActorAddr)
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to load burnt funds actor: %w", err)
	}
	if err := doTransfer(cb, tree, builtin0.BurntFundsActorAddr, ReserveAddress, burntAct.Balance); err != nil {
		return cid.Undef, xerrors.Errorf("failed to unburn funds: %w", err)
	}

	// Top up the reimbursement service
	reimbAddr, err := address.NewFromString("t0111")
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to parse reimbursement service address")
	}

	reimb, err := tree.GetActor(reimbAddr)
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to load reimbursement account actor: %w", err)
	}

	difference := types.BigSub(DesiredReimbursementBalance, reimb.Balance)
	if err := doTransfer(cb, tree, ReserveAddress, reimbAddr, difference); err != nil {
		return cid.Undef, xerrors.Errorf("failed to top up reimbursement account: %w", err)
	}

	// Now, a final sanity check to make sure the balances all check out
	total := abi.NewTokenAmount(0)
	err = tree.ForEach(func(addr address.Address, act *types.Actor) error {
		total = types.BigAdd(total, act.Balance)
		return nil
	})
	if err != nil {
		return cid.Undef, xerrors.Errorf("checking final state balance failed: %w", err)
	}

	exp := types.FromFil(build.FilBase)
	if !exp.Equals(total) {
		return cid.Undef, xerrors.Errorf("resultant state tree account balance was not correct: %s", total)
	}

	return tree.Flush(ctx)
}

func UpgradeIgnition(ctx context.Context, sm *StateManager, cb ExecCallback, root cid.Cid, ts *types.TipSet) (cid.Cid, error) {
	store := sm.cs.Store(ctx)

	nst, err := nv3.MigrateStateTree(ctx, store, root, build.UpgradeIgnitionHeight)
	if err != nil {
		return cid.Undef, xerrors.Errorf("migrating actors state: %w", err)
	}

	tree, err := sm.StateTree(nst)
	if err != nil {
		return cid.Undef, xerrors.Errorf("getting state tree: %w", err)
	}

	err = setNetworkName(ctx, store, tree, "ignition")
	if err != nil {
		return cid.Undef, xerrors.Errorf("setting network name: %w", err)
	}

	split1, err := address.NewFromString("t0115")
	if err != nil {
		return cid.Undef, xerrors.Errorf("first split address: %w", err)
	}

	split2, err := address.NewFromString("t0116")
	if err != nil {
		return cid.Undef, xerrors.Errorf("second split address: %w", err)
	}

	err = resetGenesisMsigs(ctx, sm, store, tree)
	if err != nil {
		return cid.Undef, xerrors.Errorf("resetting genesis msig start epochs: %w", err)
	}

	err = splitGenesisMultisig(ctx, cb, split1, store, tree, 50)
	if err != nil {
		return cid.Undef, xerrors.Errorf("splitting first msig: %w", err)
	}

	err = splitGenesisMultisig(ctx, cb, split2, store, tree, 50)
	if err != nil {
		return cid.Undef, xerrors.Errorf("splitting second msig: %w", err)
	}

	err = nv3.CheckStateTree(ctx, store, nst, build.UpgradeIgnitionHeight, builtin0.TotalFilecoin)
	if err != nil {
		return cid.Undef, xerrors.Errorf("sanity check after ignition upgrade failed: %w", err)
	}

	return tree.Flush(ctx)
}

func UpgradeLiftoff(ctx context.Context, sm *StateManager, cb ExecCallback, root cid.Cid, ts *types.TipSet) (cid.Cid, error) {
	tree, err := sm.StateTree(root)
	if err != nil {
		return cid.Undef, xerrors.Errorf("getting state tree: %w", err)
	}

	err = setNetworkName(ctx, sm.cs.Store(ctx), tree, "mainnet")
	if err != nil {
		return cid.Undef, xerrors.Errorf("setting network name: %w", err)
	}

	return tree.Flush(ctx)
}

func setNetworkName(ctx context.Context, store adt0.Store, tree *state.StateTree, name string) error {
	ia, err := tree.GetActor(builtin0.InitActorAddr)
	if err != nil {
		return xerrors.Errorf("getting init actor: %w", err)
	}

	var initState init0.State
	if err := store.Get(ctx, ia.Head, &initState); err != nil {
		return xerrors.Errorf("reading init state: %w", err)
	}

	initState.NetworkName = name

	ia.Head, err = store.Put(ctx, &initState)
	if err != nil {
		return xerrors.Errorf("writing new init state: %w", err)
	}

	if err := tree.SetActor(builtin0.InitActorAddr, ia); err != nil {
		return xerrors.Errorf("setting init actor: %w", err)
	}

	return nil
}

func splitGenesisMultisig(ctx context.Context, cb ExecCallback, addr address.Address, store adt0.Store, tree *state.StateTree, portions uint64) error {
	if portions < 1 {
		return xerrors.Errorf("cannot split into 0 portions")
	}

	mact, err := tree.GetActor(addr)
	if err != nil {
		return xerrors.Errorf("getting msig actor: %w", err)
	}

	mst, err := multisig.Load(store, mact)
	if err != nil {
		return xerrors.Errorf("getting msig state: %w", err)
	}

	signers, err := mst.Signers()
	if err != nil {
		return xerrors.Errorf("getting msig signers: %w", err)
	}

	thresh, err := mst.Threshold()
	if err != nil {
		return xerrors.Errorf("getting msig threshold: %w", err)
	}

	ibal, err := mst.InitialBalance()
	if err != nil {
		return xerrors.Errorf("getting msig initial balance: %w", err)
	}

	se, err := mst.StartEpoch()
	if err != nil {
		return xerrors.Errorf("getting msig start epoch: %w", err)
	}

	ud, err := mst.UnlockDuration()
	if err != nil {
		return xerrors.Errorf("getting msig unlock duration: %w", err)
	}

	pending, err := adt0.MakeEmptyMap(store).Root()
	if err != nil {
		return xerrors.Errorf("failed to create empty map: %w", err)
	}

	newIbal := big.Div(ibal, types.NewInt(portions))
	newState := &multisig0.State{
		Signers:               signers,
		NumApprovalsThreshold: thresh,
		NextTxnID:             0,
		InitialBalance:        newIbal,
		StartEpoch:            se,
		UnlockDuration:        ud,
		PendingTxns:           pending,
	}

	scid, err := store.Put(ctx, newState)
	if err != nil {
		return xerrors.Errorf("storing new state: %w", err)
	}

	newActor := types.Actor{
		Code:    builtin0.MultisigActorCodeID,
		Head:    scid,
		Nonce:   0,
		Balance: big.Zero(),
	}

	i := uint64(0)
	for i < portions {
		keyAddr, err := makeKeyAddr(addr, i)
		if err != nil {
			return xerrors.Errorf("creating key address: %w", err)
		}

		idAddr, err := tree.RegisterNewAddress(keyAddr)
		if err != nil {
			return xerrors.Errorf("registering new address: %w", err)
		}

		err = tree.SetActor(idAddr, &newActor)
		if err != nil {
			return xerrors.Errorf("setting new msig actor state: %w", err)
		}

		if err := doTransfer(cb, tree, addr, idAddr, newIbal); err != nil {
			return xerrors.Errorf("transferring split msig balance: %w", err)
		}

		i++
	}

	return nil
}

func makeKeyAddr(splitAddr address.Address, count uint64) (address.Address, error) {
	var b bytes.Buffer
	if err := splitAddr.MarshalCBOR(&b); err != nil {
		return address.Undef, xerrors.Errorf("marshalling split address: %w", err)
	}

	if err := binary.Write(&b, binary.BigEndian, count); err != nil {
		return address.Undef, xerrors.Errorf("writing count into a buffer: %w", err)
	}

	if err := binary.Write(&b, binary.BigEndian, []byte("Ignition upgrade")); err != nil {
		return address.Undef, xerrors.Errorf("writing fork name into a buffer: %w", err)
	}

	addr, err := address.NewActorAddress(b.Bytes())
	if err != nil {
		return address.Undef, xerrors.Errorf("create actor address: %w", err)
	}

	return addr, nil
}

func resetGenesisMsigs(ctx context.Context, sm *StateManager, store adt0.Store, tree *state.StateTree) error {
	gb, err := sm.cs.GetGenesis()
	if err != nil {
		return xerrors.Errorf("getting genesis block: %w", err)
	}

	gts, err := types.NewTipSet([]*types.BlockHeader{gb})
	if err != nil {
		return xerrors.Errorf("getting genesis tipset: %w", err)
	}

	cst := cbor.NewCborStore(sm.cs.Blockstore())
	genesisTree, err := state.LoadStateTree(cst, gts.ParentState())
	if err != nil {
		return xerrors.Errorf("loading state tree: %w", err)
	}

	err = genesisTree.ForEach(func(addr address.Address, genesisActor *types.Actor) error {
		if genesisActor.Code == builtin0.MultisigActorCodeID {
			currActor, err := tree.GetActor(addr)
			if err != nil {
				return xerrors.Errorf("loading actor: %w", err)
			}

			var currState multisig0.State
			if err := store.Get(ctx, currActor.Head, &currState); err != nil {
				return xerrors.Errorf("reading multisig state: %w", err)
			}

			currState.StartEpoch = build.UpgradeLiftoffHeight

			currActor.Head, err = store.Put(ctx, &currState)
			if err != nil {
				return xerrors.Errorf("writing new multisig state: %w", err)
			}

			if err := tree.SetActor(addr, currActor); err != nil {
				return xerrors.Errorf("setting multisig actor: %w", err)
			}
		}
		return nil
	})

	if err != nil {
		return xerrors.Errorf("iterating over genesis actors: %w", err)
	}

	return nil
}
