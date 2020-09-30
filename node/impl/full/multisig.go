package full

import (
	"context"

	"github.com/filecoin-project/lotus/chain/stmgr"

	"github.com/filecoin-project/go-state-types/big"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors"
	init_ "github.com/filecoin-project/lotus/chain/actors/builtin/init"
	"github.com/filecoin-project/lotus/chain/types"

	builtin0 "github.com/filecoin-project/specs-actors/actors/builtin"
	init0 "github.com/filecoin-project/specs-actors/actors/builtin/init"
	multisig0 "github.com/filecoin-project/specs-actors/actors/builtin/multisig"

	"github.com/ipfs/go-cid"
	"github.com/minio/blake2b-simd"
	"go.uber.org/fx"
	"golang.org/x/xerrors"
)

type MsigAPI struct {
	fx.In

	StateManagerAPI stmgr.StateManagerAPI
	MpoolAPI        MpoolAPI
}

// TODO: remove gp (gasPrice) from arguments
func (a *MsigAPI) MsigCreate(ctx context.Context, req uint64, addrs []address.Address, duration abi.ChainEpoch, val types.BigInt, src address.Address, gp types.BigInt) (cid.Cid, error) {

	lenAddrs := uint64(len(addrs))

	if lenAddrs < req {
		return cid.Undef, xerrors.Errorf("cannot require signing of more addresses than provided for multisig")
	}

	if req == 0 {
		req = lenAddrs
	}

	if src == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide source address")
	}

	// Set up constructor parameters for multisig
	msigParams := &multisig0.ConstructorParams{
		Signers:               addrs,
		NumApprovalsThreshold: req,
		UnlockDuration:        duration,
	}

	enc, actErr := actors.SerializeParams(msigParams)
	if actErr != nil {
		return cid.Undef, actErr
	}

	// new actors are created by invoking 'exec' on the init actor with the constructor params
	// TODO: network upgrade?
	execParams := &init0.ExecParams{
		CodeCID:           builtin0.MultisigActorCodeID,
		ConstructorParams: enc,
	}

	enc, actErr = actors.SerializeParams(execParams)
	if actErr != nil {
		return cid.Undef, actErr
	}

	// now we create the message to send this with
	msg := types.Message{
		To:     init_.Address,
		From:   src,
		Method: builtin0.MethodsInit.Exec,
		Params: enc,
		Value:  val,
	}

	// send the message out to the network
	smsg, err := a.MpoolAPI.MpoolPushMessage(ctx, &msg, nil)
	if err != nil {
		return cid.Undef, err
	}

	return smsg.Cid(), nil
}

func (a *MsigAPI) MsigPropose(ctx context.Context, msig address.Address, to address.Address, amt types.BigInt, src address.Address, method uint64, params []byte) (cid.Cid, error) {

	if msig == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide a multisig address for proposal")
	}

	if to == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide a target address for proposal")
	}

	if amt.Sign() == -1 {
		return cid.Undef, xerrors.Errorf("must provide a positive amount for proposed send")
	}

	if src == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide source address")
	}

	enc, actErr := actors.SerializeParams(&multisig0.ProposeParams{
		To:     to,
		Value:  amt,
		Method: abi.MethodNum(method),
		Params: params,
	})
	if actErr != nil {
		return cid.Undef, xerrors.Errorf("failed to serialize parameters: %w", actErr)
	}

	msg := &types.Message{
		To:     msig,
		From:   src,
		Value:  types.NewInt(0),
		Method: builtin0.MethodsMultisig.Propose,
		Params: enc,
	}

	smsg, err := a.MpoolAPI.MpoolPushMessage(ctx, msg, nil)
	if err != nil {
		return cid.Undef, xerrors.Errorf("failed to push message: %w", err)
	}

	return smsg.Cid(), nil
}

func (a *MsigAPI) MsigAddPropose(ctx context.Context, msig address.Address, src address.Address, newAdd address.Address, inc bool) (cid.Cid, error) {
	enc, actErr := serializeAddParams(newAdd, inc)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigPropose(ctx, msig, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.AddSigner), enc)
}

func (a *MsigAPI) MsigAddApprove(ctx context.Context, msig address.Address, src address.Address, txID uint64, proposer address.Address, newAdd address.Address, inc bool) (cid.Cid, error) {
	enc, actErr := serializeAddParams(newAdd, inc)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigApprove(ctx, msig, txID, proposer, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.AddSigner), enc)
}

func (a *MsigAPI) MsigAddCancel(ctx context.Context, msig address.Address, src address.Address, txID uint64, newAdd address.Address, inc bool) (cid.Cid, error) {
	enc, actErr := serializeAddParams(newAdd, inc)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigCancel(ctx, msig, txID, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.AddSigner), enc)
}

func (a *MsigAPI) MsigSwapPropose(ctx context.Context, msig address.Address, src address.Address, oldAdd address.Address, newAdd address.Address) (cid.Cid, error) {
	enc, actErr := serializeSwapParams(oldAdd, newAdd)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigPropose(ctx, msig, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.SwapSigner), enc)
}

func (a *MsigAPI) MsigSwapApprove(ctx context.Context, msig address.Address, src address.Address, txID uint64, proposer address.Address, oldAdd address.Address, newAdd address.Address) (cid.Cid, error) {
	enc, actErr := serializeSwapParams(oldAdd, newAdd)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigApprove(ctx, msig, txID, proposer, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.SwapSigner), enc)
}

func (a *MsigAPI) MsigSwapCancel(ctx context.Context, msig address.Address, src address.Address, txID uint64, oldAdd address.Address, newAdd address.Address) (cid.Cid, error) {
	enc, actErr := serializeSwapParams(oldAdd, newAdd)
	if actErr != nil {
		return cid.Undef, actErr
	}

	return a.MsigCancel(ctx, msig, txID, msig, big.Zero(), src, uint64(builtin0.MethodsMultisig.SwapSigner), enc)
}

func (a *MsigAPI) MsigApprove(ctx context.Context, msig address.Address, txID uint64, proposer address.Address, to address.Address, amt types.BigInt, src address.Address, method uint64, params []byte) (cid.Cid, error) {
	return a.msigApproveOrCancel(ctx, api.MsigApprove, msig, txID, proposer, to, amt, src, method, params)
}

func (a *MsigAPI) MsigCancel(ctx context.Context, msig address.Address, txID uint64, to address.Address, amt types.BigInt, src address.Address, method uint64, params []byte) (cid.Cid, error) {
	return a.msigApproveOrCancel(ctx, api.MsigCancel, msig, txID, src, to, amt, src, method, params)
}

func (a *MsigAPI) msigApproveOrCancel(ctx context.Context, operation api.MsigProposeResponse, msig address.Address, txID uint64, proposer address.Address, to address.Address, amt types.BigInt, src address.Address, method uint64, params []byte) (cid.Cid, error) {
	if msig == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide multisig address")
	}

	if to == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide proposed target address")
	}

	if amt.Sign() == -1 {
		return cid.Undef, xerrors.Errorf("must provide the positive amount that was proposed")
	}

	if src == address.Undef {
		return cid.Undef, xerrors.Errorf("must provide source address")
	}

	if proposer.Protocol() != address.ID {
		proposerID, err := a.StateManagerAPI.LookupID(ctx, proposer, nil)
		if err != nil {
			return cid.Undef, err
		}
		proposer = proposerID
	}

	p := multisig0.ProposalHashData{
		Requester: proposer,
		To:        to,
		Value:     amt,
		Method:    abi.MethodNum(method),
		Params:    params,
	}

	pser, err := p.Serialize()
	if err != nil {
		return cid.Undef, err
	}
	phash := blake2b.Sum256(pser)

	enc, err := actors.SerializeParams(&multisig0.TxnIDParams{
		ID:           multisig0.TxnID(txID),
		ProposalHash: phash[:],
	})

	if err != nil {
		return cid.Undef, err
	}

	var msigResponseMethod abi.MethodNum

	/*
		We pass in a MsigProposeResponse instead of MethodNum to
		tighten the possible inputs to just Approve and Cancel.
	*/
	switch operation {
	case api.MsigApprove:
		msigResponseMethod = builtin0.MethodsMultisig.Approve
	case api.MsigCancel:
		msigResponseMethod = builtin0.MethodsMultisig.Cancel
	default:
		return cid.Undef, xerrors.Errorf("Invalid operation for msigApproveOrCancel")
	}

	msg := &types.Message{
		To:     msig,
		From:   src,
		Value:  types.NewInt(0),
		Method: msigResponseMethod,
		Params: enc,
	}

	smsg, err := a.MpoolAPI.MpoolPushMessage(ctx, msg, nil)
	if err != nil {
		return cid.Undef, err
	}

	return smsg.Cid(), nil
}

func serializeAddParams(new address.Address, inc bool) ([]byte, error) {
	enc, actErr := actors.SerializeParams(&multisig0.AddSignerParams{
		Signer:   new,
		Increase: inc,
	})
	if actErr != nil {
		return nil, actErr
	}

	return enc, nil
}

func serializeSwapParams(old address.Address, new address.Address) ([]byte, error) {
	enc, actErr := actors.SerializeParams(&multisig0.SwapSignerParams{
		From: old,
		To:   new,
	})
	if actErr != nil {
		return nil, actErr
	}

	return enc, nil
}
