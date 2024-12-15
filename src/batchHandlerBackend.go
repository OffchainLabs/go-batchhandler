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
	batch                 *arbnode.SequencerInboxBatch
	delayedMessage        map[uint64]*arbostypes.L1IncomingMessage
	positionWithinMessage uint64

	ctx    context.Context
	client arbutil.L1Interface
}

func (b *MultiplexerBackend) PeekSequencerInbox() ([]byte, common.Hash, error) {
	if b.batchSeqNum != b.batch.SequenceNumber {
		return nil, common.Hash{}, ErrUnknownBatch
	}
	bytes, error := b.batch.Serialize(b.ctx, b.client)
	return bytes, b.batch.BlockHash, error
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
	if len(b.delayedMessage) == 0 {
		return nil, ErrEmptyDelayedMsg
	}
	if b.delayedMessage[seqNum] == nil {
		return nil, ErrUnknownDelayedMsg
	}
	return b.delayedMessage[seqNum], nil
}

func (b *MultiplexerBackend) SetDelayedMsg(seqNum uint64, msg *arbostypes.L1IncomingMessage) (bool, error) {
	if b.delayedMessage[seqNum] != nil {
		return false, ErrOverwritingDelayedMsg
	}
	b.delayedMessage[seqNum] = msg
	return true, nil
}
