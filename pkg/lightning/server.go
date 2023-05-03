package lightning

import (
	"Coin/pkg/address"
	"Coin/pkg/block"
	"Coin/pkg/peer"
	"Coin/pkg/pro"
	"Coin/pkg/script"
	"context"
	"time"
)

// Version was copied directly from pkg/server.go. Only changed the function receiver and types
func (ln *LightningNode) Version(ctx context.Context, in *pro.VersionRequest) (*pro.Empty, error) {
	// Reject all outdated versions (this is not true to Satoshi Client)
	if in.Version != ln.Config.Version {
		return &pro.Empty{}, nil
	}
	// If addr map is full or does not contain addr of ver, reject
	newAddr := address.New(in.AddrMe, uint32(time.Now().UnixNano()))
	if ln.AddressDB.Get(newAddr.Addr) != nil {
		err := ln.AddressDB.UpdateLastSeen(newAddr.Addr, newAddr.LastSeen)
		if err != nil {
			return &pro.Empty{}, nil
		}
	} else if err := ln.AddressDB.Add(newAddr); err != nil {
		return &pro.Empty{}, nil
	}
	newPeer := peer.New(ln.AddressDB.Get(newAddr.Addr), in.Version, in.BestHeight)
	// Check if we are waiting for a ver in response to a ver, do not respond if this is a confirmation of peering
	pendingVer := newPeer.Addr.SentVer != time.Time{} && newPeer.Addr.SentVer.Add(ln.Config.VersionTimeout).After(time.Now())
	if ln.PeerDb.Add(newPeer) && !pendingVer {
		newPeer.Addr.SentVer = time.Now()
		_, err := newAddr.VersionRPC(&pro.VersionRequest{
			Version:    ln.Config.Version,
			AddrYou:    in.AddrYou,
			AddrMe:     ln.Address,
			BestHeight: ln.BlockHeight,
		})
		if err != nil {
			return &pro.Empty{}, err
		}
	}
	return &pro.Empty{}, nil
}

// OpenChannel is called by another lightning node that wants to open a channel with us
func (ln *LightningNode) OpenChannel(ctx context.Context, in *pro.OpenChannelRequest) (*pro.OpenChannelResponse, error) {
	//TODO
	// establish a channel
	sender := ln.PeerDb.Get(in.Address)
	if (sender == nil){
		// stop and early return
		return nil, nil
	}
	// check if a channel is already created
	channel := ln.Channels[sender]
	if(channel != nil){
		return nil, nil // just return
	}

	// validate and sign off funding and refund
	fund := block.DecodeTransaction(in.FundingTransaction)
	refund := block.DecodeTransaction(in.RefundTransaction)
	errFund := ln.ValidateAndSign(fund)
	errRefund := ln.ValidateAndSign(refund)
	if(errFund != nil){
		return nil, errFund
	}
	if(errRefund != nil){
		return nil, errRefund
	}
	
	// open channel
	publicKey, privateKey := GenerateRevocationKey()
	// map private key
	myRevocationKeys := make(map[string][]byte)
	myRevocationKeys[refund.Hash()] = privateKey
	createdChannel := Channel{
		Funder: false,
		FundingTransaction: fund,
		State : 0,
		CounterPartyPubKey: in.GetPublicKey(),
		MyTransactions: []*block.Transaction{refund},
		TheirTransactions: []*block.Transaction{refund},
		MyRevocationKeys: myRevocationKeys,
		TheirRevocationKeys: make(map[string]*RevocationInfo),
	}
	ln.Channels[sender] = &createdChannel
	
	openChannelResp := pro.OpenChannelResponse{
		PublicKey: publicKey,
		SignedFundingTransaction: block.EncodeTransaction(fund),
		SignedRefundTransaction: block.EncodeTransaction(refund),
	}
	return &openChannelResp, nil
}

func (ln *LightningNode) GetUpdatedTransactions(ctx context.Context, in *pro.TransactionWithAddress) (*pro.UpdatedTransactions, error) {
	// TODO
	// validate the address
	address := in.Address
	peer := ln.PeerDb.Get(address)
	if(peer == nil){
		return nil, nil
	}
	// sign the txn
	txn := block.DecodeTransaction(in.Transaction)
	ln.SignTransaction(txn)
	// generate new revo key pair
	publicKey, privateKey := GenerateRevocationKey()
	// generate transaction
	newlySignedTxn := ln.generateTransactionWithCorrectScripts(peer, txn, publicKey)
	// add to their txn
	channel := ln.Channels[peer]
	channel.TheirTransactions = append(channel.TheirTransactions, txn)
	channel.MyRevocationKeys[newlySignedTxn.Hash()] = privateKey

	return &pro.UpdatedTransactions{
		SignedTransaction: block.EncodeTransaction(txn),
		UnsignedTransaction: block.EncodeTransaction(newlySignedTxn),
	}, nil
}

func (ln *LightningNode) GetRevocationKey(ctx context.Context, in *pro.SignedTransactionWithKey) (*pro.RevocationKey, error) {
	// TODO
	// validate the address
	address := in.Address
	peer := ln.PeerDb.Get(address)
	if(peer == nil){
		return nil, nil
	}
	// add newly signed to mytxn
	channel := ln.Channels[peer]
	txn := block.DecodeTransaction(in.SignedTransaction)
	channel.MyTransactions = append(channel.MyTransactions, txn)
	// create revinfo
	theirCoin := 1
	if channel.Funder{
		theirCoin = 0
	}
	// determine correct script
	scriptType, err := script.DetermineScriptType(in.SignedTransaction.Outputs[theirCoin].LockingScript)
	if(err != nil){
		return nil, err
	}

	revInfo := RevocationInfo{
		RevKey: in.RevocationKey,
		TransactionOutput: txn.Outputs[theirCoin],
		OutputIndex: uint32(theirCoin),
		TransactionHash: txn.Hash(),
		ScriptType: scriptType,
	}
	// add to theirRevKey
	channel.TheirRevocationKeys[txn.Hash()] = &revInfo

	// increment state
	channel.State += 1

	myRevKey := channel.MyRevocationKeys[txn.Hash()]
	return &pro.RevocationKey{Key: myRevKey}, nil
}
