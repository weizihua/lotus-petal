// Code generated by github.com/whyrusleeping/cbor-gen. DO NOT EDIT.

package miner

import (
	"fmt"
	"io"

	abi "github.com/filecoin-project/go-state-types/abi"
	cbg "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"
)

var _ = xerrors.Errorf

var lengthBufSectorOnChainInfo = []byte{139}

func (t *SectorOnChainInfo) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufSectorOnChainInfo); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.SectorNumber (abi.SectorNumber) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.SectorNumber)); err != nil {
		return err
	}

	// t.SealProof (abi.RegisteredSealProof) (int64)
	if t.SealProof >= 0 {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.SealProof)); err != nil {
			return err
		}
	} else {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajNegativeInt, uint64(-t.SealProof-1)); err != nil {
			return err
		}
	}

	// t.SealedCID (cid.Cid) (struct)

	if err := cbg.WriteCidBuf(scratch, w, t.SealedCID); err != nil {
		return xerrors.Errorf("failed to write cid field t.SealedCID: %w", err)
	}

	// t.DealIDs ([]abi.DealID) (slice)
	if len(t.DealIDs) > cbg.MaxLength {
		return xerrors.Errorf("Slice value in field t.DealIDs was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajArray, uint64(len(t.DealIDs))); err != nil {
		return err
	}
	for _, v := range t.DealIDs {
		if err := cbg.CborWriteHeader(w, cbg.MajUnsignedInt, uint64(v)); err != nil {
			return err
		}
	}

	// t.Activation (abi.ChainEpoch) (int64)
	if t.Activation >= 0 {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Activation)); err != nil {
			return err
		}
	} else {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajNegativeInt, uint64(-t.Activation-1)); err != nil {
			return err
		}
	}

	// t.Expiration (abi.ChainEpoch) (int64)
	if t.Expiration >= 0 {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Expiration)); err != nil {
			return err
		}
	} else {
		if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajNegativeInt, uint64(-t.Expiration-1)); err != nil {
			return err
		}
	}

	// t.DealWeight (big.Int) (struct)
	if err := t.DealWeight.MarshalCBOR(w); err != nil {
		return err
	}

	// t.VerifiedDealWeight (big.Int) (struct)
	if err := t.VerifiedDealWeight.MarshalCBOR(w); err != nil {
		return err
	}

	// t.InitialPledge (big.Int) (struct)
	if err := t.InitialPledge.MarshalCBOR(w); err != nil {
		return err
	}

	// t.ExpectedDayReward (big.Int) (struct)
	if err := t.ExpectedDayReward.MarshalCBOR(w); err != nil {
		return err
	}

	// t.ExpectedStoragePledge (big.Int) (struct)
	if err := t.ExpectedStoragePledge.MarshalCBOR(w); err != nil {
		return err
	}
	return nil
}

func (t *SectorOnChainInfo) UnmarshalCBOR(r io.Reader) error {
	*t = SectorOnChainInfo{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 11 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.SectorNumber (abi.SectorNumber) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.SectorNumber = abi.SectorNumber(extra)

	}
	// t.SealProof (abi.RegisteredSealProof) (int64)
	{
		maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
		var extraI int64
		if err != nil {
			return err
		}
		switch maj {
		case cbg.MajUnsignedInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 positive overflow")
			}
		case cbg.MajNegativeInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 negative oveflow")
			}
			extraI = -1 - extraI
		default:
			return fmt.Errorf("wrong type for int64 field: %d", maj)
		}

		t.SealProof = abi.RegisteredSealProof(extraI)
	}
	// t.SealedCID (cid.Cid) (struct)

	{

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field t.SealedCID: %w", err)
		}

		t.SealedCID = c

	}
	// t.DealIDs ([]abi.DealID) (slice)

	maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}

	if extra > cbg.MaxLength {
		return fmt.Errorf("t.DealIDs: array too large (%d)", extra)
	}

	if maj != cbg.MajArray {
		return fmt.Errorf("expected cbor array")
	}

	if extra > 0 {
		t.DealIDs = make([]abi.DealID, extra)
	}

	for i := 0; i < int(extra); i++ {

		maj, val, err := cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return xerrors.Errorf("failed to read uint64 for t.DealIDs slice: %w", err)
		}

		if maj != cbg.MajUnsignedInt {
			return xerrors.Errorf("value read for array t.DealIDs was not a uint, instead got %d", maj)
		}

		t.DealIDs[i] = abi.DealID(val)
	}

	// t.Activation (abi.ChainEpoch) (int64)
	{
		maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
		var extraI int64
		if err != nil {
			return err
		}
		switch maj {
		case cbg.MajUnsignedInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 positive overflow")
			}
		case cbg.MajNegativeInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 negative oveflow")
			}
			extraI = -1 - extraI
		default:
			return fmt.Errorf("wrong type for int64 field: %d", maj)
		}

		t.Activation = abi.ChainEpoch(extraI)
	}
	// t.Expiration (abi.ChainEpoch) (int64)
	{
		maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
		var extraI int64
		if err != nil {
			return err
		}
		switch maj {
		case cbg.MajUnsignedInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 positive overflow")
			}
		case cbg.MajNegativeInt:
			extraI = int64(extra)
			if extraI < 0 {
				return fmt.Errorf("int64 negative oveflow")
			}
			extraI = -1 - extraI
		default:
			return fmt.Errorf("wrong type for int64 field: %d", maj)
		}

		t.Expiration = abi.ChainEpoch(extraI)
	}
	// t.DealWeight (big.Int) (struct)

	{

		if err := t.DealWeight.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.DealWeight: %w", err)
		}

	}
	// t.VerifiedDealWeight (big.Int) (struct)

	{

		if err := t.VerifiedDealWeight.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.VerifiedDealWeight: %w", err)
		}

	}
	// t.InitialPledge (big.Int) (struct)

	{

		if err := t.InitialPledge.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.InitialPledge: %w", err)
		}

	}
	// t.ExpectedDayReward (big.Int) (struct)

	{

		if err := t.ExpectedDayReward.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.ExpectedDayReward: %w", err)
		}

	}
	// t.ExpectedStoragePledge (big.Int) (struct)

	{

		if err := t.ExpectedStoragePledge.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("unmarshaling t.ExpectedStoragePledge: %w", err)
		}

	}
	return nil
}
