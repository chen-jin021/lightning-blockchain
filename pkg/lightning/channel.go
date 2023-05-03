package lightning

import (
	"Coin/pkg/block"
	"Coin/pkg/id"
	"Coin/pkg/peer"
	"Coin/pkg/pro"
	"Coin/pkg/script"
)

// Channel is our node's view of a channel
// Funder is whether we are the channel's funder
// FundingTransaction is the channel's funding transaction
// CounterPartyPubKey is the other node's public key
// State is the current state that we are at. On instantiation,
// the refund transaction is the transaction for state 0
// Transactions is the slice of transactions, indexed by state
// MyRevocationKeys is a mapping of my private revocation keys
// TheirRevocationKeys is a mapping of their private revocation keys
type Channel struct {
	Funder             bool
	FundingTransaction *block.Transaction
	State              int
	CounterPartyPubKey []byte

	MyTransactions    []*block.Transaction
	TheirTransactions []*block.Transaction

	MyRevocationKeys    map[string][]byte
	TheirRevocationKeys map[string]*RevocationInfo
}

type RevocationInfo struct {
	RevKey            []byte
	TransactionOutput *block.TransactionOutput
	OutputIndex       uint32
	TransactionHash   string
	ScriptType        int
}

// GenerateRevocationKey returns a new public, private key pair
func GenerateRevocationKey() ([]byte, []byte) {
	i, _ := id.CreateSimpleID()
	return i.GetPublicKeyBytes(), i.GetPrivateKeyBytes()
}

// CreateChannel creates a channel with another lightning node
// fee must be enough to cover two transactions! You will get back change from first
func (ln *LightningNode) CreateChannel(peer *peer.Peer, theirPubKey []byte, amount uint32, fee uint32) {
	// TODO
	// make wallet request
	wr := WalletRequest{
		Amount: amount,
		Fee: fee,
		CounterPartyPubKey: theirPubKey,
	}
	// make rev pair
	publicKey, privateKey := GenerateRevocationKey()
	// refund txn
	fund := ln.generateFundingTransaction(wr)
	refund := ln.generateRefundTransaction(theirPubKey, fund, fee, publicKey)
	// add private rev key to myrevkey
	myRevKey := make(map[string][]byte)
	myRevKey[refund.Hash()] = privateKey

	// request open channel
	chanReq := pro.OpenChannelRequest{
		Address: ln.Address,
		PublicKey: ln.Id.GetPublicKeyBytes(),
		FundingTransaction: block.EncodeTransaction(fund),
		RefundTransaction: block.EncodeTransaction(refund),
	}

	res, err := peer.Addr.OpenChannelRPC(&chanReq)
	if(err != nil){
		return
	}

	// store refund as both party's first txn
	channel := Channel{
		Funder: true,
		FundingTransaction: nil,
		State : 0,
		CounterPartyPubKey: theirPubKey,
		MyTransactions: []*block.Transaction{},
		TheirTransactions: []*block.Transaction{},
		MyRevocationKeys: myRevKey,
		TheirRevocationKeys: make(map[string]*RevocationInfo),
	}
	ln.Channels[peer] = &channel
	fundTxn := block.DecodeTransaction(res.SignedFundingTransaction)
	refundTxn := block.DecodeTransaction(res.SignedRefundTransaction)

	channel.FundingTransaction = fundTxn
	channel.MyTransactions = append(channel.MyTransactions, refundTxn)
	channel.TheirTransactions = append(channel.TheirTransactions, refundTxn)

	// broadcast
	go func(){
		ln.BroadcastTransaction <- fundTxn
	}()
}

// UpdateState is called to update the state of a channel.
func (ln *LightningNode) UpdateState(peer *peer.Peer, tx *block.Transaction) {
	// TODO
	// request and get updated txn from peer
	txnWithAddr := block.EncodeTransactionWithAddress(tx, ln.Address)
	updated, _ := peer.Addr.GetUpdatedTransactionsRPC(txnWithAddr)
	signedtxn := block.DecodeTransaction(updated.SignedTransaction)
	unsignedtxn := block.DecodeTransaction(updated.UnsignedTransaction)
	// get channel
	channel := ln.Channels[peer]
	channel.MyTransactions = append(channel.MyTransactions, signedtxn)
	ln.SignTransaction(unsignedtxn) // sign their transaction
	channel.TheirTransactions = append(channel.TheirTransactions, unsignedtxn)

	// get their rev key
	req := pro.SignedTransactionWithKey{
		SignedTransaction: block.EncodeTransaction(unsignedtxn),
		RevocationKey: channel.MyRevocationKeys[tx.Hash()],
		Address : ln.Address,
	}
	theirRevKey, err := peer.Addr.GetRevocationKeyRPC(&req)
	if(err!= nil){
		return
	}

	// got rev key, update state
	channel.State += 1
	// create revinfo
	theirCoin := 0
	if channel.Funder{
		theirCoin = 1
	}
	// determine correct script
	scriptType, _ := script.DetermineScriptType(unsignedtxn.Outputs[theirCoin].LockingScript)

	revInfo := RevocationInfo{
		RevKey: theirRevKey.Key,
		TransactionOutput: unsignedtxn.Outputs[theirCoin],
		OutputIndex: uint32(theirCoin),
		TransactionHash: unsignedtxn.Hash(),
		ScriptType: scriptType,
	}
	channel.TheirRevocationKeys[tx.Hash()] = &revInfo
}
