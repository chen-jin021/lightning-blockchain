package lightning

import (
	"Coin/pkg/block"
	"Coin/pkg/id"
)

type WatchTower struct {
	Id id.ID
	// do we want to make this a database? It could theoretically be very large (numChannels * numKeys)
	RevocationKeys map[string]*RevocationInfo
	// Channel to send a "caught" transaction to the node (and then to the wallet)
	RevokedTransactions chan *RevocationInfo
}

//HandleBlock handles a block and figures out if we need to revoke a transaction
func (w *WatchTower) HandleBlock(block *block.Block) *RevocationInfo {
	// TODO
	// if any txn is what we are monitoring
	for _, txn := range block.Transactions{
		if rev, ok := w.RevocationKeys[txn.Hash()]; ok{
			go func(){
				w.RevokedTransactions <- rev
			}()
			return rev
		}
	}
	return nil
}
