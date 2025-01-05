package main

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/offchainlabs/nitro/arbnode"
	"github.com/offchainlabs/nitro/arbos/arbostypes"
	"github.com/offchainlabs/nitro/arbutil"
)

type MultiplexerBackend struct {
	batchSeqNum           uint64
	batches               map[uint64]*arbnode.SequencerInboxBatch
	delayedMessages       map[uint64]*arbostypes.L1IncomingMessage
	positionWithinMessage uint64

	ctx    context.Context
	client arbutil.L1Interface
}

func (b *MultiplexerBackend) PeekSequencerInbox() ([]byte, common.Hash, error) {
	seqNum := b.batchSeqNum
	bytes, error := b.batches[seqNum].Serialize(b.ctx, b.client)
	return bytes, b.batches[seqNum].BlockHash, error
}

func (b *MultiplexerBackend) GetBatchDataByNum(batchNum uint64) ([]byte, error) {
	if b.batches[batchNum] == nil {
		return nil, ErrUnknownBatch
	}
	return b.batches[batchNum].Serialize(b.ctx, b.client)
}

func (b *MultiplexerBackend) ReadInboxMessage(batchNum uint64) (*arbnode.SequencerInboxBatch, common.Hash, error) {
	if b.batches[batchNum] == nil {
		return nil, common.Hash{}, ErrUnknownBatch
	}
	return b.batches[batchNum], b.batches[batchNum].BlockHash, nil
}

func (b *MultiplexerBackend) SetInboxMessage(batchNum uint64, batch *arbnode.SequencerInboxBatch) (bool, error) {
	if b.batches == nil {
		b.batches = make(map[uint64]*arbnode.SequencerInboxBatch)
	}
	if b.batches[batchNum] != nil {
		return false, ErrOverwritingSeqMsg
	}
	b.batches[batchNum] = batch
	return true, nil
}

func (b *MultiplexerBackend) GetSequencerInboxPosition() uint64 {
	return b.batchSeqNum
}

func (b *MultiplexerBackend) AdvanceSequencerInbox() {
	b.batchSeqNum++
}

func (b *MultiplexerBackend) GetPositionWithinMessage() uint64 {
	return b.positionWithinMessage
}

func (b *MultiplexerBackend) SetPositionWithinMessage(pos uint64) {
	b.positionWithinMessage = pos
}

func (b *MultiplexerBackend) ReadDelayedInbox(seqNum uint64) (*arbostypes.L1IncomingMessage, error) {
	if len(b.delayedMessages) == 0 {
		return nil, ErrEmptyDelayedMsg
	}
	if b.delayedMessages[seqNum] == nil {
		return nil, ErrUnknownDelayedMsg
	}
	return b.delayedMessages[seqNum], nil
}

func (b *MultiplexerBackend) SetDelayedMsg(seqNum uint64, msg *arbostypes.L1IncomingMessage) (bool, error) {
	if b.delayedMessages == nil {
		b.delayedMessages = make(map[uint64]*arbostypes.L1IncomingMessage)
	}
	if b.delayedMessages[seqNum] != nil {
		return false, ErrOverwritingDelayedMsg
	}
	b.delayedMessages[seqNum] = msg
	return true, nil
}
